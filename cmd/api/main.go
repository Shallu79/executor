package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"executor/internal/api"
	"executor/internal/config"
	"executor/internal/redisq"
	"executor/internal/store"
)

func main() {
	cfg, err := config.LoadAPI()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	if cfg.JWTSecret == "" {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		cfg.JWTSecret = hex.EncodeToString(b)
		slog.Warn("JWT_SECRET not set — using an ephemeral secret; sessions will not survive restarts")
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

	st, err := store.NewPostgres(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("postgres unreachable", "error", err)
		os.Exit(1)
	}
	defer st.Close()
	if err := store.Migration(ctx, st); err != nil {
		logger.Error("migration failed", "error", err)
		os.Exit(1)
	}
	logger.Info("connected to postgres, schema ready")

	// Background job-history retention: delete submissions older than
	// SUBMISSION_RETENTION_DAYS so postgres doesn't flood.
	go runRetentionLoop(ctx, logger, st, cfg.SubmissionRetention, cfg.CleanupInterval)

	srv := api.NewServer(cfg, rdb, st, logger)
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

// runRetentionLoop periodically deletes submissions older than the retention
// window. Runs once at startup, then every interval until ctx is done.
func runRetentionLoop(
	ctx context.Context,
	logger *slog.Logger,
	st store.Store,
	retention, interval time.Duration,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		cutoff := time.Now().Add(-retention)
		n, err := st.DeleteOldSubmissions(ctx, cutoff)
		if err != nil {
			logger.Error("submission retention cleanup failed", "error", err)
		} else if n > 0 {
			logger.Info("retention cleanup", "deleted", n, "retention_days", int(retention.Hours()/24))
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
