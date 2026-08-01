// Package queries holds every SQL statement used by the store, one file per
// query. Embedded into the binary so there is nothing to deploy alongside it.
package queries

import _ "embed"

//go:embed schema.sql
var Schema string

// --- users ---

//go:embed users/create.sql
var CreateUser string

//go:embed users/get_by_email.sql
var GetUserByEmail string

//go:embed users/get_by_id.sql
var GetUserByID string

// --- api keys ---

//go:embed api_keys/create.sql
var CreateAPIKey string

//go:embed api_keys/list.sql
var ListAPIKeys string

//go:embed api_keys/get_user_by_key.sql
var GetUserByAPIKey string

//go:embed api_keys/set_revoked.sql
var SetAPIKeyRevoked string

//go:embed api_keys/delete.sql
var DeleteAPIKey string

//go:embed api_keys/touch.sql
var TouchAPIKey string

// --- submissions ---

//go:embed submissions/create.sql
var CreateSubmission string

//go:embed submissions/update_result.sql
var UpdateSubmissionResult string

//go:embed submissions/list.sql
var ListSubmissions string

//go:embed submissions/get.sql
var GetSubmission string

//go:embed submissions/delete_old.sql
var DeleteOldSubmissions string
