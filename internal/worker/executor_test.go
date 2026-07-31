package worker

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"minos/internal/config"
	"minos/internal/models"
)

// fakeNsjail is a shell script that mimics nsjail's observable behavior:
// it prints, sleeps, floods, or exits based on FAKE_* env vars. The compile
// phase is detected from the --config path containing "compile".
const fakeNsjail = `#!/bin/sh
phase=run
for arg in "$@"; do
  case "$arg" in
    *compile*) phase=compile ;;
  esac
done
if [ "$phase" = "compile" ]; then
  printf '%s' "${FAKE_COMPILE_STDOUT:-}"
  printf '%s' "${FAKE_COMPILE_STDERR:-}" >&2
  exit "${FAKE_COMPILE_EXIT:-0}"
fi
if [ -n "$FAKE_CAT_STDIN" ]; then
  cat
fi
printf '%s' "${FAKE_STDOUT:-}"
printf '%s' "${FAKE_STDERR:-}" >&2
if [ -n "$FAKE_FLOOD" ]; then
  head -c "$FAKE_FLOOD" /dev/zero | tr '\0' 'x'
fi
if [ -n "$FAKE_SLEEP" ]; then
  sleep "$FAKE_SLEEP"
fi
exit "${FAKE_EXIT:-0}"
`

// newTestExecutor builds an Executor backed by the fake nsjail.
func newTestExecutor(t *testing.T, cfgMut func(*config.Config)) (*Executor, string) {
	t.Helper()

	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "nsjail")
	if err := os.WriteFile(binPath, []byte(fakeNsjail), 0o755); err != nil {
		t.Fatalf("write fake nsjail: %v", err)
	}

	cfg := config.Config{
		TmpDir:        t.TempDir(),
		MaxOutputSize: 65536,
		NsjailBin:     binPath,
	}
	if cfgMut != nil {
		cfgMut(&cfg)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ex, err := NewExecutor(cfg, log)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	return ex, cfg.TmpDir
}

func job(lang, code string) models.Job {
	return models.Job{
		ID:       "test-job-1",
		Language: lang,
		Code:     code,
		Created:  time.Now(),
	}
}

func TestExecuteSuccess(t *testing.T) {
	t.Setenv("FAKE_STDOUT", "hello world\n")
	ex, _ := newTestExecutor(t, nil)

	res := ex.Execute(context.Background(), job("python", "print('hello world')"))
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", res.ExitCode, res.Stderr)
	}
	if res.Stdout != "hello world\n" {
		t.Fatalf("stdout = %q, want %q", res.Stdout, "hello world\n")
	}
	if res.Stderr != "" {
		t.Fatalf("stderr = %q, want empty", res.Stderr)
	}
	if res.TimeMs < 0 {
		t.Fatalf("time_ms = %d, want >= 0", res.TimeMs)
	}
	if res.Truncated {
		t.Fatal("truncated = true, want false")
	}
}

func TestExecuteExitCodePassthrough(t *testing.T) {
	t.Setenv("FAKE_EXIT", "42")
	ex, _ := newTestExecutor(t, nil)

	res := ex.Execute(context.Background(), job("python", "import sys; sys.exit(42)"))
	if res.ExitCode != 42 {
		t.Fatalf("exit code = %d, want 42", res.ExitCode)
	}
}

func TestExecuteStderrCaptured(t *testing.T) {
	t.Setenv("FAKE_EXIT", "1")
	t.Setenv("FAKE_STDERR", "Traceback (most recent call last)...")
	ex, _ := newTestExecutor(t, nil)

	res := ex.Execute(context.Background(), job("python", "1/0"))
	if res.ExitCode != 1 {
		t.Fatalf("exit code = %d, want 1", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "Traceback") {
		t.Fatalf("stderr = %q, want it to contain %q", res.Stderr, "Traceback")
	}
}

func TestExecuteStdinForwarded(t *testing.T) {
	t.Setenv("FAKE_CAT_STDIN", "1")
	ex, _ := newTestExecutor(t, nil)

	j := job("python", "print(input())")
	j.Stdin = "input-data-123"
	res := ex.Execute(context.Background(), j)
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "input-data-123") {
		t.Fatalf("stdout = %q, want it to contain stdin payload", res.Stdout)
	}
}

