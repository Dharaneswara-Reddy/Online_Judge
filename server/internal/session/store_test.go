package session_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/toji339/online-judge/internal/database"
	"github.com/toji339/online-judge/internal/session"
)

// hour is the token lifetime these tests pretend to use. The real one is
// 24 hours; nothing here depends on the number, only on "later than now".
const hour = time.Hour

// storeFactory builds a fresh, empty store. Every implementation of
// session.Store is run through the same contract below, so the Mongo
// store and the in-memory fake cannot drift apart.
type storeFactory struct {
	name string
	// build returns a store, or skips the test when its backing service
	// is not available.
	build func(t *testing.T) session.Store
}

// mongoTestStore connects to the local test MongoDB, or skips.
//
// It uses a run-scoped database name so a failed run cannot poison the
// next one, and drops it on cleanup.
func mongoTestStore(t *testing.T) session.Store {
	t.Helper()

	uri := os.Getenv("TEST_MONGO_URI")
	if uri == "" {
		t.Skip("TEST_MONGO_URI not set — skipping the Mongo-backed store contract")
	}

	client, err := database.Connect(uri)
	require.NoError(t, err)

	db := client.Database("online_judge_session_test_" + bson.NewObjectID().Hex())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = db.Drop(ctx)
		_ = client.Disconnect(ctx)
	})

	store := session.NewMongoStore(db)
	require.NoError(t, store.EnsureIndexes(t.Context()))
	return store
}

func factories() []storeFactory {
	return []storeFactory{
		{name: "memory", build: func(*testing.T) session.Store { return session.NewMemoryStore() }},
		{name: "mongo", build: mongoTestStore},
	}
}

// forEachStore runs one contract test against every implementation.
func forEachStore(t *testing.T, name string, body func(t *testing.T, store session.Store)) {
	t.Helper()
	for _, f := range factories() {
		t.Run(f.name+"/"+name, func(t *testing.T) {
			body(t, f.build(t))
		})
	}
}

// =============================================================
// The revocation contract
// =============================================================

func TestStore_AnUnknownSessionIsNotRevoked(t *testing.T) {
	forEachStore(t, "unknown", func(t *testing.T, store session.Store) {
		revoked, err := store.IsRevoked(t.Context(), "never-seen", "user-1", time.Now())

		require.NoError(t, err)
		assert.False(t, revoked, "the default answer must be 'still valid'")
	})
}

func TestStore_RevokedSessionIsRejected(t *testing.T) {
	forEachStore(t, "revoked", func(t *testing.T, store session.Store) {
		require.NoError(t, store.RevokeSession(t.Context(), "sid-1", time.Now().Add(hour)))

		revoked, err := store.IsRevoked(t.Context(), "sid-1", "user-1", time.Now())

		require.NoError(t, err)
		assert.True(t, revoked)
	})
}

// TestStore_RevokingOneSessionLeavesTheOthers is the requirement that
// separates per-session revocation from "log out everywhere": signing
// out on your laptop must not sign you out on your phone.
func TestStore_RevokingOneSessionLeavesTheOthers(t *testing.T) {
	forEachStore(t, "one_of_many", func(t *testing.T, store session.Store) {
		issued := time.Now()
		require.NoError(t, store.RevokeSession(t.Context(), "laptop", issued.Add(hour)))

		laptop, err := store.IsRevoked(t.Context(), "laptop", "user-1", issued)
		require.NoError(t, err)
		phone, err := store.IsRevoked(t.Context(), "phone", "user-1", issued)
		require.NoError(t, err)
		tablet, err := store.IsRevoked(t.Context(), "tablet", "user-1", issued)
		require.NoError(t, err)

		assert.True(t, laptop)
		assert.False(t, phone, "a second session of the same user must survive")
		assert.False(t, tablet)
	})
}

// TestStore_RevokeAllEndsEverySessionOfOneUser is the incident-response
// lever: one call ends every token that user is holding.
func TestStore_RevokeAllEndsEverySessionOfOneUser(t *testing.T) {
	forEachStore(t, "revoke_all", func(t *testing.T, store session.Store) {
		issued := time.Now().Add(-10 * time.Minute)
		barrier := time.Now()

		require.NoError(t, store.RevokeUserSessions(t.Context(), "user-1", barrier, barrier.Add(hour)))

		for _, sid := range []string{"laptop", "phone", "tablet"} {
			revoked, err := store.IsRevoked(t.Context(), sid, "user-1", issued)
			require.NoError(t, err)
			assert.True(t, revoked, "session %q should have been swept", sid)
		}

		// Another user is untouched.
		other, err := store.IsRevoked(t.Context(), "laptop", "user-2", issued)
		require.NoError(t, err)
		assert.False(t, other, "revoking one account must not sign out the rest")
	})
}

