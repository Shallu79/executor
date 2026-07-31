package redisq

import (
	"context"
	"encoding/json"
	"time"

	"minos/internal/models"

	"github.com/redis/go-redis/v9"
)

// Results pushes execution results to ephemeral per-job keys
// (minos:result:{job_id}) that the API gateway block-waits on.
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
