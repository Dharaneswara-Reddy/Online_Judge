package database

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// testDatabase connects to the local test MongoDB, or skips. It uses a
// throwaway database name so a failed run cannot poison the next one.
func testDatabase(t *testing.T) *mongo.Database {
	t.Helper()

	uri := os.Getenv("TEST_MONGO_URI")
	if uri == "" {
		t.Skip("TEST_MONGO_URI not set — skipping index tests that need a real MongoDB")
	}

	client, err := Connect(uri)
	require.NoError(t, err)

	name := "online_judge_index_test_" + bson.NewObjectID().Hex()
	db := client.Database(name)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = db.Drop(ctx)
		_ = client.Disconnect(ctx)
	})
	return db
}

// indexNames lists the indexes that exist on a collection.
func indexNames(t *testing.T, coll *mongo.Collection) map[string]bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cursor, err := coll.Indexes().List(ctx)
	require.NoError(t, err)

	var specs []struct {
		Name string `bson:"name"`
	}
	require.NoError(t, cursor.All(ctx, &specs))

	names := make(map[string]bool, len(specs))
	for _, s := range specs {
		names[s.Name] = true
	}
	return names
}

// TestEnsureIndexes_CreatesTheAdmissionControlIndex is the happy path:
// on a clean database every index, including the partial unique one, is
// there afterwards.
func TestEnsureIndexes_CreatesTheAdmissionControlIndex(t *testing.T) {
	db := testDatabase(t)

	require.NoError(t, EnsureIndexes(db))

	assert.True(t, indexNames(t, db.Collection("submissions"))["one_inflight_submission_per_user"],
		"admission control is enforced by this index")
	assert.True(t, indexNames(t, db.Collection("users"))["email_1"])
}

// TestEnsureIndexes_SurvivesDuplicateInFlightSubmissions is the defect:
// a database restored from a backup, or left dirty by a crashed run, can
// already hold two pending submissions for one user. The partial unique
// index then cannot be built — but that degrades one guarantee, and must
// not stop the process starting, nor cost the other indexes.
func TestEnsureIndexes_SurvivesDuplicateInFlightSubmissions(t *testing.T) {
	db := testDatabase(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, err := db.Collection("submissions").InsertMany(ctx, []any{
		bson.M{"user_id": "dirty-user", "status": "pending", "submitted_at": time.Now()},
		bson.M{"user_id": "dirty-user", "status": "pending", "submitted_at": time.Now()},
	})
	require.NoError(t, err)

	assert.NoError(t, EnsureIndexes(db),
		"a duplicate row must not make the service unbootable")

	// The rest of the indexes still have to be there: one failed build
	// must not take its neighbours or the other collections with it.
	subIndexes := indexNames(t, db.Collection("submissions"))
	assert.False(t, subIndexes["one_inflight_submission_per_user"],
		"this is the index the dirty data blocks")
	assert.True(t, subIndexes["user_id_1_submitted_at_-1"],
		"the history index is unrelated to the failure and must still exist")
	assert.True(t, indexNames(t, db.Collection("users"))["username_1"],
		"a later collection must still be indexed")
	assert.True(t, indexNames(t, db.Collection("problems"))["slug_1"])
}
