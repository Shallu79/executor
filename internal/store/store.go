// Package store persists accounts, API keys, and job history in postgres.
// The API depends on the Store interface; the postgres implementation backs
// it in production, and tests use an in-memory fake.
package store

import (
	"context"
	"errors"
	"time"

	"executor/internal/models"
)

// Sentinel errors returned by Store methods.
var (
	ErrNotFound = errors.New("store: not found")
	ErrConflict = errors.New("store: conflict (already exists)")
	ErrRevoked  = errors.New("store: api key revoked")
)

// Store is the persistence contract the API needs.
type Store interface {
	// Users
	CreateUser(ctx context.Context, email, passwordHash string) (models.User, error)
	GetUserByEmail(ctx context.Context, email string) (models.User, error)
	GetUserByID(ctx context.Context, id string) (models.User, error)

	// API keys
	CreateAPIKey(ctx context.Context, userID, name, keyHash, prefix string) (models.APIKey, error)
	ListAPIKeys(ctx context.Context, userID string) ([]models.APIKey, error)
	GetUserByAPIKey(ctx context.Context, plaintextKey string) (models.User, error)
	SetAPIKeyRevoked(ctx context.Context, userID, keyID string, revoked bool) error
	DeleteAPIKey(ctx context.Context, userID, keyID string) error

	// Submissions (job history)
	CreateSubmission(ctx context.Context, sub models.Submission) (models.Submission, error)
	UpdateSubmissionResult(ctx context.Context, id, status string, exitCode int, stdout, stderr string, timeMs int64) error
	ListSubmissions(ctx context.Context, userID string, limit int) ([]models.Submission, error)
	GetSubmission(ctx context.Context, userID, id string) (models.Submission, error)

	// DeleteOldSubmissions removes rows older than the cutoff (retention
	// window). Returns the number of rows deleted.
	DeleteOldSubmissions(ctx context.Context, olderThan time.Time) (int64, error)
}

// Migration applies the schema. In-memory fakes need no schema.
func Migration(ctx context.Context, s Store) error {
	pg, ok := s.(*Postgres)
	if !ok {
		return nil
	}
	return pg.Migration(ctx)
}
