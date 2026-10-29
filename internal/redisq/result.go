package redisq

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"executor/internal/models"

	"github.com/redis/go-redis/v9"
)

// Results pushes execution results to ephemeral per-job keys
// (executor:result:{job_id}) that the API gateway block-waits on.
type Results struct {
	rdb    *redis.Client
	prefix string
	ttl    time.Duration
}

// NewResults wraps rdb with the result key prefix and TTL
func NewResults(rdb *redis.Client, prefix string, ttl time.Duration) *Results {
	return &Results{rdb: rdb, prefix: prefix, ttl: ttl}
}

// Push serializes res and atomically LPUSHes it to the job's result key and
// sets the expiry. Uses a pipeline so both commands land in one round trip.
func (r *Results) Push(ctx context.Context, jobID string, res models.ExecutionResult) error {
	payload, err := json.Marshal(res)
	if err != nil {
		return err
	}
	key := r.prefix + jobID
	pipe := r.rdb.TxPipeline()
	pipe.LPush(ctx, key, payload)
	pipe.Expire(ctx, key, r.ttl)
	_, err = pipe.Exec(ctx)
	return err
}

// Get returns the result for a job, if it has been pushed. found is false
// while the job is still queued/processing (or once its key has expired).
func (r *Results) Get(ctx context.Context, jobID string) (res models.ExecutionResult, found bool, err error) {
	vals, err := r.rdb.LRange(ctx, r.prefix+jobID, 0, 0).Result()
	if err != nil {
		return res, false, err
	}
	if len(vals) == 0 {
		return res, false, nil
	}
	if err := json.Unmarshal([]byte(vals[0]), &res); err != nil {
		return res, false, err
	}
	return res, true, nil
}

// Wait blocks up to timeout for the job's result using BLPOP — the API
// gateway's synchronous path (ARCHITECTURE.md section 3, "no polling in the
// hot path"). Returns (result, true, nil) when the worker pushes, or
// (zero, false, nil) when the wait window expires.
func (r *Results) Wait(ctx context.Context, jobID string, timeout time.Duration) (models.ExecutionResult, bool, error) {
	vals, err := r.rdb.BLPop(ctx, timeout, r.prefix+jobID).Result()
	if errors.Is(err, redis.Nil) {
		return models.ExecutionResult{}, false, nil
	}
	if err != nil {
		return models.ExecutionResult{}, false, err
	}
	if len(vals) < 2 {
		return models.ExecutionResult{}, false, nil
	}
	var res models.ExecutionResult
	if err := json.Unmarshal([]byte(vals[1]), &res); err != nil {
		return models.ExecutionResult{}, false, err
	}
	return res, true, nil
}
