// Package models defines the wire formats shared between the API gateway
// and the worker: the Job pulled from minos:queue and the ExecutionResult
// pushed to minos:result:{job_id}.
package models

import "time"

// Job is the unit of work pulled from the minos:queue Redis list.
// It is produced by the API gateway and consumed by workers via BLPOP.
type Job struct {
	ID       string    `json:"id"`
	Language string    `json:"language"`
	Code     string    `json:"code"`
	Stdin    string    `json:"stdin,omitempty"`
	Created  time.Time `json:"created"`
}

// ExecutionResult is pushed to minos:result:{job_id} once a job finishes
// (successfully or not). The API gateway block-waits on that key.
type ExecutionResult struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	ExitCode  int    `json:"exit_code"`
	TimeMs    int64  `json:"time_ms"`
	Truncated bool   `json:"truncated,omitempty"`
}
