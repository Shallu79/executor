package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"minos/internal/models"
	"minos/internal/store/queries"
)

// Postgres is the production Store backed by pgx. All SQL lives in the
// embedded queries package — nothing here contains raw SQL strings.
type Postgres struct {
	pool *pgxpool.Pool
}

// NewPostgres connects and verifies the database.
func NewPostgres(ctx context.Context, databaseURL string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Postgres{pool: pool}, nil
}

// Close releases the connection pool.
func (p *Postgres) Close() { p.pool.Close() }

func (p *Postgres) Pool() *pgxpool.Pool { return p.pool }

// Migration applies the schema at startup.
func (p *Postgres) Migration(ctx context.Context) error {
	_, err := p.pool.Exec(ctx, queries.Schema)
	return err
}

// HashAPIKey is the canonical way to store/compare API keys.
func HashAPIKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// isUniqueViolation reports whether err is a postgres unique constraint error.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// --- Users ---

func (p *Postgres) CreateUser(ctx context.Context, email, passwordHash string) (models.User, error) {
	user := models.User{
		ID:           newID(),
		Email:        email,
		PasswordHash: passwordHash,
		CreatedAt:    time.Now().UTC(),
	}
	_, err := p.pool.Exec(ctx, queries.CreateUser, user.ID, user.Email, user.PasswordHash)
	if isUniqueViolation(err) {
		return models.User{}, ErrConflict
	}
	if err != nil {
		return models.User{}, err
	}
	return user, nil
}

func (p *Postgres) GetUserByEmail(ctx context.Context, email string) (models.User, error) {
	var u models.User
	err := p.pool.QueryRow(ctx, queries.GetUserByEmail, email).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.User{}, ErrNotFound
	}
	return u, err
}

func (p *Postgres) GetUserByID(ctx context.Context, id string) (models.User, error) {
	var u models.User
	err := p.pool.QueryRow(ctx, queries.GetUserByID, id).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.User{}, ErrNotFound
	}
	return u, err
}

// --- API keys ---

func (p *Postgres) CreateAPIKey(ctx context.Context, userID, name, keyHash, prefix string) (models.APIKey, error) {
	key := models.APIKey{
		ID:        newID(),
		UserID:    userID,
		Name:      name,
		Prefix:    prefix,
		KeyHash:   keyHash,
		CreatedAt: time.Now().UTC(),
	}
	_, err := p.pool.Exec(ctx, queries.CreateAPIKey,
		key.ID, key.UserID, key.Name, key.KeyHash, key.Prefix)
	if err != nil {
		return models.APIKey{}, err
	}
	return key, nil
}

func (p *Postgres) ListAPIKeys(ctx context.Context, userID string) ([]models.APIKey, error) {
	rows, err := p.pool.Query(ctx, queries.ListAPIKeys, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []models.APIKey
	for rows.Next() {
		var k models.APIKey
		if err := rows.Scan(&k.ID, &k.UserID, &k.Name, &k.Prefix, &k.KeyHash,
			&k.Revoked, &k.LastUsedAt, &k.CreatedAt); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (p *Postgres) GetUserByAPIKey(ctx context.Context, plaintextKey string) (models.User, error) {
	hash := HashAPIKey(plaintextKey)
	var (
		user models.User
		key  models.APIKey
	)
	err := p.pool.QueryRow(ctx, queries.GetUserByAPIKey, hash).
		Scan(&user.ID, &user.Email, &user.PasswordHash, &user.CreatedAt, &key.ID, &key.Revoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.User{}, ErrNotFound
	}
	if err != nil {
		return models.User{}, err
	}
	if key.Revoked {
		return models.User{}, ErrRevoked
	}
	// Best-effort touch: never fail an execution because of a failed stat.
	_, _ = p.pool.Exec(ctx, queries.TouchAPIKey, key.ID)
	return user, nil
}

func (p *Postgres) SetAPIKeyRevoked(ctx context.Context, userID, keyID string, revoked bool) error {
	tag, err := p.pool.Exec(ctx, queries.SetAPIKeyRevoked, revoked, keyID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) DeleteAPIKey(ctx context.Context, userID, keyID string) error {
	tag, err := p.pool.Exec(ctx, queries.DeleteAPIKey, keyID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Submissions ---

func (p *Postgres) CreateSubmission(ctx context.Context, sub models.Submission) (models.Submission, error) {
	if sub.ID == "" {
		sub.ID = newID()
	}
	if sub.CreatedAt.IsZero() {
		sub.CreatedAt = time.Now().UTC()
	}
	if sub.Status == "" {
		sub.Status = "queued"
	}
	_, err := p.pool.Exec(ctx, queries.CreateSubmission,
		sub.ID, sub.UserID, sub.Language, sub.Code, sub.Stdin, sub.Status,
		sub.ExitCode, sub.Stdout, sub.Stderr, sub.TimeMs, sub.CreatedAt)
	return sub, err
}

func (p *Postgres) UpdateSubmissionResult(ctx context.Context, id, status string, exitCode int, stdout, stderr string, timeMs int64) error {
	tag, err := p.pool.Exec(ctx, queries.UpdateSubmissionResult,
		status, exitCode, stdout, stderr, timeMs, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) ListSubmissions(ctx context.Context, userID string, limit int) ([]models.Submission, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := p.pool.Query(ctx, queries.ListSubmissions, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []models.Submission
	for rows.Next() {
		var s models.Submission
		if err := rows.Scan(&s.ID, &s.UserID, &s.Language, &s.Code, &s.Stdin,
			&s.Status, &s.ExitCode, &s.Stdout, &s.Stderr, &s.TimeMs, &s.CreatedAt); err != nil {
			return nil, err
		}
		subs = append(subs, s)
	}
	return subs, rows.Err()
}

func (p *Postgres) GetSubmission(ctx context.Context, userID, id string) (models.Submission, error) {
	var s models.Submission
	err := p.pool.QueryRow(ctx, queries.GetSubmission, id, userID).
		Scan(&s.ID, &s.UserID, &s.Language, &s.Code, &s.Stdin,
			&s.Status, &s.ExitCode, &s.Stdout, &s.Stderr, &s.TimeMs, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Submission{}, ErrNotFound
	}
	return s, err
}

// DeleteOldSubmissions removes rows older than the given cutoff, enforcing
// the retention window. Returns the number of deleted rows.
func (p *Postgres) DeleteOldSubmissions(ctx context.Context, olderThan time.Time) (int64, error) {
	tag, err := p.pool.Exec(ctx, queries.DeleteOldSubmissions, olderThan.UTC())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
