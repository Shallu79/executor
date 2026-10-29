package api

import (
	"errors"
	"strings"

	"github.com/gin-gonic/gin"

	"executor/internal/auth"
	"executor/internal/store"
)

// bearer extracts the token from the Authorization header, if present.
func bearer(c *gin.Context) string {
	return strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
}

// requireAPIKey authenticates /api/v1/execute and /api/v1/jobs/*. The key is
// read from X-API-Key (or Authorization: Bearer), resolved against the store
// (hashed lookup), and its owner is stored in the context as "user_id".
func (s *Server) requireAPIKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader(APIKeyHeader)
		if key == "" {
			key = bearer(c)
		}
		if key == "" {
			c.AbortWithStatusJSON(401, ErrorResponse{Error: "invalid or missing API key"})
			return
		}

		user, err := s.store.GetUserByAPIKey(c.Request.Context(), key)
		if err != nil {
			msg := "invalid or missing API key"
			if errors.Is(err, store.ErrRevoked) {
				msg = "api key is revoked"
			}
			c.AbortWithStatusJSON(401, ErrorResponse{Error: msg})
			return
		}

		c.Set("user_id", user.ID)
		c.Set("api_key", key)
		c.Next()
	}
}

// requireSession authenticates the account endpoints (signin issued an
// HS256 JWT). The user id is stored in the context as "user_id".
func (s *Server) requireSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := bearer(c)
		if token == "" {
			c.AbortWithStatusJSON(401, ErrorResponse{Error: "missing bearer token"})
			return
		}
		userID, err := auth.ParseSessionToken(token, s.cfg.JWTSecret)
		if err != nil {
			c.AbortWithStatusJSON(401, ErrorResponse{Error: "invalid or expired session"})
			return
		}
		c.Set("user_id", userID)
		c.Next()
	}
}
