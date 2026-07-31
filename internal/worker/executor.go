package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"text/template"
	"time"

	nsjailcfg "minos/configs/nsjail"
	"minos/internal/config"
	"minos/internal/models"
)

// Executor runs submissions inside nsjail sandboxes.
type Executor struct {
	cfg   config.Config
	tmpl  *template.Template
	langs map[string]Language
	log   *slog.Logger
}

// NewExecutor validates the setup (nsjail present, template parses) so
// configuration errors surface at startup, not on the first job.
func NewExecutor(cfg config.Config, log *slog.Logger) (*Executor, error) {
	if _, err := exec.LookPath(cfg.NsjailBin); err != nil {
		return nil, fmt.Errorf("nsjail binary %q not found: %w", cfg.NsjailBin, err)
	}
	tmpl, err := template.New("nsjail").Parse(nsjailcfg.BaseTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse nsjail config template: %w", err)
	}
	return &Executor{
		cfg:   cfg,
		tmpl:  tmpl,
		langs: DefaultLanguages(),
		log:   log,
	}, nil
}

// Execute runs a job to completion and returns its result. It never panics
// and always returns a result infra errors become ExitCode 255 so the API
// gateway always gets an answer.
func (e *Executor) Execute(ctx context.Context, job models.Job) models.ExecutionResult {
	lang, ok := e.langs[job.Language]
	if !ok {
		return models.ExecutionResult{
			ExitCode: 255,
			Stderr:   fmt.Sprintf("unsupported language: %q", job.Language),
		}
	}

	ws, err := NewWorkspace(e.cfg.TmpDir, job.ID)
	if err != nil {
		return models.ExecutionResult{ExitCode: 255, Stderr: "failed to create workspace"}
	}
	// Deferred cleanup runs no matter what happens.
	defer func() {
		if err := ws.Cleanup(); err != nil {
			e.log.Warn("workspace cleanup failed", "job_id", job.ID, "error", err)
		}
	}()

	if err := ws.WriteAppFile(lang.SourceFile, []byte(job.Code), 0o400); err != nil {
		e.log.Error("write source failed", "job_id", job.ID, "error", err)
		return models.ExecutionResult{ExitCode: 255, Stderr: "failed to write source file"}
	}
	if job.Stdin != "" {
		if err := ws.WriteAppFile("stdin.txt", []byte(job.Stdin), 0o400); err != nil {
			e.log.Error("write stdin failed", "job_id", job.ID, "error", err)
			return models.ExecutionResult{ExitCode: 255, Stderr: "failed to write stdin file"}
		}
	}

	if lang.CompilePath != "" {
		compileRes := e.runPhase(ctx, ws, job.ID, lang, compilePhase(lang))
		if compileRes.ExitCode != 0 {
			compileRes.Stderr = "Compilation error:\n" + compileRes.Stderr
			return compileRes
		}
	}

	return e.runPhase(ctx, ws, job.ID, lang, runPhase(lang))
}

// execPhase is a single nsjail invocation (compile or run).
type execPhase struct {
	Name        string // "compile" | "run"
	TimeLimit   int    // seconds
	Memory      uint64 // bytes
	MaxProcs    uint64
	MaxFileSize uint64 // bytes
	ExecPath    string
	ExecArgs    []string
	Env         []string
}

func compilePhase(l Language) execPhase {
	return execPhase{
		Name:        "compile",
		TimeLimit:   l.CompileTimeLimit,
		Memory:      l.CompileMemory,
		MaxProcs:    l.CompileMaxProcs,
		MaxFileSize: l.CompileMaxFSize,
		ExecPath:    l.CompilePath,
		ExecArgs:    l.CompileArgs,
		Env:         l.CompileEnv,
	}
}

func runPhase(l Language) execPhase {
	return execPhase{
		Name:        "run",
		TimeLimit:   l.TimeLimit,
		Memory:      l.Memory,
		MaxProcs:    l.MaxProcs,
		MaxFileSize: l.MaxFileSize,
		ExecPath:    l.ExecPath,
		ExecArgs:    l.ExecArgs,
		Env:         l.Env,
	}
}

