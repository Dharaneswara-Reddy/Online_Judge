// Package database handles connecting to MongoDB Atlas,
// graceful disconnection, and index creation. All database
// access in the application flows through the client and
// database references established here.
package database

import (
	"context"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Driver limits for a single small instance.
//
// The driver's defaults assume a large fleet talking to a large cluster:
// 100 connections per host (200 across a two-host Atlas replica set), a
// 30 second server-selection timeout, and no default operation timeout
// at all. Every one of those is the wrong end of the trade-off here.
const (
	// maxPoolSize caps connections per server. The API runs on two vCPUs
	// inside a small memory limit, and each pooled connection costs a
	// socket plus the driver's per-connection read/write buffers — a few
	// hundred idle connections are a real slice of the process's heap for
	// work the CPU could never do in parallel anyway. Twenty leaves ample
	// headroom over the handful of concurrent database operations a
	// two-vCPU box can actually make progress on, while still absorbing a
	// burst of requests that are all waiting on Mongo at once.
	maxPoolSize uint64 = 20

	// minPoolSize keeps a couple of connections warm so the first request
	// after an idle period does not pay a TCP and TLS handshake to Atlas.
	minPoolSize uint64 = 2

	// maxConnIdleTime returns memory and sockets to the OS after a burst
	// instead of holding the high-water mark until the process restarts.
	maxConnIdleTime = 60 * time.Second

	// serverSelectionTimeout is how long an operation waits for a usable
	// server before failing. The 30 second default means an unreachable
	// Atlas turns every in-flight request into a 30 second hang, which
	// exhausts the HTTP server long before anyone sees an error. Five
	// seconds still rides out a normal replica-set election (typically
	// well under two seconds) while failing fast on a real outage.
	serverSelectionTimeout = 5 * time.Second

	// connectTimeout bounds one TCP + TLS handshake to Atlas.
	connectTimeout = 5 * time.Second

	// operationTimeout is the client-level deadline applied to any
	// operation whose context carries none of its own — which is every
	// query made from an HTTP handler, since Gin's request context has no
	// deadline. Without it a stalled operation blocks a goroutine (and
	// its pooled connection) indefinitely. Callers that legitimately need
	// longer, such as the index builds below, set their own deadline: the
	// driver only applies this timeout when the context has none.
	operationTimeout = 10 * time.Second
)

// clientOptions builds the driver options used for every connection.
//
// The limits are applied after ApplyURI so they win over anything in the
// connection string: they are a safety envelope for this deployment, not
// a default to be talked out of by a copy-pasted Atlas URI.
func clientOptions(uri string) *options.ClientOptions {
	return options.Client().
		ApplyURI(uri).
		// ObjectIDAsHexString lets documents whose _id is an ObjectID
		// decode into a plain Go string field. Domain types such as
		// problem.Problem and submission.Submission model their ID as a
		// string so the domain packages stay free of driver types;
		// without this option every read of those collections fails to
		// decode.
		SetBSONOptions(&options.BSONOptions{ObjectIDAsHexString: true}).
		SetMaxPoolSize(maxPoolSize).
		SetMinPoolSize(minPoolSize).
		SetMaxConnIdleTime(maxConnIdleTime).
		SetServerSelectionTimeout(serverSelectionTimeout).
		SetConnectTimeout(connectTimeout).
		SetTimeout(operationTimeout)
}

// Connect establishes a connection to MongoDB Atlas using
// the provided URI. It pings the server to verify the
// connection is alive before returning the client.
func Connect(uri string) (*mongo.Client, error) {
	// Steps to follow while connecting to MongoDB
	// =============================================

	// 1. Create a context with a 10-second timeout for the connection
	//    attempt. Server selection gives up sooner than this in practice,
	//    so an unreachable database is reported in about five seconds.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 2. Connect to MongoDB using the provided URI and the bounded
	//    resource limits above.
	client, err := mongo.Connect(clientOptions(uri))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
	}

	// 3. Ping the database to confirm the connection is alive
	err = client.Ping(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to ping MongoDB: %w", err)
	}

	log.Println("Connected to MongoDB Atlas successfully")
	return client, nil
}

