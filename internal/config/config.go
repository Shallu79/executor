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

type APIConfig struct {
	ListenAddr    string        // LISTEN_ADDR — where the HTTP server binds
	RedisAddr     string        // REDIS_ADDR
	RedisPassword string        // REDIS_PASSWORD
	RedisDB       int           // REDIS_DB
	QueueKey      string        // MINOS_QUEUE
	ResultPrefix  string        // MINOS_RESULT_PREFIX
	ResultTTL     time.Duration // RESULT_TTL
	MaxCodeSize   int           // MAX_CODE_SIZE — bytes
	MaxStdinSize  int           // MAX_STDIN_SIZE — bytes

	// Accounts / persistence
	DatabaseURL string // DATABASE_URL — postgres DSN
	JWTSecret   string // JWT_SECRET — signs session tokens

	// Submission retention. Rows older than SubmissionRetention are deleted
	// on a CleanupInterval tick so postgres doesn't flood with history.
	SubmissionRetention time.Duration // SUBMISSION_RETENTION_DAYS
	CleanupInterval     time.Duration // CLEANUP_INTERVAL

	// ExecutionTimeout is how long POST /execute blocks waiting for the
	// worker's result before returning 504. Must exceed the worst-case
	// worker runtime (compile + run + 2s slack per phase).
	ExecutionTimeout time.Duration // EXECUTION_TIMEOUT
}

// LoadAPI reads API configuration from the environment.
func LoadAPI() (APIConfig, error) {
	c := APIConfig{
		ListenAddr:          envStr("LISTEN_ADDR", ":8080"),
		RedisAddr:           envStr("REDIS_ADDR", "localhost:6379"),
		RedisPassword:       envStr("REDIS_PASSWORD", ""),
		RedisDB:             envInt("REDIS_DB", 0),
		QueueKey:            envStr("MINOS_QUEUE", "minos:queue"),
		ResultPrefix:        envStr("MINOS_RESULT_PREFIX", "minos:result:"),
		ResultTTL:           envDur("RESULT_TTL", 5*time.Minute),
		MaxCodeSize:         envInt("MAX_CODE_SIZE", 65536),
		MaxStdinSize:        envInt("MAX_STDIN_SIZE", 1024),
		DatabaseURL:         envStr("DATABASE_URL", ""),
		JWTSecret:           envStr("JWT_SECRET", ""),
		SubmissionRetention: time.Duration(envInt("SUBMISSION_RETENTION_DAYS", 30)) * 24 * time.Hour,
		CleanupInterval:     envDur("CLEANUP_INTERVAL", time.Hour),
		ExecutionTimeout:    envDur("EXECUTION_TIMEOUT", 30*time.Second),
	}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("DATABASE_URL is required (postgres DSN)")
	}
	if c.ListenAddr == "" {
		c.ListenAddr = ":8080"
	}
	return c, nil
}
