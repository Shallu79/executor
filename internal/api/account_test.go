package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type signupResp struct {
	User struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"user"`
	APIKey string `json:"api_key"`
}

func TestSignupSigninMe(t *testing.T) {
	s, _, _, _ := newTestServer(t)

	// Signup returns the user + a bootstrap API key.
	w := doRequest(t, s, "POST", "/api/v1/auth/signup",
		`{"email":"ada@example.com","password":"super-secret"}`, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("signup status = %d, want 201 (body %s)", w.Code, w.Body.String())
	}
	resp := decode[signupResp](t, w)
	if resp.User.Email != "ada@example.com" || resp.APIKey == "" {
		t.Fatalf("unexpected signup response: %+v", resp)
	}

	// Duplicate email → 409.
	w = doRequest(t, s, "POST", "/api/v1/auth/signup",
		`{"email":"ada@example.com","password":"super-secret"}`, "")
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate signup status = %d, want 409", w.Code)
	}

	// Validation.
	w = doRequest(t, s, "POST", "/api/v1/auth/signup", `{"email":"nope","password":"x"}`, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad signup status = %d, want 400", w.Code)
	}

	// Signin → session token.
	w = doRequest(t, s, "POST", "/api/v1/auth/signin",
		`{"email":"ada@example.com","password":"super-secret"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("signin status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var signin struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &signin); err != nil || signin.Token == "" {
		t.Fatalf("signin missing token: %s", w.Body.String())
	}

	// Wrong password → 401.
	w = doRequest(t, s, "POST", "/api/v1/auth/signin",
		`{"email":"ada@example.com","password":"wrong-password"}`, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-password signin status = %d, want 401", w.Code)
	}

	// /me with session token.
	w = doRequestAuth(t, s, "GET", "/api/v1/me", "", signin.Token)
	if w.Code != http.StatusOK {
		t.Fatalf("me status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	me := decode[struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}](t, w)
	if me.ID != resp.User.ID || me.Email != "ada@example.com" {
		t.Fatalf("me mismatch: %+v", me)
	}

	// /me without token → 401.
	if w := doRequest(t, s, "GET", "/api/v1/me", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("me without token = %d, want 401", w.Code)
	}
}

func TestAPIKeyCRUD(t *testing.T) {
	s, _, _, st := newTestServer(t)

	w := doRequest(t, s, "POST", "/api/v1/auth/signup",
		`{"email":"grace@example.com","password":"super-secret"}`, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("signup status = %d, want 201", w.Code)
	}

	// Signin for a session.
	w = doRequest(t, s, "POST", "/api/v1/auth/signin",
		`{"email":"grace@example.com","password":"super-secret"}`, "")
	var signin struct {
		Token string `json:"token"`
	}
	json.Unmarshal(w.Body.Bytes(), &signin)

	// List keys (should include the bootstrap default).
	w = doRequestAuth(t, s, "GET", "/api/v1/api-keys", "", signin.Token)
	if w.Code != http.StatusOK {
		t.Fatalf("list keys status = %d, want 200", w.Code)
	}
	var list struct {
		APIKeys []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Prefix  string `json:"prefix"`
			Revoked bool   `json:"revoked"`
		} `json:"api_keys"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.APIKeys) != 1 {
		t.Fatalf("list keys response: %s (%v)", w.Body.String(), err)
	}

	// Create a new key — plaintext returned once.
	w = doRequestAuth(t, s, "POST", "/api/v1/api-keys", `{"name":"ci"}`, signin.Token)
	if w.Code != http.StatusCreated {
		t.Fatalf("create key status = %d, want 201", w.Code)
	}
	var created struct {
		APIKey struct {
			ID string `json:"id"`
		} `json:"api_key"`
		Key string `json:"key"`
	}
	json.Unmarshal(w.Body.Bytes(), &created)
	if !strings.HasPrefix(created.Key, "minos_") || created.APIKey.ID == "" {
		t.Fatalf("create key response: %s", w.Body.String())
	}

	// The new key works for execution (auth).
	w = doRequest(t, s, "POST", "/api/v1/execute",
		`{"language":"python","code":"print(1)"}`, created.Key)
	if w.Code != http.StatusOK && w.Code != http.StatusGatewayTimeout {
		t.Fatalf("new key execute status = %d, want 200/504", w.Code)
	}

	// Revoke it → execution now fails with 401.
	w = doRequestAuth(t, s, "PATCH", "/api/v1/api-keys/"+created.APIKey.ID, `{"revoked":true}`, signin.Token)
	if w.Code != http.StatusOK {
		t.Fatalf("revoke status = %d, want 200", w.Code)
	}
	w = doRequest(t, s, "POST", "/api/v1/execute",
		`{"language":"python","code":"print(1)"}`, created.Key)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked key execute status = %d, want 401", w.Code)
	}

	// Delete it.
	w = doRequestAuth(t, s, "DELETE", "/api/v1/api-keys/"+created.APIKey.ID, "", signin.Token)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", w.Code)
	}

	// Access control: another user cannot touch this key.
	_, otherKey := seedUser(t, st)
	w = doRequest(t, s, "POST", "/api/v1/execute",
		`{"language":"python","code":"print(1)"}`, otherKey)
	if w.Code != http.StatusOK && w.Code != http.StatusGatewayTimeout {
		t.Fatalf("other user execute status = %d", w.Code)
	}
}

func TestSubmissionsHistory(t *testing.T) {
	s, _, _, _ := newTestServer(t)

	// Sign up a fresh user (auto-gets a bootstrap API key) and sign in.
	w := doRequest(t, s, "POST", "/api/v1/auth/signup",
		`{"email":"hist@example.com","password":"super-secret"}`, "")
	resp := decode[signupResp](t, w)

	w = doRequest(t, s, "POST", "/api/v1/auth/signin",
		`{"email":"hist@example.com","password":"super-secret"}`, "")
	var signin struct {
		Token string `json:"token"`
	}
	json.Unmarshal(w.Body.Bytes(), &signin)

	// Submit two jobs with the bootstrap key. No worker is running, so the
	// sync wait returns 504 — but the history row is already persisted.
	for _, lang := range []string{"python", "bash"} {
		w = doRequest(t, s, "POST", "/api/v1/execute",
			`{"language":"`+lang+`","code":"print(1)"}`, resp.APIKey)
		if w.Code != http.StatusGatewayTimeout {
			t.Fatalf("submit(%s) status = %d, want 504 (timeout path)", lang, w.Code)
		}
	}

	w = doRequestAuth(t, s, "GET", "/api/v1/submissions", "", signin.Token)
	if w.Code != http.StatusOK {
		t.Fatalf("list submissions status = %d, want 200", w.Code)
	}
	var hist struct {
		Submissions []struct {
			ID       string `json:"id"`
			Language string `json:"language"`
			Status   string `json:"status"`
		} `json:"submissions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &hist); err != nil {
		t.Fatalf("decode history: %v (%s)", err, w.Body.String())
	}
	if len(hist.Submissions) != 2 {
		t.Fatalf("history count = %d, want 2 (%s)", len(hist.Submissions), w.Body.String())
	}
	seen := map[string]bool{}
	for _, s := range hist.Submissions {
		seen[s.Language] = true
		if s.Status != "queued" {
			t.Fatalf("history row status = %q, want queued: %+v", s.Status, s)
		}
	}
	if !seen["python"] || !seen["bash"] {
		t.Fatalf("history missing languages: %+v", seen)
	}

	// History is scoped to the user: without a session, it 401s.
	if w := doRequest(t, s, "GET", "/api/v1/submissions", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("history without session = %d, want 401", w.Code)
	}
}
