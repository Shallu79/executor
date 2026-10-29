// Executor worker daemon: pulls jobs from Redis, executes them in nsjail,
// pushes results back. No HTTP — just BLPOP → nsjail → LPUSH.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"executor/internal/config"
	"executor/internal/redisq"
	"executor/internal/worker"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	// SIGTERM/SIGINT → graceful shutdown (drain in-flight jobs).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rdb := redisq.NewClient(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	defer rdb.Close()

	if err := redisq.PingWithRetry(ctx, rdb, 10, time.Second); err != nil {
		logger.Error("redis unreachable", "addr", cfg.RedisAddr, "error", err)
		os.Exit(1)
	}
	logger.Info("connected to redis", "addr", cfg.RedisAddr)

	w, err := worker.New(cfg, rdb, logger)
	if err != nil {
		logger.Error("worker setup failed", "error", err)
		os.Exit(1)
	}

	logger.Info("worker starting",
		"concurrency", cfg.Concurrency,
		"tmp_dir", cfg.TmpDir,
		"queue", cfg.QueueKey,
		"hostname", cfg.Hostname,
		"nsjail_bin", cfg.NsjailBin,
		"clone_newnet", cfg.CloneNewNet,
		"clone_newpid", cfg.CloneNewPID,
		"clone_newcgroup", cfg.CloneNewCgroup,
	)

	if err := w.Run(ctx); err != nil {
		logger.Error("worker stopped with error", "error", err)
		os.Exit(1)
	}
	logger.Info("worker stopped cleanly")
}
