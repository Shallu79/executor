package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"executor/internal/config"
	"executor/internal/models"
	"executor/internal/redisq"
	"executor/internal/store"
)

func newTestServer(t *testing.T) (*Server, *redis.Client, *miniredis.Miniredis, *memStore) {
	t.Helper()
	mr := miniredis.RunT(t)
	cfg := config.APIConfig{
		RedisAddr:        mr.Addr(),
		QueueKey:         "executor:queue",
		ResultPrefix:     "executor:result:",
		ResultTTL:        5 * time.Minute,
		MaxCodeSize:      65536,
		MaxStdinSize:     1024,
		ExecutionTimeout: 2 * time.Second,
		JWTSecret:        "test-jwt-secret",
	}
	rdb := redisq.NewClient(mr.Addr(), "", 0)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := newMemStore()
	return NewServer(cfg, rdb, st, log), rdb, mr, st
}

// seedUser creates a user with an API key and returns the plaintext key.
// Each call uses a unique email.
func seedUser(t *testing.T, st store.Store) (userID, apiKey string) {
	t.Helper()
	email := "user+" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.com"
	user, err := st.CreateUser(context.Background(), email, "hashedpw")
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	plain := "executor_test_key_" + time.Now().String()
	if _, err := st.CreateAPIKey(context.Background(), user.ID, "default", store.HashAPIKey(plain), plain[:13]); err != nil {
		t.Fatalf("seed key: %v", err)
	}
	return user.ID, plain
}

func doRequest(t *testing.T, s *Server, method, path, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if key != "" {
		req.Header.Set(APIKeyHeader, key)
	}
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)
	return w
}

func doRequestAuth(t *testing.T, s *Server, method, path, body, bearerToken string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	return v
}

// --- execute / jobs (API key auth) ---

func TestExecuteRequiresAPIKey(t *testing.T) {
	s, _, _, _ := newTestServer(t)

	w := doRequest(t, s, "POST", "/api/v1/execute", `{"language":"python","code":"print(1)"}`, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no key: status = %d, want 401", w.Code)
	}

	w = doRequest(t, s, "POST", "/api/v1/execute", `{"language":"python","code":"print(1)"}`, "wrong-key")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key: status = %d, want 401", w.Code)
	}
}

func TestExecuteWithValidKey(t *testing.T) {
	s, rdb, _, st := newTestServer(t)
	userID, apiKey := seedUser(t, st)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- doRequest(t, s, "POST", "/api/v1/execute",
			`{"language":"python","code":"print(42)","stdin":"hi"}`, apiKey)
	}()

	var jobID string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		vals, err := rdb.LRange(t.Context(), "executor:queue", 0, -1).Result()
		if err == nil && len(vals) > 0 {
			var job models.Job
			if json.Unmarshal([]byte(vals[0]), &job) == nil && job.ID != "" {
				jobID = job.ID
				if job.UserID != userID {
					t.Fatalf("user_id = %q, want %q", job.UserID, userID)
				}
				if job.Language != "python" || job.Stdin != "hi" {
					t.Fatalf("job fields mismatch: %+v", job)
				}
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if jobID == "" {
		t.Fatal("job never reached the queue")
	}

	res := models.ExecutionResult{Stdout: "42\n", Stderr: "", ExitCode: 0, TimeMs: 22}
	payload, _ := json.Marshal(res)
	if err := rdb.LPush(t.Context(), "executor:result:"+jobID, payload).Err(); err != nil {
		t.Fatalf("simulate worker: %v", err)
	}

	w := <-done
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	got := decode[JobResponse](t, w)
	if got.Status != "completed" || got.Stdout != "42\n" || got.ExitCode != 0 || got.TimeMs != 22 {
		t.Fatalf("result mismatch: %+v", got)
	}

	// The submission must be persisted and marked completed.
	sub, err := st.GetSubmission(context.Background(), userID, jobID)
	if err != nil {
		t.Fatalf("submission missing: %v", err)
	}
	if sub.Status != "completed" || sub.Stdout != "42\n" {
		t.Fatalf("submission not backfilled: %+v", sub)
	}
}

func TestExecuteTimesOutWithoutWorker(t *testing.T) {
	s, rdb, _, st := newTestServer(t)
	_, apiKey := seedUser(t, st)

	w := doRequest(t, s, "POST", "/api/v1/execute",
		`{"language":"python","code":"print(1)"}`, apiKey)
	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504 (body %s)", w.Code, w.Body.String())
	}
	got := decode[JobResponse](t, w)
	if got.Status != "processing" || got.JobID == "" {
		t.Fatalf("504 body missing job_id/status: %+v", got)
	}
	if n := rdb.LLen(t.Context(), "executor:queue").Val(); n != 1 {
		t.Fatalf("queue length = %d, want 1", n)
	}
}

