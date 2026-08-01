package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"github.com/gin-gonic/gin"

	"minos/internal/auth"
	"minos/internal/models"
	"minos/internal/store"
)

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

func newAPIKey() (plaintext, prefix string) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	plaintext = "minos_" + hex.EncodeToString(b)
	return plaintext, plaintext[:10]
}

func userJSON(u models.User) gin.H {
	return gin.H{"id": u.ID, "email": u.Email, "created_at": u.CreatedAt}
}

func apiKeyJSON(k models.APIKey) gin.H {
	return gin.H{
		"id":           k.ID,
		"name":         k.Name,
		"prefix":       k.Prefix,
		"revoked":      k.Revoked,
		"last_used_at": k.LastUsedAt,
		"created_at":   k.CreatedAt,
	}
}

type credentialsRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// signup creates a user, hashes the password, and auto-issues a first API
// key so the account can execute immediately.
func (s *Server) signup(c *gin.Context) {
	var req credentialsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "invalid payload"})
		return
	}
	if !emailRe.MatchString(req.Email) {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "invalid email"})
		return
	}
	if len(req.Password) < 8 {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "password must be at least 8 characters"})
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		s.log.Error("password hash failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "internal error"})
		return
	}

	user, err := s.store.CreateUser(c.Request.Context(), req.Email, hash)
	if errors.Is(err, store.ErrConflict) {
		c.JSON(http.StatusConflict, ErrorResponse{Error: "email already registered"})
		return
	}
	if err != nil {
		s.log.Error("create user failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "internal error"})
		return
	}

	// Bootstrap: issue the user's first API key.
	plaintext, prefix := newAPIKey()
	if _, err := s.store.CreateAPIKey(c.Request.Context(),
		user.ID, "default", store.HashAPIKey(plaintext), prefix); err != nil {
		s.log.Error("create default api key failed", "user_id", user.ID, "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "internal error"})
		return
	}

	s.log.Info("user signed up", "user_id", user.ID, "email", user.Email)
	c.JSON(http.StatusCreated, gin.H{
		"user":    userJSON(user),
		"api_key": plaintext,
	})
}

func (s *Server) signin(c *gin.Context) {
	var req credentialsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "invalid payload"})
		return
	}

	user, err := s.store.GetUserByEmail(c.Request.Context(), req.Email)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !auth.CheckPassword(user.PasswordHash, req.Password)) {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "invalid email or password"})
		return
	}
	if err != nil {
		s.log.Error("get user failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "internal error"})
		return
	}

	token, err := auth.NewSessionToken(user.ID, s.cfg.JWTSecret)
	if err != nil {
		s.log.Error("sign token failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "internal error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"token": token, "user": userJSON(user)})
}

func (s *Server) me(c *gin.Context) {
	user, err := s.store.GetUserByID(c.Request.Context(), c.GetString("user_id"))
	if err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: "user not found"})
		return
	}
	c.JSON(http.StatusOK, userJSON(user))
}

func (s *Server) listAPIKeys(c *gin.Context) {
	keys, err := s.store.ListAPIKeys(c.Request.Context(), c.GetString("user_id"))
	if err != nil {
		s.log.Error("list api keys failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "internal error"})
		return
	}
	out := make([]gin.H, 0, len(keys))
	for _, k := range keys {
		out = append(out, apiKeyJSON(k))
	}
	c.JSON(http.StatusOK, gin.H{"api_keys": out})
}

func (s *Server) createAPIKey(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "name is required"})
		return
	}

	plaintext, prefix := newAPIKey()
	key, err := s.store.CreateAPIKey(c.Request.Context(),
		c.GetString("user_id"), req.Name, store.HashAPIKey(plaintext), prefix)
	if err != nil {
		s.log.Error("create api key failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "internal error"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"api_key": apiKeyJSON(key), "key": plaintext})
}

func (s *Server) updateAPIKey(c *gin.Context) {
	var req struct {
		Revoked *bool `json:"revoked"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Revoked == nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "revoked is required"})
		return
	}
	if err := s.store.SetAPIKeyRevoked(c.Request.Context(),
		c.GetString("user_id"), c.Param("id"), *req.Revoked); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{Error: "api key not found"})
			return
		}
		s.log.Error("update api key failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"revoked": *req.Revoked})
}

func (s *Server) deleteAPIKey(c *gin.Context) {
	if err := s.store.DeleteAPIKey(c.Request.Context(),
		c.GetString("user_id"), c.Param("id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{Error: "api key not found"})
			return
		}
		s.log.Error("delete api key failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "internal error"})
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

func (s *Server) listSubmissions(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	subs, err := s.store.ListSubmissions(c.Request.Context(), c.GetString("user_id"), limit)
	if err != nil {
		s.log.Error("list submissions failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"submissions": subs})
}

func (s *Server) getSubmission(c *gin.Context) {
	sub, err := s.store.GetSubmission(c.Request.Context(), c.GetString("user_id"), c.Param("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{Error: "submission not found"})
			return
		}
		s.log.Error("get submission failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "internal error"})
		return
	}
	c.JSON(http.StatusOK, sub)
}
