// Minos API gateway: validates submissions, queues jobs in Redis, and serves
// results back to clients. Identity is a shared API key for now; per-user
// auth endpoints come later.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"minos/internal/api"
	"minos/internal/config"
	"minos/internal/redisq"
)

func main() {
	cfg, err := config.LoadAPI()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	// SIGTERM/SIGINT → graceful shutdown (drain in-flight requests).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rdb := redisq.NewClient(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	defer rdb.Close()

	if err := redisq.PingWithRetry(ctx, rdb, 10, time.Second); err != nil {
		logger.Error("redis unreachable", "addr", cfg.RedisAddr, "error", err)
		os.Exit(1)
	}
	logger.Info("connected to redis", "addr", cfg.RedisAddr)

	srv := api.NewServer(cfg, rdb, logger)
	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("api listening", "addr", cfg.ListenAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		logger.Error("api server failed", "error", err)
		os.Exit(1)
	case <-ctx.Done():
		logger.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("api stopped cleanly")
}
