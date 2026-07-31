// Package redisq wraps the Redis client with the queue/result operations
// shared by the API gateway and the worker, per ARCHITECTURE.md section 5.2.
package redisq

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// NewClient builds a Redis client. Shared by both the API gateway and the
// worker so they always talk to the same broker with the same options.
func NewClient(addr, password string, db int) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
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