// Disconnect gracefully closes the MongoDB connection.
// This should be called when the server is shutting down
// to release resources cleanly.
func Disconnect(client *mongo.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Disconnect(ctx); err != nil {
		log.Printf("Error disconnecting from MongoDB: %v", err)
	} else {
		log.Println("Disconnected from MongoDB")
	}
}

// ensure creates each index, logging and carrying on past a failure.
//
// One index at a time rather than one CreateMany per collection: a batch
// is refused as a whole, so a single index that cannot be built — the
// partial unique one below is the realistic case, on a database that
// already holds duplicate in-flight submissions — would silently cost
// its neighbours as well.
//
// A failure here is loud but never fatal. A missing index degrades a
// guarantee (a constraint stops being enforced, a query gets slower);
// refusing to start degrades everything, and on a restored backup or a
// database left dirty by a crashed run it would be an unbootable
// service that no amount of restarting fixes.
func ensure(ctx context.Context, coll *mongo.Collection, models []mongo.IndexModel) {
	created := 0
	for _, model := range models {
		if _, err := coll.Indexes().CreateOne(ctx, model); err != nil {
			log.Printf("ERROR: could not build index %v on %q: %v — "+
				"the service is starting without it and the guarantee it enforces is degraded",
				model.Keys, coll.Name(), err)
			continue
		}
		created++
	}
	log.Printf("%s: %d of %d indexes ensured", coll.Name(), created, len(models))
}

