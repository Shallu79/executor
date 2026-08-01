package models

import "time"

// User is an account in the Minos system.
type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

// APIKey is a user-scoped credential used to submit jobs. The plaintext key
// is shown exactly once at creation; only its hash and a display prefix are
// stored (in the postgres store).
type APIKey struct {
	ID         string     `json:"id"`
	UserID     string     `json:"-"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	KeyHash    string     `json:"-"`
	Revoked    bool       `json:"revoked"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Submission is a persisted job for history. It mirrors the lifecycle of a
// queued job: "queued" until the worker produces a result, then "completed".
type Submission struct {
	ID        string    `json:"id"`
	UserID    string    `json:"-"`
	Language  string    `json:"language"`
	Code      string    `json:"code"`
	Stdin     string    `json:"stdin"`
	Status    string    `json:"status"`
	ExitCode  int       `json:"exit_code"`
	Stdout    string    `json:"stdout"`
	Stderr    string    `json:"stderr"`
	TimeMs    int64     `json:"time_ms"`
	CreatedAt time.Time `json:"created_at"`
}
