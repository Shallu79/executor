package worker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceLifecycle(t *testing.T) {
	base := t.TempDir()

	ws, err := NewWorkspace(base, "abc-123_DEF")
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	if ws.Dir != filepath.Join(base, "abc-123_DEF") {
		t.Fatalf("Dir = %q", ws.Dir)
	}
	if ws.AppDir != filepath.Join(ws.Dir, "app") {
		t.Fatalf("AppDir = %q", ws.AppDir)
	}
	if st, err := os.Stat(ws.AppDir); err != nil || !st.IsDir() {
		t.Fatalf("app dir missing: %v", err)
	}

	if err := ws.WriteAppFile("main.py", []byte("print(1)"), 0o400); err != nil {
		t.Fatalf("WriteAppFile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws.AppDir, "main.py")); err != nil {
		t.Fatalf("source file missing: %v", err)
	}

	if _, err := ws.WriteFile("nsjail-run.cfg", []byte("mode: ONCE"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := ws.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(ws.Dir); !os.IsNotExist(err) {
		t.Fatalf("workspace still exists after cleanup")
	}
}

func TestWorkspaceRejectsBadJobIDs(t *testing.T) {
	base := t.TempDir()
	for _, id := range []string{"", "../x", "a/b", "a b", "-lead", "x" + string(rune(0)) + "y", string(make([]byte, 100))} {
		if _, err := NewWorkspace(base, id); err == nil {
			t.Errorf("NewWorkspace(%q) succeeded, want error", id)
		}
	}
}

func TestWorkspaceRejectsNestedFileNames(t *testing.T) {
	ws, err := NewWorkspace(t.TempDir(), "nested-test")
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	defer ws.Cleanup()

	for _, name := range []string{"../escape", "sub/dir", "/abs"} {
		if err := ws.WriteAppFile(name, []byte("x"), 0o400); err == nil {
			t.Errorf("WriteAppFile(%q) succeeded, want error", name)
		}
	}
}

func TestBoundedWriter(t *testing.T) {
	w := newBoundedWriter(10)

	n, err := w.Write([]byte("0123456789"))
	if n != 10 || err != nil {
		t.Fatalf("Write returned %d, %v", n, err)
	}
	if w.Truncated() {
		t.Fatal("truncated after exact-fit write")
	}

	n, err = w.Write([]byte("abcdef"))
	if n != 6 || err != nil {
		t.Fatalf("Write returned %d, %v", n, err)
	}
	if !w.Truncated() {
		t.Fatal("not truncated after overflow write")
	}
	if got := w.String(); got != "0123456789" {
		t.Fatalf("String() = %q", got)
	}

	// Writer must keep accepting data (reporting it consumed) so the child
	// never blocks on a full pipe.
	n, err = w.Write([]byte("more data"))
	if n != 9 || err != nil {
		t.Fatalf("Write returned %d, %v", n, err)
	}
	if got := len(w.String()); got != 10 {
		t.Fatalf("len = %d, want 10", got)
	}
}
