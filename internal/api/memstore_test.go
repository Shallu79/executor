package api

import (
	"context"
	"sync"
	"time"

	"executor/internal/models"
	"executor/internal/store"
)

// memStore is an in-memory Store for tests.
type memStore struct {
	mu         sync.Mutex
	users      map[string]models.User
	byEmail    map[string]string // email -> id
	keys       map[string]models.APIKey
	keyByHash  map[string]string // hash -> key id
	subs       map[string]models.Submission
	subsByUser map[string][]string
	nextID     int
}

func newMemStore() *memStore {
	return &memStore{
		users:      map[string]models.User{},
		byEmail:    map[string]string{},
		keys:       map[string]models.APIKey{},
		keyByHash:  map[string]string{},
		subs:       map[string]models.Submission{},
		subsByUser: map[string][]string{},
	}
}

func (m *memStore) newID(prefix string) string {
	m.nextID++
	return prefix + string(rune('a'+m.nextID))
}

// --- users ---

func (m *memStore) CreateUser(ctx context.Context, email, passwordHash string) (models.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byEmail[email]; ok {
		return models.User{}, store.ErrConflict
	}
	u := models.User{ID: m.newID("u"), Email: email, PasswordHash: passwordHash, CreatedAt: time.Now().UTC()}
	m.users[u.ID] = u
	m.byEmail[email] = u.ID
	return u, nil
}

func (m *memStore) GetUserByEmail(ctx context.Context, email string) (models.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byEmail[email]
	if !ok {
		return models.User{}, store.ErrNotFound
	}
	return m.users[id], nil
}

func (m *memStore) GetUserByID(ctx context.Context, id string) (models.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return models.User{}, store.ErrNotFound
	}
	return u, nil
}

// --- api keys ---

func (m *memStore) CreateAPIKey(ctx context.Context, userID, name, keyHash, prefix string) (models.APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := models.APIKey{ID: m.newID("k"), UserID: userID, Name: name, Prefix: prefix, KeyHash: keyHash, CreatedAt: time.Now().UTC()}
	m.keys[k.ID] = k
	m.keyByHash[keyHash] = k.ID
	return k, nil
}

func (m *memStore) ListAPIKeys(ctx context.Context, userID string) ([]models.APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []models.APIKey
	for _, k := range m.keys {
		if k.UserID == userID {
			out = append(out, k)
		}
	}
	return out, nil
}

func (m *memStore) GetUserByAPIKey(ctx context.Context, plaintextKey string) (models.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.keyByHash[store.HashAPIKey(plaintextKey)]
	if !ok {
		return models.User{}, store.ErrNotFound
	}
	k := m.keys[id]
	if k.Revoked {
		return models.User{}, store.ErrRevoked
	}
	now := time.Now().UTC()
	k.LastUsedAt = &now
	m.keys[id] = k
	return m.users[k.UserID], nil
}

func (m *memStore) SetAPIKeyRevoked(ctx context.Context, userID, keyID string, revoked bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[keyID]
	if !ok || k.UserID != userID {
		return store.ErrNotFound
	}
	k.Revoked = revoked
	m.keys[keyID] = k
	return nil
}

func (m *memStore) DeleteAPIKey(ctx context.Context, userID, keyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[keyID]
	if !ok || k.UserID != userID {
		return store.ErrNotFound
	}
	delete(m.keys, keyID)
	delete(m.keyByHash, k.KeyHash)
	return nil
}

// --- submissions ---

func (m *memStore) CreateSubmission(ctx context.Context, sub models.Submission) (models.Submission, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sub.CreatedAt.IsZero() {
		sub.CreatedAt = time.Now().UTC()
	}
	m.subs[sub.ID] = sub
	m.subsByUser[sub.UserID] = append(m.subsByUser[sub.UserID], sub.ID)
	return sub, nil
}

func (m *memStore) UpdateSubmissionResult(ctx context.Context, id, status string, exitCode int, stdout, stderr string, timeMs int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.subs[id]
	if !ok {
		return store.ErrNotFound
	}
	s.Status = status
	s.ExitCode = exitCode
	s.Stdout = stdout
	s.Stderr = stderr
	s.TimeMs = timeMs
	m.subs[id] = s
	return nil
}

func (m *memStore) ListSubmissions(ctx context.Context, userID string, limit int) ([]models.Submission, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := m.subsByUser[userID]
	out := make([]models.Submission, 0, len(ids))
	for i := len(ids) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, m.subs[ids[i]])
	}
	return out, nil
}

func (m *memStore) GetSubmission(ctx context.Context, userID, id string) (models.Submission, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.subs[id]
	if !ok || s.UserID != userID {
		return models.Submission{}, store.ErrNotFound
	}
	return s, nil
}

func (m *memStore) DeleteOldSubmissions(ctx context.Context, olderThan time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for id, s := range m.subs {
		if s.CreatedAt.Before(olderThan) {
			delete(m.subs, id)
			n++
		}
	}
	return n, nil
}
