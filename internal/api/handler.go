package api

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"minos/internal/models"
)

// Response envelopes.
type (
	// ErrorResponse is returned for any 4xx/5xx.
	ErrorResponse struct {
		Error string `json:"error"`
	}

	// JobResponse is the execution result returned by POST /execute and
	// GET /jobs/:id. Status is "completed" once the worker has produced a
	// result, "processing" if the sync wait expired and the client should
	// poll GET /jobs/:id.
	JobResponse struct {
		JobID     string `json:"job_id"`
		Status    string `json:"status"`
		Stdout    string `json:"stdout"`
		Stderr    string `json:"stderr"`
		ExitCode  int    `json:"exit_code"`
		TimeMs    int64  `json:"time_ms"`
		Truncated bool   `json:"truncated,omitempty"`
		Error     string `json:"error,omitempty"`
	}
)

// jobIDRe guards the :id path param before it is used in a Redis key.
var jobIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// execute validates a submission, pushes it to the queue, then BLOCKS on the
// result key until the worker produces a result — the client gets stdout,
// stderr, exit code and timing in the same HTTP response (synchronous model,
// ARCHITECTURE.md sections 3 & 5.1).
//
// If the wait window (EXECUTION_TIMEOUT) expires — slow worker, queue
// backlog — it returns 504 with the job_id so the client can poll
// GET /api/v1/jobs/{id}.
func (s *Server) execute(c *gin.Context) {
	// Validate the raw body is UTF-8 BEFORE JSON parsing — encoding/json
	// silently replaces invalid sequences with U+FFFD, hiding garbage.
	body, err := io.ReadAll(c.Request.Body)
	if err != nil || !utf8.Valid(body) {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "invalid payload"})
		return
	}
	var req models.SubmissionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "invalid payload"})
		return
	}
	if err := s.validate(req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	job := models.Job{
		ID:        uuid.NewString(),
		Language:  req.Language,
		Code:      req.Code,
		Stdin:     req.Stdin,
		Submitter: c.GetString("api_key"),
		Created:   time.Now().UTC(),
	}

	if err := s.queue.Push(c.Request.Context(), job); err != nil {
		s.log.Error("queue push failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "queue push failed"})
		return
	}

	s.log.Info("job submitted",
		"job_id", job.ID,
		"language", job.Language,
		"code_size", len(job.Code),
		"submitter", job.Submitter,
	)

	// Block-wait for the worker's result on the same request. c.Request.Context()
	// aborts the BLPOP if the client disconnects.
	res, found, err := s.results.Wait(c.Request.Context(), job.ID, s.cfg.ExecutionTimeout)
	if err != nil {
		s.log.Error("waiting for result failed", "job_id", job.ID, "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "internal error"})
		return
	}
	if !found {
		s.log.Info("execution timeout, job still in flight", "job_id", job.ID)
		c.JSON(http.StatusGatewayTimeout, JobResponse{
			JobID:  job.ID,
			Status: "processing",
			Error:  "execution timed out, poll GET /api/v1/jobs/" + job.ID,
		})
		return
	}

	s.log.Info("job completed",
		"job_id", job.ID,
		"exit_code", res.ExitCode,
		"time_ms", res.TimeMs,
		"stdout_size", len(res.Stdout),
		"stderr_size", len(res.Stderr),
	)

	c.JSON(http.StatusOK, JobResponse{
		JobID:     job.ID,
		Status:    "completed",
		Stdout:    res.Stdout,
		Stderr:    res.Stderr,
		ExitCode:  res.ExitCode,
		TimeMs:    res.TimeMs,
		Truncated: res.Truncated,
	})
}

// getJob returns the execution result for a submitted job, or "processing"
// while the worker is still running it (result key not yet present).
func (s *Server) getJob(c *gin.Context) {
	id := c.Param("id")
	if !jobIDRe.MatchString(id) {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "invalid job id"})
		return
	}

	res, found, err := s.results.Get(c.Request.Context(), id)
	if err != nil {
		s.log.Error("fetch result failed", "job_id", id, "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "internal error"})
		return
	}
	if !found {
		c.JSON(http.StatusOK, JobResponse{JobID: id, Status: "processing"})
		return
	}

	c.JSON(http.StatusOK, JobResponse{
		JobID:     id,
		Status:    "completed",
		Stdout:    res.Stdout,
		Stderr:    res.Stderr,
		ExitCode:  res.ExitCode,
		TimeMs:    res.TimeMs,
		Truncated: res.Truncated,
	})
}
