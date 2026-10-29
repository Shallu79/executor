// Package worker implements the Executor worker daemon: it pulls jobs from the
// Redis queue, executes them in nsjail sandboxes, and pushes results back
package worker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"executor/internal/config"
	"executor/internal/redisq"

	"github.com/redis/go-redis/v9"
)

// Worker is the daemon: N goroutines each running BLPOP → execute → LPUSH.
type Worker struct {
	cfg     config.Config
	rdb     *redis.Client
	queue   *redisq.Queue
	results *redisq.Results
	exec    *Executor
	log     *slog.Logger
}

// New wires up the worker. Fails fast if nsjail or the workspace dir is
// unusable better to crash at boot than on the first job.
func New(cfg config.Config, rdb *redis.Client, log *slog.Logger) (*Worker, error) {
	exec, err := NewExecutor(cfg, log)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.TmpDir, 0o700); err != nil {
		return nil, fmt.Errorf("create tmp dir %s: %w", cfg.TmpDir, err)
	}
	return &Worker{
		cfg:     cfg,
		rdb:     rdb,
		queue:   redisq.NewQueue(rdb, cfg.QueueKey),
		results: redisq.NewResults(rdb, cfg.ResultPrefix, cfg.ResultTTL),
		exec:    exec,
		log:     log,
	}, nil
}

// Run starts the heartbeat and the worker pool, then blocks until ctx is
// cancelled (graceful shutdown) or a fatal error occurs (e.g. Redis
// unreachable for MAX_CONSEC_REDIS_ERRORS in a row the supervisor should
// restart the worker).
func (w *Worker) Run(ctx context.Context) error {
	w.cleanupStaleWorkspaces()

	hbCtx, hbCancel := context.WithCancel(context.Background())
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		w.heartbeat(hbCtx)
	}()
	// Stop the heartbeat (including its key cleanup) before returning.
	defer func() {
		hbCancel()
		<-hbDone
	}()

	errCh := make(chan error, w.cfg.Concurrency)
	var wg sync.WaitGroup
	for i := 0; i < w.cfg.Concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if err := w.loop(ctx, id); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

// loop is one worker goroutine: dequeue → execute → push result, forever.
func (w *Worker) loop(ctx context.Context, id int) error {
	log := w.log.With(slog.Int("goroutine", id))
	consecErrs := 0

	for {
		if ctx.Err() != nil {
			return nil
		}

		job, err := w.queue.Pop(ctx, w.cfg.BLPOPTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return nil // shutting down
			}
			consecErrs++
			log.Error("queue pop failed", "error", err, "consecutive_errors", consecErrs)
			if consecErrs >= w.cfg.MaxConsecErrors {
				return fmt.Errorf("redis unreachable after %d consecutive errors: %w", consecErrs, err)
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Second):
			}
			continue
		}
		if job == nil {
			continue // BLPOP timeout check ctx, block again
		}
		consecErrs = 0

		jlog := log.With(slog.String("job_id", job.ID), slog.String("language", job.Language))
		jlog.Info("job received", slog.Int("code_size", len(job.Code)))

		res := w.exec.Execute(ctx, *job)

		jlog.Info("job completed",
			slog.Int("exit_code", res.ExitCode),
			slog.Int64("time_ms", res.TimeMs),
			slog.Int("stdout_size", len(res.Stdout)),
			slog.Int("stderr_size", len(res.Stderr)),
			slog.Bool("truncated", res.Truncated),
		)

		// Push with a fresh short-lived context: during shutdown the parent
		// ctx is already cancelled, but the API is still waiting on this key
		// and would otherwise 504.
		pctx, pcancel := context.WithTimeout(context.Background(), 3*time.Second)
		err = w.results.Push(pctx, job.ID, res)
		pcancel()
		if err != nil {
			jlog.Error("result push failed", "error", err)
		}
	}
}

// heartbeat maintains executor:worker:{hostname}:heartbeat (TTL 30s, renewed
// every 10s) so monitoring can detect dead workers.
func (w *Worker) heartbeat(ctx context.Context) {
	key := "executor:worker:" + w.cfg.Hostname + ":heartbeat"
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()

	for {
		if err := w.rdb.Set(ctx, key, "alive", 30*time.Second).Err(); err != nil && ctx.Err() == nil {
			w.log.Warn("heartbeat failed", "error", err)
		}
		select {
		case <-ctx.Done():
			// Best-effort cleanup so the worker doesn't look alive for 30s
			// after a clean shutdown.
			delCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			w.rdb.Del(delCtx, key)
			cancel()
			return
		case <-tick.C:
		}
	}
}

// cleanupStaleWorkspaces removes workspaces left behind by a previous crash.
func (w *Worker) cleanupStaleWorkspaces() {
	entries, err := os.ReadDir(w.cfg.TmpDir)
	if err != nil {
		if !os.IsNotExist(err) {
			w.log.Warn("cannot scan tmp dir", "dir", w.cfg.TmpDir, "error", err)
		}
		return
	}
	cutoff := time.Now().Add(-w.cfg.StaleAge)
	removed := 0
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.RemoveAll(filepath.Join(w.cfg.TmpDir, entry.Name())); err == nil {
				removed++
			}
		}
	}
	if removed > 0 {
		w.log.Info("removed stale workspaces", slog.Int("count", removed))
	}
}
