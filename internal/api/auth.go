package api

import (
	"crypto/subtle"
	"strings"

	"github.com/gin-gonic/gin"
)

// requireAPIKey authenticates every /api/v1 request. The key is read from
// the X-API-Key header (or Authorization: Bearer <key>) and compared in
// constant time. On success the key is stored in the context as "api_key"
// so handlers can record who submitted a job.
func (s *Server) requireAPIKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader(APIKeyHeader)
		if key == "" {
			key = strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		}

		if key == "" || subtle.ConstantTimeCompare([]byte(key), []byte(s.cfg.APIKey)) != 1 {
			c.AbortWithStatusJSON(401, ErrorResponse{Error: "invalid or missing API key"})
			return
		}

		c.Set("api_key", key)
		c.Next()
	}
}
