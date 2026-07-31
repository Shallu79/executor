package worker

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

// jobIDRe restricts job IDs to a safe charset. The API generates UUIDs, but
// the worker never trusts the queue payload — this blocks path traversal
// via crafted IDs ("../../etc").
var jobIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// Workspace is a per-job directory: {TmpDir}/{jobID}/. Only its app/
// subdirectory is bind-mounted into the jail (as /app); rendered nsjail
// configs and logs live next to it and stay invisible to user code.
//
//	{TmpDir}/{jobID}/
//	├── app/            → bind-mounted to /app (only writable jail dir)
//	│   ├── main.py
//	│   └── stdin.txt
//	├── nsjail-run.cfg  → rendered config (not visible inside jail)
//	└── nsjail-run.log  → nsjail's own log output
type Workspace struct {
	Dir    string
	AppDir string
}

// NewWorkspace creates the isolated per-job directory (0700).
func NewWorkspace(base, jobID string) (*Workspace, error) {
	if !jobIDRe.MatchString(jobID) {
		return nil, fmt.Errorf("invalid job id %q", jobID)
	}
	dir := filepath.Join(base, jobID)
	app := filepath.Join(dir, "app")
	if err := os.MkdirAll(app, 0o700); err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}
	return &Workspace{Dir: dir, AppDir: app}, nil
}

// WriteAppFile writes a file into the jailed /app directory. name must be a
// bare file name (no directory components).
func (w *Workspace) WriteAppFile(name string, data []byte, perm fs.FileMode) error {
	if name == "" || name != filepath.Base(name) {
		return fmt.Errorf("invalid app file name %q", name)
	}
	return os.WriteFile(filepath.Join(w.AppDir, name), data, perm)
}

// WriteFile writes a file into the workspace root (outside the jail mount).
func (w *Workspace) WriteFile(name string, data []byte, perm fs.FileMode) (string, error) {
	if name == "" || name != filepath.Base(name) {
		return "", fmt.Errorf("invalid file name %q", name)
	}
	path := filepath.Join(w.Dir, name)
	if err := os.WriteFile(path, data, perm); err != nil {
		return "", err
	}
	return path, nil
}

// OpenAppFile opens a file inside app/ for reading, e.g. stdin.txt.
func (w *Workspace) OpenAppFile(name string) (*os.File, error) {
	if name == "" || name != filepath.Base(name) {
		return nil, fmt.Errorf("invalid app file name %q", name)
	}
	return os.Open(filepath.Join(w.AppDir, name))
}

// LogPath returns where nsjail's own logs for a phase should go.
func (w *Workspace) LogPath(phase string) string {
	return filepath.Join(w.Dir, "nsjail-"+phase+".log")
}

// Cleanup removes the whole workspace. os.RemoveAll does not follow
// symlinks, so symlink-escape attempts inside app/ can't drag host files
func (w *Workspace) Cleanup() error {
	return os.RemoveAll(w.Dir)
}