func TestExecuteValidation(t *testing.T) {
	s, rdb, _, st := newTestServer(t)
	_, apiKey := seedUser(t, st)

	cases := []struct {
		name string
		body string
	}{
		{"empty", `{}`},
		{"unsupported language", `{"language":"cobol","code":"DISPLAY 'HI'."}`},
		{"empty code", `{"language":"python","code":""}`},
		{"code too large", `{"language":"python","code":"` + strings.Repeat("a", 65537) + `"}`},
		{"invalid utf8", "{\"language\":\"python\",\"code\":\"\xff\xfe\"}"},
		{"null bytes", `{"language":"python","code":"print(1)\x00"}`},
		{"stdin too large", `{"language":"python","code":"print(1)","stdin":"` + strings.Repeat("b", 1025) + `"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doRequest(t, s, "POST", "/api/v1/execute", tc.body, apiKey)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", w.Code, w.Body.String())
			}
		})
	}

	if n := rdb.LLen(t.Context(), "executor:queue").Val(); n != 0 {
		t.Fatalf("queue length = %d, want 0", n)
	}
}

func TestGetJobProcessingThenCompleted(t *testing.T) {
	s, rdb, _, st := newTestServer(t)
	_, apiKey := seedUser(t, st)

	id := "job-0001"
	w := doRequest(t, s, "GET", "/api/v1/jobs/"+id, "", apiKey)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := decode[JobResponse](t, w); got.Status != "processing" {
		t.Fatalf("status = %q, want processing", got.Status)
	}

	res := models.ExecutionResult{Stdout: "42\n", Stderr: "", ExitCode: 0, TimeMs: 22}
	payload, _ := json.Marshal(res)
	if err := rdb.LPush(t.Context(), "executor:result:"+id, payload).Err(); err != nil {
		t.Fatalf("seed result: %v", err)
	}

	w = doRequest(t, s, "GET", "/api/v1/jobs/"+id, "", apiKey)
	got := decode[JobResponse](t, w)
	if got.Status != "completed" || got.Stdout != "42\n" || got.TimeMs != 22 {
		t.Fatalf("result mismatch: %+v", got)
	}
}

func TestGetJobRequiresAPIKey(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	w := doRequest(t, s, "GET", "/api/v1/jobs/abc", "", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestGetJobInvalidID(t *testing.T) {
	s, _, _, st := newTestServer(t)
	_, apiKey := seedUser(t, st)
	for _, id := range []string{"..", "a b", "inv@lid", "x\x00y"} {
		esc := url.PathEscape(id)
		w := doRequest(t, s, "GET", "/api/v1/jobs/"+esc, "", apiKey)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("id %q (esc %q): status = %d, want 400", id, esc, w.Code)
		}
	}
}

func TestHealthEndpointsUnauthenticated(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	if w := doRequest(t, s, "GET", "/healthz", "", ""); w.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", w.Code)
	}
	if w := doRequest(t, s, "GET", "/readyz", "", ""); w.Code != http.StatusOK {
		t.Fatalf("readyz status = %d, want 200", w.Code)
	}
}

// TestCORSPreflight ensures browser preflight for PATCH/DELETE passes — a
// missing method here silently breaks revoke/delete from the dashboard.
func TestCORSPreflight(t *testing.T) {
	s, _, _, _ := newTestServer(t)

	req := httptest.NewRequest("OPTIONS", "/api/v1/api-keys/abc",
		bytes.NewBufferString(""))
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "DELETE")
	req.Header.Set("Access-Control-Request-Headers", "authorization")
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", w.Code)
	}
	methods := w.Header().Get("Access-Control-Allow-Methods")
	for _, m := range []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"} {
		if !strings.Contains(methods, m) {
			t.Fatalf("preflight Allow-Methods %q missing %s", methods, m)
		}
	}
}
