package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"minos/internal/config"
	"minos/internal/models"
	"minos/internal/redisq"
)

func newTestServer(t *testing.T) (*Server, *redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	cfg := config.APIConfig{
		APIKey:           "test-secret",
		RedisAddr:        mr.Addr(),
		QueueKey:         "minos:queue",
		ResultPrefix:     "minos:result:",
		ResultTTL:        5 * time.Minute,
		MaxCodeSize:      65536,
		MaxStdinSize:     1024,
		ExecutionTimeout: 2 * time.Second,
	}
	rdb := redisq.NewClient(mr.Addr(), "", 0)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewServer(cfg, rdb, log), rdb, mr
}

func doRequest(t *testing.T, s *Server, method, path, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if key != "" {
		req.Header.Set(APIKeyHeader, key)
	}
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	return v
}

func TestExecuteRequiresAPIKey(t *testing.T) {
	s, _, _ := newTestServer(t)

	// No key.
	w := doRequest(t, s, "POST", "/api/v1/execute", `{"language":"python","code":"print(1)"}`, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no key: status = %d, want 401", w.Code)
	}

	// Wrong key.
	w = doRequest(t, s, "POST", "/api/v1/execute", `{"language":"python","code":"print(1)"}`, "wrong-key")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key: status = %d, want 401", w.Code)
	}

	// Bearer header also works.
	req := httptest.NewRequest("POST", "/api/v1/execute", bytes.NewBufferString(`{"language":"python","code":"print(1)"}`))
	req.Header.Set("Authorization", "Bearer test-secret")
	w = httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)
	if w.Code == http.StatusUnauthorized {
		t.Fatal("valid bearer token rejected")
	}
}

func TestExecuteSynchronous(t *testing.T) {
	s, rdb, _ := newTestServer(t)

	// POST /execute blocks (BLPOP) until the worker pushes a result. Run it
	// in a goroutine and simulate the worker after the job hits the queue.
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- doRequest(t, s, "POST", "/api/v1/execute",
			`{"language":"python","code":"print(42)","stdin":"hi"}`, "test-secret")
	}()

	// Wait for the job to be queued, then act as the worker and push a result.
	var jobID string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		vals, err := rdb.LRange(t.Context(), "minos:queue", 0, -1).Result()
		if err == nil && len(vals) > 0 {
			var job models.Job
			if json.Unmarshal([]byte(vals[0]), &job) == nil && job.ID != "" {
				jobID = job.ID
				if job.Submitter != "test-secret" {
					t.Fatalf("submitter = %q, want %q", job.Submitter, "test-secret")
				}
				if job.Language != "python" || job.Stdin != "hi" {
					t.Fatalf("job fields mismatch: %+v", job)
				}
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if jobID == "" {
		t.Fatal("job never reached the queue")
	}

	res := models.ExecutionResult{Stdout: "42\n", Stderr: "", ExitCode: 0, TimeMs: 22}
	payload, _ := json.Marshal(res)
	if err := rdb.LPush(t.Context(), "minos:result:"+jobID, payload).Err(); err != nil {
		t.Fatalf("simulate worker: %v", err)
	}

	w := <-done
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	got := decode[JobResponse](t, w)
	if got.Status != "completed" {
		t.Fatalf("status = %q, want completed", got.Status)
	}
	if got.Stdout != "42\n" || got.ExitCode != 0 || got.TimeMs != 22 {
		t.Fatalf("result mismatch: %+v", got)
	}
	if got.JobID != jobID {
		t.Fatalf("job_id = %q, want %q", got.JobID, jobID)
	}
}

func TestExecuteTimesOutWithoutWorker(t *testing.T) {
	s, rdb, _ := newTestServer(t)
	// No worker will push a result — the request must return 504 after the
	// wait window, with a job_id so the client can poll.
	w := doRequest(t, s, "POST", "/api/v1/execute",
		`{"language":"python","code":"print(1)"}`, "test-secret")
	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504 (body %s)", w.Code, w.Body.String())
	}
	got := decode[JobResponse](t, w)
	if got.Status != "processing" {
		t.Fatalf("status = %q, want processing", got.Status)
	}
	if got.JobID == "" {
		t.Fatal("504 response must carry job_id for polling")
	}
	// Job must still be in the queue (never picked up).
	if n := rdb.LLen(t.Context(), "minos:queue").Val(); n != 1 {
		t.Fatalf("queue length = %d, want 1", n)
	}
}

func TestExecuteValidation(t *testing.T) {
	s, rdb, _ := newTestServer(t)

	cases := []struct {
		name string
		body string
	}{
		{"empty", `{}`},
		{"unsupported language", `{"language":"cobol","code":"DISPLAY 'HI'."}`},
		{"empty code", `{"language":"python","code":""}`},
		{"code too large", `{"language":"python","code":"` + strings.Repeat("a", 65537) + `"}`},
		{"invalid utf8", "{\"language\":\"python\",\"code\":\"\xff\xfe\"}"},
		{"null bytes", `{"language":"python","code":"print(1)\x00"}`},
		{"stdin too large", `{"language":"python","code":"print(1)","stdin":"` + strings.Repeat("b", 1025) + `"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doRequest(t, s, "POST", "/api/v1/execute", tc.body, "test-secret")
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", w.Code, w.Body.String())
			}
		})
	}

	// Nothing should have been queued by any of the rejected submissions.
	if n := rdb.LLen(t.Context(), "minos:queue").Val(); n != 0 {
		t.Fatalf("queue length = %d, want 0", n)
	}
}

func TestGetJobProcessingThenCompleted(t *testing.T) {
	s, rdb, _ := newTestServer(t)

	// Push a result as the worker would, and submit nothing — just poll a
	// fresh id first to observe "processing".
	id := "job-0001"
	w := doRequest(t, s, "GET", "/api/v1/jobs/"+id, "", "test-secret")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := decode[JobResponse](t, w); got.Status != "processing" {
		t.Fatalf("status = %q, want processing", got.Status)
	}

	// Now simulate the worker pushing a result.
	res := models.ExecutionResult{Stdout: "42\n", Stderr: "", ExitCode: 0, TimeMs: 22}
	payload, _ := json.Marshal(res)
	if err := rdb.LPush(t.Context(), "minos:result:"+id, payload).Err(); err != nil {
		t.Fatalf("seed result: %v", err)
	}

	w = doRequest(t, s, "GET", "/api/v1/jobs/"+id, "", "test-secret")
	got := decode[JobResponse](t, w)
	if got.Status != "completed" {
		t.Fatalf("status = %q, want completed", got.Status)
	}
	if got.Stdout != "42\n" || got.ExitCode != 0 || got.TimeMs != 22 {
		t.Fatalf("result mismatch: %+v", got)
	}
}

func TestGetJobRequiresAPIKey(t *testing.T) {
	s, _, _ := newTestServer(t)
	w := doRequest(t, s, "GET", "/api/v1/jobs/abc", "", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestGetJobInvalidID(t *testing.T) {
	s, _, _ := newTestServer(t)
	for _, id := range []string{"..", "a b", "inv@lid", "x\x00y"} {
		esc := url.PathEscape(id)
		w := doRequest(t, s, "GET", "/api/v1/jobs/"+esc, "", "test-secret")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("id %q (esc %q): status = %d, want 400", id, esc, w.Code)
		}
	}
}

func TestHealthEndpointsUnauthenticated(t *testing.T) {
	s, _, _ := newTestServer(t)

	w := doRequest(t, s, "GET", "/healthz", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", w.Code)
	}
	w = doRequest(t, s, "GET", "/readyz", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("readyz status = %d, want 200", w.Code)
	}
}
