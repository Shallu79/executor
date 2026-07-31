// Package redisq wraps the Redis client with the queue/result operations
package redisq

import (
	"context"
	"fmt"
	"time"

	"minos/internal/config"

	"github.com/redis/go-redis/v9"
)

func NewClient(cfg config.Config) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
}

// PingWithRetry verifies Redis connectivity, retrying with a fixed delay.
// Used at startup so a worker doesn't crash-loop while Redis boots.
func PingWithRetry(ctx context.Context, rdb *redis.Client, attempts int, delay time.Duration) error {
	var err error
	for i := 0; i < attempts; i++ {
		if err = rdb.Ping(ctx).Err(); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return fmt.Errorf("redis ping failed after %d attempts: %w", attempts, err)
}
