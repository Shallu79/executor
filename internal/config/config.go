// Package config loads worker configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all worker settings. Every field maps to an environment
// variable so the same binary runs bare-metal (systemd) or in Docker.
type Config struct {
	// Redis connection
	RedisAddr     string // REDIS_ADDR
	RedisPassword string // REDIS_PASSWORD
	RedisDB       int    // REDIS_DB

	// Queue/result keys
	QueueKey     string        // MINOS_QUEUE          — list workers BLPOP from
	ResultPrefix string        // MINOS_RESULT_PREFIX  — result key prefix
	ResultTTL    time.Duration // RESULT_TTL           — expiry for result keys

	// Execution
	Concurrency   int    // CONCURRENCY     — goroutines pulling jobs
	TmpDir        string // TMP_DIR         — per-job workspace base dir
	MaxOutputSize int    // MAX_OUTPUT_SIZE — stdout/stderr cap in bytes

	// nsjail
	NsjailBin      string // NSJAIL_BIN           — path to nsjail binary
	CloneNewNet    bool   // NSJAIL_CLONE_NEWNET  — empty net ns (no network in jail)
	CloneNewPID    bool   // NSJAIL_CLONE_NEWPID  — private PID ns
	CloneNewCgroup bool   // NSJAIL_CLONE_NEWCGROUP

	// Reliability
	BLPOPTimeout    time.Duration // BLPOP_TIMEOUT
	MaxConsecErrors int           // MAX_CONSEC_REDIS_ERRORS — worker exits past this
	StaleAge        time.Duration // STALE_WORKSPACE_AGE — startup cleanup threshold

	// Identity / logging
	Hostname string // HOSTNAME          — used in heartbeat key
	LogLevel string // LOG_LEVEL         — debug|info|warn|error
}

// Load reads configuration from the environment, applying defaults that
// match ARCHITECTURE.md.
func Load() (Config, error) {
	host, _ := os.Hostname()

	c := Config{
		RedisAddr:     envStr("REDIS_ADDR", "localhost:6379"),
		RedisPassword: envStr("REDIS_PASSWORD", ""),
		RedisDB:       envInt("REDIS_DB", 0),

		QueueKey:     envStr("MINOS_QUEUE", "minos:queue"),
		ResultPrefix: envStr("MINOS_RESULT_PREFIX", "minos:result:"),
		ResultTTL:    envDur("RESULT_TTL", 5*time.Minute),

		Concurrency:   envInt("CONCURRENCY", 4),
		TmpDir:        envStr("TMP_DIR", "/tmp/minos"),
		MaxOutputSize: envInt("MAX_OUTPUT_SIZE", 65536),

		NsjailBin:      envStr("NSJAIL_BIN", "nsjail"),
		CloneNewNet:    envBool("NSJAIL_CLONE_NEWNET", false),
		CloneNewPID:    envBool("NSJAIL_CLONE_NEWPID", false),
		CloneNewCgroup: envBool("NSJAIL_CLONE_NEWCGROUP", false),

		BLPOPTimeout:    envDur("BLPOP_TIMEOUT", 5*time.Second),
		MaxConsecErrors: envInt("MAX_CONSEC_REDIS_ERRORS", 10),
		StaleAge:        envDur("STALE_WORKSPACE_AGE", time.Hour),

		Hostname: envStr("HOSTNAME", host),
		LogLevel: envStr("LOG_LEVEL", "info"),
	}

	if c.Concurrency < 1 {
		return c, fmt.Errorf("CONCURRENCY must be >= 1, got %d", c.Concurrency)
	}
	if c.MaxOutputSize < 1024 {
		return c, fmt.Errorf("MAX_OUTPUT_SIZE must be >= 1024, got %d", c.MaxOutputSize)
	}
	if c.TmpDir == "" {
		return c, fmt.Errorf("TMP_DIR must not be empty")
	}
	if c.Hostname == "" {
		c.Hostname = "unknown"
	}
	return c, nil
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// envDur accepts Go duration strings ("5s", "1m") or bare seconds ("30").
func envDur(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(n) * time.Second
	}
	return def
}
