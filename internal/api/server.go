// Package api implements the Minos API gateway (ARCHITECTURE.md section 5.1):
// a small HTTP service that validates submissions, pushes jobs to Redis, and
// serves results back to clients. Identity today is a single shared API key;
// per-user auth will be layered on later without changing this surface.
package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"minos/internal/config"
	"minos/internal/languages"
	"minos/internal/redisq"
)

// APIKeyHeader is the request header carrying the identity token.
const APIKeyHeader = "X-API-Key"

// Server wires the API gateway together.
type Server struct {
	cfg     config.APIConfig
	rdb     *redis.Client
	queue   *redisq.Queue
	results *redisq.Results
	log     *slog.Logger
	langs   map[string]languages.Language
}

// NewServer builds a Server bound to the shared Redis client.
func NewServer(cfg config.APIConfig, rdb *redis.Client, log *slog.Logger) *Server {
	return &Server{
		cfg:     cfg,
		rdb:     rdb,
		queue:   redisq.NewQueue(rdb, cfg.QueueKey),
		results: redisq.NewResults(rdb, cfg.ResultPrefix, cfg.ResultTTL),
		log:     log,
		langs:   languages.DefaultLanguages(),
	}
}

// Router assembles the gin engine with all routes and middleware.
func (s *Server) Router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), s.requestLogger(), cors())

	// Health endpoints are unauthenticated (used by load balancers).
	r.GET("/healthz", s.healthz)
	r.GET("/readyz", s.readyz)

	v1 := r.Group("/api/v1", s.requireAPIKey())
	v1.POST("/execute", s.execute)
	v1.GET("/jobs/:id", s.getJob)

	return r
}

// cors permits browser-based clients (the demo runner page) to call the API
// from any origin. Requests are still authenticated via the API key header.
func cors() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// requestLogger emits one structured log line per request.
func (s *Server) requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		s.log.Info("http request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"client_ip", c.ClientIP(),
		)
	}
}

func (s *Server) healthz(c *gin.Context) {
	c.JSON(200, gin.H{"status": "ok"})
}

func (s *Server) readyz(c *gin.Context) {
	if err := s.rdb.Ping(c.Request.Context()).Err(); err != nil {
		s.log.Error("readiness redis ping failed", "error", err)
		c.JSON(503, gin.H{"status": "not_ready"})
		return
	}
	c.JSON(200, gin.H{"status": "ready"})
}