// TestStore_RevokeAllSparesLaterLogins is what makes the barrier usable:
// after revoking everything, the user can sign in again immediately and
// the new token works.
func TestStore_RevokeAllSparesLaterLogins(t *testing.T) {
	forEachStore(t, "revoke_all_then_login", func(t *testing.T, store session.Store) {
		barrier := time.Now()
		require.NoError(t, store.RevokeUserSessions(t.Context(), "user-1", barrier, barrier.Add(hour)))

		freshlyIssued := barrier.Add(2 * time.Second)
		revoked, err := store.IsRevoked(t.Context(), "new-session", "user-1", freshlyIssued)

		require.NoError(t, err)
		assert.False(t, revoked, "a token issued after the barrier must still work")
	})
}

// TestStore_RevocationsExpireWithTheirToken is the "not an unbounded
// denylist" requirement. A record that outlives the token it revokes is
// dead weight forever, so once the token would have expired anyway the
// entry must stop mattering — whether or not the storage layer has got
// round to reclaiming it.
func TestStore_RevocationsExpireWithTheirToken(t *testing.T) {
	forEachStore(t, "expiry", func(t *testing.T, store session.Store) {
		alreadyExpired := time.Now().Add(-time.Minute)
		require.NoError(t, store.RevokeSession(t.Context(), "old-sid", alreadyExpired))

		revoked, err := store.IsRevoked(t.Context(), "old-sid", "user-1", time.Now().Add(-2*hour))

		require.NoError(t, err)
		assert.False(t, revoked,
			"a revocation past its token's expiry must not be consulted")
	})
}

// TestStore_RevokeAllIsIdempotentAndNeverMovesBackwards guards a subtle
// one: two revoke-all calls must leave the later barrier standing. A
// naive overwrite would let a stale second call un-revoke sessions the
// first call had ended.
func TestStore_RevokeAllIsIdempotentAndNeverMovesBackwards(t *testing.T) {
	forEachStore(t, "barrier_monotonic", func(t *testing.T, store session.Store) {
		later := time.Now()
		earlier := later.Add(-time.Hour)

		require.NoError(t, store.RevokeUserSessions(t.Context(), "user-1", later, later.Add(hour)))
		require.NoError(t, store.RevokeUserSessions(t.Context(), "user-1", earlier, earlier.Add(hour)))

		// Issued between the two barriers: the later one must still bite.
		revoked, err := store.IsRevoked(t.Context(), "sid", "user-1", later.Add(-time.Minute))

		require.NoError(t, err)
		assert.True(t, revoked, "the barrier must never move backwards")
	})
}

// TestStore_RevokeSessionIsIdempotent — logout twice is not an error.
func TestStore_RevokeSessionIsIdempotent(t *testing.T) {
	forEachStore(t, "idempotent", func(t *testing.T, store session.Store) {
		expiry := time.Now().Add(hour)
		require.NoError(t, store.RevokeSession(t.Context(), "sid", expiry))
		require.NoError(t, store.RevokeSession(t.Context(), "sid", expiry))

		revoked, err := store.IsRevoked(t.Context(), "sid", "user-1", time.Now())
		require.NoError(t, err)
		assert.True(t, revoked)
	})
}

// TestStore_ConcurrentUse is run under -race in CI. Every authenticated
// request reads this store, so a data race here is a race on the whole
// API's hot path.
func TestStore_ConcurrentUse(t *testing.T) {
	forEachStore(t, "concurrent", func(t *testing.T, store session.Store) {
		const workers = 16
		expiry := time.Now().Add(hour)

		var wg sync.WaitGroup
		errs := make(chan error, workers*3)
		for i := range workers {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				sid := "sid-" + bson.NewObjectID().Hex()
				if err := store.RevokeSession(t.Context(), sid, expiry); err != nil {
					errs <- err
					return
				}
				if _, err := store.IsRevoked(t.Context(), sid, "user-1", time.Now()); err != nil {
					errs <- err
					return
				}
				if err := store.RevokeUserSessions(t.Context(), "user-concurrent", time.Now(), expiry); err != nil {
					errs <- err
				}
			}(i)
		}
		wg.Wait()
		close(errs)

		for err := range errs {
			assert.NoError(t, err)
		}
	})
}

