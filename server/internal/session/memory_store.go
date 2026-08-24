package session

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MemoryStore is an in-process implementation of Store, used by unit
// tests so the middleware and controller can be exercised without a
// database.
//
// It is deliberately not offered as a production fallback. Its state
// dies with the process and is not shared between instances, so a
// revoked token would come back to life on the next deploy — which is
// exactly the failure this package exists to prevent. Production uses
// MongoStore; see the package comment for why not Redis.
type MemoryStore struct {
	mu       sync.RWMutex
	sessions map[string]time.Time // session id -> token expiry
	barriers map[string]barrier   // user id -> account barrier
}

// barrier is the in-memory form of a user record.
type barrier struct {
	notBefore time.Time
	expiresAt time.Time
}

// NewMemoryStore returns an empty store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sessions: make(map[string]time.Time),
		barriers: make(map[string]barrier),
	}
}

// compile-time proof that the fake models the real thing.
var _ Store = (*MemoryStore)(nil)

// RevokeSession ends one session.
func (s *MemoryStore) RevokeSession(_ context.Context, sessionID string, expiresAt time.Time) error {
	if sessionID == "" {
		return fmt.Errorf("cannot revoke a session with no identifier")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.sessions[sessionID]; !ok || expiresAt.After(existing) {
		s.sessions[sessionID] = expiresAt
	}
	return nil
}

// RevokeUserSessions raises the account's barrier, never lowering it —
// the same monotonic rule MongoStore gets from $max.
func (s *MemoryStore) RevokeUserSessions(_ context.Context, userID string, issuedBefore, expiresAt time.Time) error {
	if userID == "" {
		return fmt.Errorf("cannot revoke sessions for an empty user id")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current := s.barriers[userID]
	if issuedBefore.After(current.notBefore) {
		current.notBefore = issuedBefore
	}
	if expiresAt.After(current.expiresAt) {
		current.expiresAt = expiresAt
	}
	s.barriers[userID] = current
	return nil
}

// IsRevoked mirrors MongoStore, expired records included: an entry past
// its token's own expiry is ignored rather than consulted.
func (s *MemoryStore) IsRevoked(_ context.Context, sessionID, userID string, issuedAt time.Time) (bool, error) {
	now := time.Now()

	s.mu.RLock()
	defer s.mu.RUnlock()

	if expiry, ok := s.sessions[sessionID]; ok && sessionID != "" && expiry.After(now) {
		return true, nil
	}
	if b, ok := s.barriers[userID]; ok && userID != "" && b.expiresAt.After(now) {
		if issuedAt.Before(b.notBefore) {
			return true, nil
		}
	}
	return false, nil
}
