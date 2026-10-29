// Package api implements the Executor API gateway (ARCHITECTURE.md section 5.1):
// a small HTTP service that validates submissions, pushes jobs to Redis, and
// serves results back to clients. It also owns the account surface: signup,
// signin, API-key management, and job history backed by postgres.
package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"executor/internal/config"
	"executor/internal/languages"
	"executor/internal/redisq"
	"executor/internal/store"
)

// APIKeyHeader is the request header carrying the identity token.
const APIKeyHeader = "X-API-Key"

// Server wires the API gateway together.
type Server struct {
	cfg     config.APIConfig
	rdb     *redis.Client
	store   store.Store
	queue   *redisq.Queue
	results *redisq.Results
	log     *slog.Logger
	langs   map[string]languages.Language
}

// NewServer builds a Server bound to the shared Redis client and store.
func NewServer(cfg config.APIConfig, rdb *redis.Client, st store.Store, log *slog.Logger) *Server {
	return &Server{
		cfg:     cfg,
		rdb:     rdb,
		store:   st,
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

	// Public auth endpoints.
	auth := r.Group("/api/v1/auth")
	auth.POST("/signup", s.signup)
	auth.POST("/signin", s.signin)

	// Account endpoints (session token required).
	account := r.Group("/api/v1", s.requireSession())
	account.GET("/me", s.me)
	account.GET("/api-keys", s.listAPIKeys)
	account.POST("/api-keys", s.createAPIKey)
	account.PATCH("/api-keys/:id", s.updateAPIKey)
	account.DELETE("/api-keys/:id", s.deleteAPIKey)
	account.GET("/submissions", s.listSubmissions)
	account.GET("/submissions/:id", s.getSubmission)

	// Job execution (API key required).
	v1 := r.Group("/api/v1", s.requireAPIKey())
	v1.POST("/execute", s.execute)
	v1.GET("/jobs/:id", s.getJob)

	return r
}

// cors permits browser-based clients (the dashboard) to call the API from
// any origin. Requests are still authenticated via the API key / bearer.
func cors() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
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