// =============================================================
// Durability
// =============================================================

// TestMongoStore_SurvivesARestart is the point of choosing Mongo over an
// in-process map: a revoked token must stay revoked when the API process
// is replaced, which on this deployment happens on every release.
//
// The restart is simulated the only way a test can — a brand new client,
// connection pool and store, pointed at the same database.
func TestMongoStore_SurvivesARestart(t *testing.T) {
	uri := os.Getenv("TEST_MONGO_URI")
	if uri == "" {
		t.Skip("TEST_MONGO_URI not set — skipping the durability test")
	}

	dbName := "online_judge_session_restart_" + bson.NewObjectID().Hex()

	connect := func(t *testing.T) (*mongo.Client, *mongo.Database) {
		client, err := database.Connect(uri)
		require.NoError(t, err)
		return client, client.Database(dbName)
	}

	// 1. One process revokes a session and exits.
	before, db := connect(t)
	require.NoError(t, session.NewMongoStore(db).RevokeSession(t.Context(), "sid-durable", time.Now().Add(hour)))
	require.NoError(t, session.NewMongoStore(db).RevokeUserSessions(t.Context(), "user-durable", time.Now(), time.Now().Add(hour)))
	require.NoError(t, before.Disconnect(t.Context()))

	// 2. A fresh process asks about the same token.
	after, db2 := connect(t)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = db2.Drop(ctx)
		_ = after.Disconnect(ctx)
	})

	store := session.NewMongoStore(db2)

	revoked, err := store.IsRevoked(t.Context(), "sid-durable", "someone", time.Now())
	require.NoError(t, err)
	assert.True(t, revoked, "a restart must not resurrect a revoked session")

	swept, err := store.IsRevoked(t.Context(), "any", "user-durable", time.Now().Add(-time.Minute))
	require.NoError(t, err)
	assert.True(t, swept, "a restart must not resurrect a swept account")
}

// TestMongoStore_CreatesTheTTLIndex proves the entries are reclaimed by
// the database rather than accumulating forever. The sweep itself is
// MongoDB's background task on roughly a one minute period, which is not
// something a test should wait on; what this asserts is that the index
// telling it to sweep exists, and expires on the token's own expiry.
func TestMongoStore_CreatesTheTTLIndex(t *testing.T) {
	uri := os.Getenv("TEST_MONGO_URI")
	if uri == "" {
		t.Skip("TEST_MONGO_URI not set — skipping the TTL index test")
	}

	client, err := database.Connect(uri)
	require.NoError(t, err)
	db := client.Database("online_judge_session_ttl_" + bson.NewObjectID().Hex())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = db.Drop(ctx)
		_ = client.Disconnect(ctx)
	})

	require.NoError(t, session.NewMongoStore(db).EnsureIndexes(t.Context()))

	cursor, err := db.Collection(session.CollectionName).Indexes().List(t.Context())
	require.NoError(t, err)
	var specs []struct {
		Name               string `bson:"name"`
		ExpireAfterSeconds *int32 `bson:"expireAfterSeconds"`
	}
	require.NoError(t, cursor.All(t.Context(), &specs))

	var found bool
	for _, s := range specs {
		if s.ExpireAfterSeconds != nil {
			found = true
			assert.Equal(t, int32(0), *s.ExpireAfterSeconds,
				"entries expire at the moment stored in expires_at, not later")
		}
	}
	assert.True(t, found, "the revocation collection must carry a TTL index")
}

// TestNewSessionID_IsUnpredictableAndUnique covers the identifier the
// whole scheme is keyed by. A guessable one would let an attacker revoke
// somebody else's session.
func TestNewSessionID_IsUnpredictableAndUnique(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for range 1000 {
		id, err := session.NewSessionID()
		require.NoError(t, err)
		assert.Len(t, id, 32, "128 bits, hex encoded")
		assert.False(t, seen[id], "session ids must not repeat")
		seen[id] = true
	}
}