func TestExecuteTimeout(t *testing.T) {
	t.Setenv("FAKE_SLEEP", "30") // way past the 3s ctx fallback (1s limit + 2s)
	ex, _ := newTestExecutor(t, nil)

	start := time.Now()
	res := ex.Execute(context.Background(), job("python", "while True: pass"))
	elapsed := time.Since(start)

	if res.ExitCode != 137 {
		t.Fatalf("exit code = %d, want 137", res.ExitCode)
	}
	if res.Stderr != "Execution timed out" {
		t.Fatalf("stderr = %q, want %q", res.Stderr, "Execution timed out")
	}
	// python TimeLimit=5 in the registry... use the registry value + 2s slack.
	want := time.Duration(5+2)*time.Second + 5*time.Second // WaitDelay headroom
	if elapsed > want {
		t.Fatalf("took %v, want < %v (fallback kill not working)", elapsed, want)
	}
	if res.TimeMs < 7000 {
		t.Fatalf("time_ms = %d, want >= 7000 (5s limit + 2s slack)", res.TimeMs)
	}
}

func TestExecuteNsjailSideTimeoutIsNormalized(t *testing.T) {
	// Simulates nsjail's own time_limit firing: process killed with SIGKILL
	// (exit 137) right at the wall-clock budget — stderr should say so even
	// though the Go fallback never fired.
	t.Setenv("FAKE_SLEEP", "4") // past bash's 3s limit, under the 5s ctx fallback
	t.Setenv("FAKE_EXIT", "137")
	ex, _ := newTestExecutor(t, nil)

	res := ex.Execute(context.Background(), job("bash", "sleep infinity"))
	if res.ExitCode != 137 {
		t.Fatalf("exit code = %d, want 137", res.ExitCode)
	}
	if res.Stderr != "Execution timed out" {
		t.Fatalf("stderr = %q, want %q", res.Stderr, "Execution timed out")
	}
}

func TestExecuteTruncation(t *testing.T) {
	t.Setenv("FAKE_FLOOD", "10000")
	ex, _ := newTestExecutor(t, func(c *config.Config) {
		c.MaxOutputSize = 1024
	})

	res := ex.Execute(context.Background(), job("python", "print('x'*10000)"))
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", res.ExitCode)
	}
	if len(res.Stdout) != 1024 {
		t.Fatalf("stdout len = %d, want 1024", len(res.Stdout))
	}
	if !res.Truncated {
		t.Fatal("truncated = false, want true")
	}
}

func TestExecuteUnsupportedLanguage(t *testing.T) {
	ex, _ := newTestExecutor(t, nil)

	res := ex.Execute(context.Background(), job("cobol", "DISPLAY 'HI'."))
	if res.ExitCode != 255 {
		t.Fatalf("exit code = %d, want 255", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "unsupported language") {
		t.Fatalf("stderr = %q, want unsupported-language message", res.Stderr)
	}
}

func TestExecuteRejectsPathTraversalJobID(t *testing.T) {
	ex, tmp := newTestExecutor(t, nil)

	j := job("python", "print(1)")
	j.ID = "../../etc/evil"
	res := ex.Execute(context.Background(), j)
	if res.ExitCode != 255 {
		t.Fatalf("exit code = %d, want 255", res.ExitCode)
	}
	// Nothing may have been created outside the tmp base.
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("read tmp dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("tmp dir not empty after rejected job: %v", entries)
	}
}

func TestExecuteCleansWorkspace(t *testing.T) {
	ex, tmp := newTestExecutor(t, nil)

	res := ex.Execute(context.Background(), job("python", "print(1)"))
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", res.ExitCode)
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("read tmp dir: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("workspace not cleaned, leftovers: %v", names)
	}
}

func TestCompileFailureShortCircuits(t *testing.T) {
	t.Setenv("FAKE_COMPILE_EXIT", "1")
	t.Setenv("FAKE_COMPILE_STDERR", "main.c:1:1: error: expected declaration")
	t.Setenv("FAKE_STDOUT", "SHOULD-NOT-RUN")
	ex, _ := newTestExecutor(t, nil)

	res := ex.Execute(context.Background(), job("c", "int main( { return 0; }"))
	if res.ExitCode != 1 {
		t.Fatalf("exit code = %d, want 1", res.ExitCode)
	}
	if !strings.HasPrefix(res.Stderr, "Compilation error:\n") {
		t.Fatalf("stderr = %q, want Compilation error prefix", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "expected declaration") {
		t.Fatalf("stderr = %q, want compiler output included", res.Stderr)
	}
	if strings.Contains(res.Stdout, "SHOULD-NOT-RUN") {
		t.Fatal("run phase executed despite compile failure")
	}
}

func TestCompileSuccessThenRuns(t *testing.T) {
	t.Setenv("FAKE_COMPILE_EXIT", "0")
	t.Setenv("FAKE_STDOUT", "42\n")
	ex, _ := newTestExecutor(t, nil)

	res := ex.Execute(context.Background(), job("c", "int main(void){return 0;}"))
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr %q)", res.ExitCode, res.Stderr)
	}
	if res.Stdout != "42\n" {
		t.Fatalf("stdout = %q, want %q", res.Stdout, "42\n")
	}
}

