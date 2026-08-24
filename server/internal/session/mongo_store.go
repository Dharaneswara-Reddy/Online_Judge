package session

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// CollectionName is where revocation records live. Exported so tests and
// operators can look at it without guessing.
const CollectionName = "revoked_sessions"

// Key prefixes keep the two kinds of record in one collection, which is
// what lets a single _id lookup answer both questions at once.
const (
	sessionKeyPrefix = "s:"
	userKeyPrefix    = "u:"
)

// revocation is one record.
//
// Session records use only ExpiresAt. User records also carry NotBefore,
// the barrier every token of that account must have been issued after.
type revocation struct {
	ID        string    `bson:"_id"`
	NotBefore time.Time `bson:"not_before,omitempty"`
	ExpiresAt time.Time `bson:"expires_at"`
}

// MongoStore is the durable implementation of Store.
type MongoStore struct {
	collection *mongo.Collection
}

// NewMongoStore builds a store over the given database.
//
// It performs no I/O. Call EnsureIndexes once at startup to create the
// TTL index; the store is correct without it, but the collection would
// then grow instead of being reclaimed.
func NewMongoStore(db *mongo.Database) *MongoStore {
	return &MongoStore{collection: db.Collection(CollectionName)}
}

// compile-time proof that the store satisfies the contract.
var _ Store = (*MongoStore)(nil)

// EnsureIndexes creates the TTL index that reclaims expired records.
//
// expireAfterSeconds of zero means "delete when the date in this field
// has passed", so each record is swept at the exact moment the token it
// revokes expired anyway. MongoDB runs that sweep on roughly a one
// minute period, so a record may linger a little past its expiry —
// which is why IsRevoked also filters on expires_at rather than trusting
// the sweep to have happened.
//
// Creating an index is idempotent; calling this on every start is safe.
func (s *MongoStore) EnsureIndexes(ctx context.Context) error {
	_, err := s.collection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "expires_at", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(0).SetName("revocation_ttl"),
	})
	if err != nil {
		return fmt.Errorf("failed to create the revocation TTL index: %w", err)
	}
	return nil
}

// RevokeSession ends one session.
//
// $setOnInsert with an upsert makes this idempotent without a read: the
// first call writes the record, and a repeat leaves the existing expiry
// alone rather than racing to rewrite it.
func (s *MongoStore) RevokeSession(ctx context.Context, sessionID string, expiresAt time.Time) error {
	if sessionID == "" {
		return fmt.Errorf("cannot revoke a session with no identifier")
	}

	_, err := s.collection.UpdateOne(ctx,
		bson.M{"_id": sessionKeyPrefix + sessionID},
		bson.M{"$setOnInsert": bson.M{"expires_at": expiresAt.UTC()}},
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil {
		return fmt.Errorf("failed to revoke session: %w", err)
	}
	return nil
}

// RevokeUserSessions raises the account's barrier.
//
// $max rather than $set is the load-bearing part: two revocations racing
// each other, or arriving out of order, must leave the later barrier
// standing. A plain overwrite would let a stale call quietly un-revoke
// sessions an earlier call had ended. $max also sets the field on insert
// when the document does not exist yet, so one operation covers both.
func (s *MongoStore) RevokeUserSessions(ctx context.Context, userID string, issuedBefore, expiresAt time.Time) error {
	if userID == "" {
		return fmt.Errorf("cannot revoke sessions for an empty user id")
	}

	_, err := s.collection.UpdateOne(ctx,
		bson.M{"_id": userKeyPrefix + userID},
		bson.M{"$max": bson.M{
			"not_before": issuedBefore.UTC(),
			"expires_at": expiresAt.UTC(),
		}},
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil {
		return fmt.Errorf("failed to revoke user sessions: %w", err)
	}
	return nil
}

// IsRevoked answers both questions in one round trip.
//
// The $in over the two possible keys means an authenticated request
// costs a single primary-key read of a collection that holds only
// unexpired revocations — so in practice a few rows, fully resident.
//
// The expires_at filter is not redundant with the TTL index. The sweep
// is periodic and best-effort, and the index may be missing entirely on
// a database that has never had EnsureIndexes run against it; without
// this filter a stale record could revoke a token that had itself long
// since expired, which is harmless but confusing, or — after a clock
// change — one that had not.
func (s *MongoStore) IsRevoked(ctx context.Context, sessionID, userID string, issuedAt time.Time) (bool, error) {
	keys := make([]string, 0, 2)
	if sessionID != "" {
		keys = append(keys, sessionKeyPrefix+sessionID)
	}
	if userID != "" {
		keys = append(keys, userKeyPrefix+userID)
	}
	if len(keys) == 0 {
		return false, nil
	}

	now := time.Now().UTC()
	cursor, err := s.collection.Find(ctx, bson.M{
		"_id":        bson.M{"$in": keys},
		"expires_at": bson.M{"$gt": now},
	})
	if err != nil {
		return false, fmt.Errorf("failed to read revocation state: %w", err)
	}

	var records []revocation
	if err := cursor.All(ctx, &records); err != nil {
		return false, fmt.Errorf("failed to decode revocation state: %w", err)
	}

	for _, record := range records {
		switch record.ID {
		case sessionKeyPrefix + sessionID:
			// This exact session was signed out.
			return true, nil
		case userKeyPrefix + userID:
			// The whole account was swept. Only tokens issued before the
			// barrier are covered, so the user can sign in again at once.
			if issuedAt.Before(record.NotBefore) {
				return true, nil
			}
		}
	}

	return false, nil
}
