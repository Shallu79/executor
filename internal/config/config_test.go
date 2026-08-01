package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadAPI_RetentionDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x:y@localhost/z")

	cfg, err := LoadAPI()
	if err != nil {
		t.Fatalf("LoadAPI: %v", err)
	}
	if cfg.SubmissionRetention != 30*24*time.Hour {
		t.Fatalf("SubmissionRetention = %v, want 720h (30 days)", cfg.SubmissionRetention)
	}
	if cfg.CleanupInterval != time.Hour {
		t.Fatalf("CleanupInterval = %v, want 1h", cfg.CleanupInterval)
	}
}

func TestLoadAPI_RetentionCustom(t *testing.T) {
	os.Setenv("DATABASE_URL", "postgres://x:y@localhost/z")
	defer os.Unsetenv("DATABASE_URL")

	t.Setenv("SUBMISSION_RETENTION_DAYS", "7")
	t.Setenv("CLEANUP_INTERVAL", "30m")

	cfg, err := LoadAPI()
	if err != nil {
		t.Fatalf("LoadAPI: %v", err)
	}
	if cfg.SubmissionRetention != 7*24*time.Hour {
		t.Fatalf("SubmissionRetention = %v, want 168h (7 days)", cfg.SubmissionRetention)
	}
	if cfg.CleanupInterval != 30*time.Minute {
		t.Fatalf("CleanupInterval = %v, want 30m", cfg.CleanupInterval)
	}
}
