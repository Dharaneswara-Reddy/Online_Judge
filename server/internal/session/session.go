// Package session holds server-side revocation state for issued JWTs.
//
// # Why this exists
//
// A JWT is self-contained: once signed it is valid until its exp, and
// nothing the server does can take it back. Logging out only cleared the
// browser cookie, so a stolen or leaked token stayed good for the full
// 24 hour lifetime and there was no answer to "revoke this admin now".
//
// # The design
//
// Every token carries a jti — a random per-session identifier. This
// package records the sessions that have been revoked, and the auth
// middleware consults it on each authenticated request.
//
// There are two kinds of record, and one lookup reads both:
//
//   - a session record ends exactly one token. This is what logout
//     writes, so signing out on a laptop leaves the phone signed in.
//   - a user record is a barrier: every token for that account issued
//     before an instant is dead. This is "log out everywhere" and the
//     incident-response lever, and it works in constant space no matter
//     how many sessions the account has open.
//
// # Why it is not a permanent denylist
//
// Both records carry the expiry of the tokens they revoke, and are
// reclaimed by a MongoDB TTL index at that moment. A revocation that
// outlived its token would be dead weight forever: the token it names
// could not be accepted anyway. Reads also ignore an entry past its
// expiry, so the guarantee does not depend on when the database's
// background sweep happens to run.
//
// # Why MongoDB and not Redis
//
// Redis is optional in this system — the API is required to keep working
// without it, with rate limits disabled and War Room fan-out reduced to
// one instance. Revocation cannot be in that category: a security
// control that silently stops working when a cache is absent is worse
// than one that was never claimed. MongoDB is already mandatory (nothing
// starts without it), it is durable across the process restarts that
// every release performs, and the lookup is a primary-key read of a
// collection that only ever holds unexpired revocations.
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// Checker answers whether a token has been revoked. The auth middleware
// depends on this narrow half rather than the whole store.
type Checker interface {
	// IsRevoked reports whether the token identified by sessionID, issued
	// to userID at issuedAt, has been revoked.
	//
	// All three arguments are needed because a token can be revoked two
	// ways: by its own identifier, or by a barrier covering the account.
	// Passing them together keeps it to one round trip.
	IsRevoked(ctx context.Context, sessionID, userID string, issuedAt time.Time) (bool, error)
}

// Revoker writes revocation state. The auth controller depends on this
// half.
type Revoker interface {
	// RevokeSession ends one session. expiresAt is the exp of the token
	// being revoked, and is when the record becomes reclaimable.
	//
	// It is idempotent: revoking an already-revoked session is not an
	// error, because a double logout is not a problem worth surfacing.
	RevokeSession(ctx context.Context, sessionID string, expiresAt time.Time) error

	// RevokeUserSessions ends every token for one account that was issued
	// before issuedBefore. expiresAt is when the barrier stops mattering,
	// which is the furthest ahead any covered token could have expired —
	// in practice now plus the token lifetime.
	//
	// The barrier only ever moves forward, so an out-of-order call cannot
	// un-revoke sessions an earlier call ended.
	RevokeUserSessions(ctx context.Context, userID string, issuedBefore, expiresAt time.Time) error
}

// Store is the full surface, implemented by MongoStore and by the
// in-memory fake used in unit tests.
type Store interface {
	Checker
	Revoker
}

// sessionIDBytes is the entropy behind a jti. 128 bits is the same
// budget as a UUID: the identifier is what revocation is keyed by, so a
// guessable one would let an attacker end somebody else's session.
const sessionIDBytes = 16

// NewSessionID mints the per-session identifier carried as the token's
// jti claim.
func NewSessionID() (string, error) {
	buf := make([]byte, sessionIDBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate a session id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