func TestRenderConfigContents(t *testing.T) {
	ex, _ := newTestExecutor(t, nil)

	ws, err := NewWorkspace(t.TempDir(), "cfg-test")
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	defer ws.Cleanup()

	lang := DefaultLanguages()["python"]
	cfgPath, err := ex.renderConfig(ws, lang, runPhase(lang))
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read rendered config: %v", err)
	}
	cfg := string(data)

	for _, want := range []string{
		`path: "/usr/bin/python3"`,
		`arg: "/app/main.py"`,
		"time_limit: 5",
		"rlimit_as: 268435456",
		"rlimit_cpu: 5",
		"rlimit_nproc: 32",
		"rlimit_fsize: 5242880",
		`src: "` + ws.AppDir + `"`,
		`dst: "/app"`,
		"clone_newnet: false",
		`hostname: "minos-python-run"`,
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("rendered config missing %q\n--- config ---\n%s", want, cfg)
		}
	}
}

func TestRenderConfigCompilePhase(t *testing.T) {
	ex, _ := newTestExecutor(t, nil)

	ws, err := NewWorkspace(t.TempDir(), "cfg-test-compile")
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	defer ws.Cleanup()

	lang := DefaultLanguages()["c"]
	cfgPath, err := ex.renderConfig(ws, lang, compilePhase(lang))
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	data, _ := os.ReadFile(cfgPath)
	cfg := string(data)

	for _, want := range []string{
		`path: "/usr/bin/gcc"`,
		"time_limit: 10",
		"rlimit_as: 536870912",
		"rlimit_nproc: 64",
		"rlimit_fsize: 104857600",
		`envar: "TMPDIR=/app"`,
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("rendered compile config missing %q\n--- config ---\n%s", want, cfg)
		}
	}
}

// TestLanguageRegistry ensures every language is registered and renders a
// valid-looking run config with the expected source file and executable.
func TestLanguageRegistry(t *testing.T) {
	langs := DefaultLanguages()

	want := map[string]struct {
		srcFile string
		exec    string
	}{
		"python":     {"main.py", "/usr/bin/python3"},
		"bash":       {"main.sh", "/usr/bin/bash"},
		"javascript": {"main.js", "/usr/bin/node"},
		"go":         {"main.go", "/usr/bin/go"},
		"c":          {"main.c", "/app/main.out"},
		"cpp":        {"main.cpp", "/app/main.out"},
		"java":       {"Main.java", "/usr/bin/java"},
	}

	if len(langs) != len(want) {
		t.Fatalf("registry has %d languages, want %d: %v", len(langs), len(want), langs)
	}
	for name, exp := range want {
		lang, ok := langs[name]
		if !ok {
			t.Errorf("language %q not registered", name)
			continue
		}
		if lang.SourceFile != exp.srcFile {
			t.Errorf("%s SourceFile = %q, want %q", name, lang.SourceFile, exp.srcFile)
		}
		if lang.ExecPath != exp.exec {
			t.Errorf("%s ExecPath = %q, want %q", name, lang.ExecPath, exp.exec)
		}
		// Compiled languages must have a compile phase; interpreted ones must not.
		switch name {
		case "python", "bash", "javascript", "go":
			if lang.CompilePath != "" {
				t.Errorf("%s should be single-phase, has CompilePath=%q", name, lang.CompilePath)
			}
		default:
			if lang.CompilePath == "" {
				t.Errorf("%s should be two-phase, CompilePath empty", name)
			}
		}
	}
}

// TestRenderConfigRunEnv verifies per-language run env vars (e.g. Go's
// GOCACHE, Java's tmpdir) are emitted as envar lines in the run config.
func TestRenderConfigRunEnv(t *testing.T) {
	ex, _ := newTestExecutor(t, nil)

	ws, err := NewWorkspace(t.TempDir(), "cfg-test-env")
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	defer ws.Cleanup()

	lang := DefaultLanguages()["go"]
	cfgPath, err := ex.renderConfig(ws, lang, runPhase(lang))
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	data, _ := os.ReadFile(cfgPath)
	cfg := string(data)

	for _, want := range []string{
		`envar: "GOCACHE=/app/.cache"`,
		`envar: "GOTOOLCHAIN=local"`,
		`arg: "main.go"`,
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("rendered go run config missing %q\n--- config ---\n%s", want, cfg)
		}
	}
}
