package worker

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"minos/internal/config"
	"minos/internal/models"
	"minos/internal/redisq"

	"github.com/alicebob/miniredis/v2"
)

// TestWorkerEndToEnd runs the real Worker against an in-memory Redis and
// the fake nsjail: job in → result out, with TTL and heartbeat.
func TestWorkerEndToEnd(t *testing.T) {
	t.Setenv("FAKE_STDOUT", "42\n")

	mr := miniredis.RunT(t)

	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "nsjail")
	if err := os.WriteFile(binPath, []byte(fakeNsjail), 0o755); err != nil {
		t.Fatalf("write fake nsjail: %v", err)
	}

	cfg := config.Config{
		RedisAddr:       mr.Addr(),
		QueueKey:        "minos:queue",
		ResultPrefix:    "minos:result:",
		ResultTTL:       5 * time.Minute,
		Concurrency:     2,
		TmpDir:          t.TempDir(),
		MaxOutputSize:   65536,
		NsjailBin:       binPath,
		BLPOPTimeout:    time.Second,
		MaxConsecErrors: 3,
		StaleAge:        time.Hour,
		Hostname:        "test-host",
	}

	rdb := redisq.NewClient(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	defer rdb.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w, err := New(cfg, rdb, log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	// Push a job the way the API gateway would.
	payload, _ := json.Marshal(models.Job{
		ID:       "e2e-job-1",
		Language: "python",
		Code:     "print(42)",
		Created:  time.Now(),
	})
	if err := rdb.RPush(ctx, cfg.QueueKey, payload).Err(); err != nil {
		t.Fatalf("rpush: %v", err)
	}

	// Wait for the result key to appear, and verify the TTL is set BEFORE
	// popping — popping the only element empties the list and Redis deletes
	// the key, taking the TTL with it.
	deadline := time.Now().Add(5 * time.Second)
	for !mr.Exists("minos:result:e2e-job-1") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !mr.Exists("minos:result:e2e-job-1") {
		t.Fatal("result key never appeared")
	}
	if ttl := mr.TTL("minos:result:e2e-job-1"); ttl <= 0 || ttl > 5*time.Minute {
		t.Fatalf("result TTL = %v, want (0, 5m]", ttl)
	}

	res, err := rdb.BLPop(ctx, 5*time.Second, "minos:result:e2e-job-1").Result()
	if err != nil {
		t.Fatalf("waiting for result: %v", err)
	}
	if len(res) < 2 {
		t.Fatalf("empty result payload: %v", res)
	}
	var got models.ExecutionResult
	if err := json.Unmarshal([]byte(res[1]), &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if got.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr %q)", got.ExitCode, got.Stderr)
	}
	if got.Stdout != "42\n" {
		t.Fatalf("stdout = %q, want %q", got.Stdout, "42\n")
	}

	// Heartbeat key should appear within a moment.
	deadline = time.Now().Add(2 * time.Second)
	for !mr.Exists("minos:worker:test-host:heartbeat") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !mr.Exists("minos:worker:test-host:heartbeat") {
		t.Fatal("heartbeat key never appeared")
	}

	// Graceful shutdown: Run must return, and the heartbeat must be removed.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down within 5s")
	}
	if mr.Exists("minos:worker:test-host:heartbeat") {
		t.Fatal("heartbeat key not removed on shutdown")
	}
}

// TestWorkerDropsMalformedJobs verifies a garbage payload doesn't wedge the
// loop — the worker logs, drops it, and processes the next job.
func TestWorkerDropsMalformedJobs(t *testing.T) {
	t.Setenv("FAKE_STDOUT", "ok\n")

	mr := miniredis.RunT(t)

	binPath := filepath.Join(t.TempDir(), "nsjail")
	if err := os.WriteFile(binPath, []byte(fakeNsjail), 0o755); err != nil {
		t.Fatalf("write fake nsjail: %v", err)
	}

	cfg := config.Config{
		RedisAddr:       mr.Addr(),
		QueueKey:        "minos:queue",
		ResultPrefix:    "minos:result:",
		ResultTTL:       time.Minute,
		Concurrency:     1,
		TmpDir:          t.TempDir(),
		MaxOutputSize:   65536,
		NsjailBin:       binPath,
		BLPOPTimeout:    time.Second,
		MaxConsecErrors: 3,
		Hostname:        "test-host",
	}

	rdb := redisq.NewClient(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	defer rdb.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w, err := New(cfg, rdb, log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	rdb.RPush(ctx, cfg.QueueKey, "{not json at all")
	good, _ := json.Marshal(models.Job{ID: "good-1", Language: "python", Code: "print('ok')"})
	rdb.RPush(ctx, cfg.QueueKey, good)

	res, err := rdb.BLPop(ctx, 5*time.Second, "minos:result:good-1").Result()
	if err != nil {
		t.Fatalf("waiting for result: %v", err)
	}
	var got models.ExecutionResult
	if err := json.Unmarshal([]byte(res[1]), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Stdout != "ok\n" {
		t.Fatalf("stdout = %q, want %q", got.Stdout, "ok\n")
	}

	cancel()
	<-done
}
