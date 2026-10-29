package redisq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"executor/internal/models"

	"github.com/redis/go-redis/v9"
)

// Queue is the FIFO job queue (executor:queue) workers consume from.
type Queue struct {
	rdb *redis.Client
	key string
}

// NewQueue wraps rdb for the given list key.
func NewQueue(rdb *redis.Client, key string) *Queue {
	return &Queue{rdb: rdb, key: key}
}

// Push appends a job to the queue (RPUSH). Used by the API gateway.
func (q *Queue) Push(ctx context.Context, job models.Job) error {
	payload, err := json.Marshal(job)
	if err != nil {
		return err
	}
	return q.rdb.RPush(ctx, q.key, payload).Err()
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