// EnsureIndexes creates every index the application relies on, across
// all collections — uniqueness constraints, the compound indexes the
// listing queries sort on, and the partial unique index that enforces
// submission admission control.
//
// Indexes are created idempotently — calling this function multiple
// times is safe. If the indexes already exist, MongoDB simply returns
// them without error.
//
// It does not return an error when an index cannot be built; see ensure
// for why. The error result is retained for a condition that genuinely
// should stop startup, should one ever arise.
func EnsureIndexes(db *mongo.Database) error {
	// Steps to follow while creating indexes
	// ========================================

	// 1. Get a reference to the users collection
	collection := db.Collection("users")

	// 2. Define the unique indexes we need:
	//    - username must be unique across all users
	//    - email must be unique across all users
	indexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "username", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		{
			Keys:    bson.D{{Key: "email", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
	}

	// 3. Create the indexes on the collection.
	// One budget covers every build below. Index builds on a collection
	// with millions of documents take far longer than a request would,
	// so this is generous; when it runs out the remaining builds are
	// logged as failures and startup continues without them.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ensure(ctx, collection, indexes)

	// --- Problem collection indexes ---
	problemsColl := db.Collection("problems")
	// Every listing sorts by created_at descending, so each filter needs
	// that as the trailing key — otherwise Mongo has to fetch the matches
	// and sort them in memory, which fails outright past 32MB.
	//
	// Title search deliberately has no index of its own. It is an
	// unanchored case-insensitive regex, which no b-tree index can serve,
	// and a $text index would answer a different question (whole words
	// only) than the one the search box asks. Searching alongside a
	// difficulty or tag still rides the compound indexes below, with the
	// regex applied to that far smaller candidate set; a bare search scans
	// the problem catalogue, which is curated and small by construction.
	problemIndexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "slug", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		{
			Keys: bson.D{{Key: "created_at", Value: -1}},
		},
		{
			Keys: bson.D{{Key: "difficulty", Value: 1}, {Key: "created_at", Value: -1}},
		},
		{
			Keys: bson.D{{Key: "tags", Value: 1}, {Key: "created_at", Value: -1}},
		},
		{
			Keys: bson.D{{Key: "company_tags.company", Value: 1}, {Key: "created_at", Value: -1}},
		},
	}
	ensure(ctx, problemsColl, problemIndexes)

	// --- Test case collection indexes ---
	testCasesColl := db.Collection("test_cases")
	testCaseIndexes := []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "problem_id", Value: 1},
				{Key: "is_sample", Value: 1},
			},
		},
	}
	ensure(ctx, testCasesColl, testCaseIndexes)

	// --- Submission collection indexes ---
	// The compound user/time index backs the submission history page,
	// while the room index backs War Room winner lookups.
	submissionsColl := db.Collection("submissions")
	submissionIndexes := []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "user_id", Value: 1},
				{Key: "submitted_at", Value: -1},
			},
		},
		{
			Keys: bson.D{
				{Key: "problem_id", Value: 1},
				{Key: "status", Value: 1},
			},
		},
		{
			Keys: bson.D{
				{Key: "war_room_id", Value: 1},
				{Key: "judged_at", Value: 1},
			},
		},
		// The landing page counts accepted submissions; status is not a
		// prefix of any index above, so without this it is a collection
		// scan on the largest collection in the system.
		{
			Keys: bson.D{{Key: "status", Value: 1}},
		},
		// Backs the stale-submission sweep, which asks for non-terminal
		// rows older than a cutoff. Both the equality and the range are
		// in the key, so the sweep touches only the handful of documents
		// it is about to reclaim rather than every submission ever made.
		{
			Keys: bson.D{{Key: "status", Value: 1}, {Key: "submitted_at", Value: 1}},
		},
		// Covers both the profile's solved-problem set and its accepted
		// count without touching a document.
		{
			Keys: bson.D{
				{Key: "user_id", Value: 1},
				{Key: "status", Value: 1},
				{Key: "problem_id", Value: 1},
			},
		},
		// Admission control, enforced by the database rather than by a
		// count-then-insert in application code — which races, because two
		// concurrent requests both read "none in flight" before either
		// writes. Being partial, the constraint only covers non-terminal
		// submissions, so it releases itself the moment a verdict lands
		// and there is no counter to leak or reconcile.
		{
			Keys: bson.D{{Key: "user_id", Value: 1}},
			Options: options.Index().
				SetName("one_inflight_submission_per_user").
				SetUnique(true).
				SetPartialFilterExpression(bson.M{
					"status": bson.M{"$in": []string{"pending", "running"}},
				}),
		},
	}
	ensure(ctx, submissionsColl, submissionIndexes)

	// --- War room collection indexes ---
	// The room code is the shareable join key, so it must be unique.
	warRoomsColl := db.Collection("war_rooms")
	warRoomIndexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "room_code", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		{
			Keys: bson.D{
				{Key: "status", Value: 1},
				{Key: "created_at", Value: -1},
			},
		},
		{
			Keys: bson.D{{Key: "participants.user_id", Value: 1}, {Key: "created_at", Value: -1}},
		},
		// The second branch of the stale-room sweep filters on started_at.
		{
			Keys: bson.D{{Key: "status", Value: 1}, {Key: "started_at", Value: 1}},
		},
	}
	ensure(ctx, warRoomsColl, warRoomIndexes)

	// --- Discussion collection indexes ---
	discussionsColl := db.Collection("discussions")
	discussionIndexes := []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "problem_id", Value: 1},
				{Key: "created_at", Value: 1},
			},
		},
		{
			Keys: bson.D{{Key: "parent_id", Value: 1}},
		},
		// Serves the paginated root query: the filter and the full sort
		// key are both in the index, so a page is an index range scan
		// rather than a fetch-and-sort of the whole thread.
		{
			Keys: bson.D{
				{Key: "problem_id", Value: 1},
				{Key: "parent_id", Value: 1},
				{Key: "created_at", Value: -1},
				{Key: "_id", Value: -1},
			},
		},
	}
	ensure(ctx, discussionsColl, discussionIndexes)

	// --- Company tag collection indexes ---
	// The unique compound index is the constraint that stops one user
	// from inflating a single company's tag count on a problem.
	companyTagsColl := db.Collection("problem_company_tags")
	companyTagIndexes := []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "problem_id", Value: 1},
				{Key: "user_id", Value: 1},
				{Key: "company", Value: 1},
			},
			Options: options.Index().SetUnique(true),
		},
		{
			Keys: bson.D{{Key: "company", Value: 1}},
		},
	}
	ensure(ctx, companyTagsColl, companyTagIndexes)

	return nil
}
