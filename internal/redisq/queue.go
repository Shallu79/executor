package redisq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"minos/internal/models"

	"github.com/redis/go-redis/v9"
)

// Queue is the FIFO job queue (minos:queue) workers consume from.
type Queue struct {
	rdb *redis.Client
	key string
}

// NewQueue wraps rdb for the given list key.
func NewQueue(rdb *redis.Client, key string) *Queue {
	return &Queue{rdb: rdb, key: key}
}

// Pop block-waits up to timeout for the next job.
// Returns (nil, nil) when the timeout expires with no job - callers loop.
func (q *Queue) Pop(ctx context.Context, timeout time.Duration) (*models.Job, error) {
	res, err := q.rdb.BLPop(ctx, timeout, q.key).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(res) < 2 {
		return nil, nil
	}
	var job models.Job
	if err := json.Unmarshal([]byte(res[1]), &job); err != nil {
		// Malformed payload: no usable job ID to report against, so the
		// caller can only log and drop it.
		return nil, fmt.Errorf("decode job payload: %w", err)
	}
	return &job, nil
}