// renderConfig writes the per-phase nsjail config into the workspace
// (outside the /app bind mount) and returns its path.
func (e *Executor) renderConfig(ws *Workspace, lang Language, ph execPhase) (string, error) {
	data := struct {
		Name           string
		TimeLimit      int
		Memory         uint64
		MaxProcs       uint64
		MaxFileSize    uint64
		ExecPath       string
		ExecArgs       []string
		Env            []string
		AppDir         string
		CloneNewNet    bool
		CloneNewPID    bool
		CloneNewCgroup bool
	}{
		Name:           lang.Name + "-" + ph.Name,
		TimeLimit:      ph.TimeLimit,
		Memory:         ph.Memory,
		MaxProcs:       ph.MaxProcs,
		MaxFileSize:    ph.MaxFileSize,
		ExecPath:       ph.ExecPath,
		ExecArgs:       ph.ExecArgs,
		Env:            ph.Env,
		AppDir:         ws.AppDir,
		CloneNewNet:    e.cfg.CloneNewNet,
		CloneNewPID:    e.cfg.CloneNewPID,
		CloneNewCgroup: e.cfg.CloneNewCgroup,
	}

	var buf bytes.Buffer
	if err := e.tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render config: %w", err)
	}
	return ws.WriteFile("nsjail-"+ph.Name+".cfg", buf.Bytes(), 0o600)
}

// runPhase executes one nsjail invocation with layered timeouts:
//
//  1. nsjail's own time_limit / rlimit_cpu (primary, kernel-enforced)
//  2. Go context deadline = TimeLimit + 2s (fallback — belt + suspenders)
//  3. WaitDelay caps the time we can ever block on inherited pipes
func (e *Executor) runPhase(ctx context.Context, ws *Workspace, jobID string, lang Language, ph execPhase) models.ExecutionResult {
	res := models.ExecutionResult{}

	cfgPath, err := e.renderConfig(ws, lang, ph)
	if err != nil {
		e.log.Error("render nsjail config failed", "job_id", jobID, "error", err)
		res.ExitCode = 255
		res.Stderr = "failed to prepare sandbox"
		return res
	}

	// Fallback timeout: 2s of slack over nsjail's own time_limit.
	timeout := time.Duration(ph.TimeLimit)*time.Second + 2*time.Second
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	logPath := ws.LogPath(ph.Name)
	// --log keeps nsjail's own chatter out of the user's stderr.
	cmd := exec.CommandContext(tctx, e.cfg.NsjailBin, "--log", logPath, "--config", cfgPath)

	// New process group so the fallback kill takes out the whole tree
	// (nsjail + jailed process + any grandchildren), not just nsjail.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	// If a grandchild inherited our stdout/stderr pipes, don't block on
	// them forever after the kill.
	cmd.WaitDelay = 5 * time.Second

	stdout := newBoundedWriter(e.cfg.MaxOutputSize)
	stderr := newBoundedWriter(e.cfg.MaxOutputSize)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if stdin, err := ws.OpenAppFile("stdin.txt"); err == nil {
		defer stdin.Close()
		cmd.Stdin = stdin
	}

	start := time.Now()
	runErr := cmd.Run()
	res.TimeMs = time.Since(start).Milliseconds()

	res.Stdout = stdout.String()
	res.Stderr = stderr.String()
	res.Truncated = stdout.Truncated() || stderr.Truncated()

	switch {
	case runErr == nil:
		res.ExitCode = 0
	case errors.Is(tctx.Err(), context.DeadlineExceeded):
		// Go fallback fired: nsjail's own timer should have handled this,
		// so log it — it may indicate nsjail misconfiguration.
		e.log.Warn("context timeout fired (nsjail time_limit did not)",
			"job_id", jobID, "phase", ph.Name, "limit_s", ph.TimeLimit)
		res.ExitCode = 137 // SIGKILL
		res.Stderr = "Execution timed out"
	case errors.Is(tctx.Err(), context.Canceled):
		res.ExitCode = 137
		res.Stderr = "Worker shutting down"
	default:
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			if res.ExitCode < 0 {
				// Killed by a signal before an exit code existed.
				res.ExitCode = 137
			}
		} else {
			// nsjail itself failed to start (bad config, missing binary...).
			res.ExitCode = 255
			res.Stderr = "failed to start sandbox"
			e.log.Error("nsjail spawn failed",
				"job_id", jobID, "phase", ph.Name, "error", runErr,
				"nsjail_log", readTail(logPath, 2048))
		}
	}

	// nsjail's own timer is the primary enforcement: time_limit kills with
	// SIGKILL (137), rlimit_cpu with SIGXCPU (152). Normalize those into a
	// clear timeout signal when the phase burned its full wall-clock budget.
	if res.TimeMs >= int64(ph.TimeLimit)*1000 && (res.ExitCode == 137 || res.ExitCode == 152) &&
		!strings.Contains(res.Stderr, "Execution timed out") {
		if res.Stderr == "" {
			res.Stderr = "Execution timed out"
		} else {
			res.Stderr += "\nExecution timed out"
		}
	}
	return res
}

// readTail returns the last n bytes of a file, for diagnostics. Best-effort.
func readTail(path string, n int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return ""
	}
	off := max(st.Size()-n, 0)
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil {
		return ""
	}
	return string(buf)
}
