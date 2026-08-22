package mongorepo_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/toji339/online-judge/internal/companytag"
	"github.com/toji339/online-judge/internal/companytag/mongorepo"
	"github.com/toji339/online-judge/internal/database"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// These tests need a real MongoDB because what is being tested is how
// concurrent updates interleave inside the database. A fake cannot show
// a lost update that only the database's own concurrency produces.
//
// Set TEST_MONGO_URI to run them; without it they skip.
func testDB(t *testing.T) *mongo.Database {
	t.Helper()

	uri := os.Getenv("TEST_MONGO_URI")
	if uri == "" {
		t.Skip("TEST_MONGO_URI not set — skipping MongoDB-backed company tag tests")
	}

	client, err := database.Connect(uri)
	if err != nil {
		t.Fatalf("connect to %s: %v", uri, err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })

	db := client.Database("online_judge_test")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = db.Collection("problems").DeleteMany(ctx, bson.M{"slug": "companytag-concurrency"})
	})
	return db
}

// insertProblem creates a problem document and returns its hex ID.
// companyTags is stored verbatim so the pre-company-tags shape (a null
// field) can be reproduced.
func insertProblem(t *testing.T, db *mongo.Database, companyTags any) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := db.Collection("problems").InsertOne(ctx, bson.M{
		"title":         "Concurrency",
		"slug":          "companytag-concurrency",
		"difficulty":    "easy",
		"tags":          []string{},
		"company_tags":  companyTags,
		"created_at":    time.Now().UTC(),
		"time_limit_ms": 1000,
	})
	if err != nil {
		t.Fatalf("insert problem: %v", err)
	}
	return result.InsertedID.(bson.ObjectID).Hex()
}

func summaryCount(t *testing.T, db *mongo.Database, problemID, company string) int {
	t.Helper()

	oid, err := bson.ObjectIDFromHex(problemID)
	if err != nil {
		t.Fatalf("bad problem id: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var doc struct {
		CompanyTags []struct {
			Company string `bson:"company"`
			Count   int    `bson:"count"`
		} `bson:"company_tags"`
	}
	if err := db.Collection("problems").FindOne(ctx, bson.M{"_id": oid}).Decode(&doc); err != nil {
		t.Fatalf("read problem: %v", err)
	}
	for _, entry := range doc.CompanyTags {
		if entry.Company == company {
			return entry.Count
		}
	}
	return 0
}

// TestIncrementSummary_ConcurrentFirstTagsAreNotLost is the regression
// test for the lost update. The append guarded against a concurrent
// insert with $ne but ignored the result: when the guard matched nothing
// because another request had just appended the entry, that increment
// vanished, and the count under-reported permanently.
func TestIncrementSummary_ConcurrentFirstTagsAreNotLost(t *testing.T) {
	db := testDB(t)
	repo := mongorepo.New(db)
	problemID := insertProblem(t, db, []any{})

	const taggers = 32
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, taggers)

	for i := 0; i < taggers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release everyone at once, so the appends collide
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := repo.IncrementSummary(ctx, problemID, "Google"); err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("IncrementSummary: %v", err)
	}

	if got := summaryCount(t, db, problemID, "Google"); got != taggers {
		t.Errorf("company tag count = %d, want %d — %d increment(s) were lost",
			got, taggers, taggers-got)
	}
}

// TestIncrementSummary_NormalisesANullSummaryArray keeps the older
// documents working: problems created before company tags existed store
// company_tags as null, and $push cannot append to null.
func TestIncrementSummary_NormalisesANullSummaryArray(t *testing.T) {
	db := testDB(t)
	repo := mongorepo.New(db)
	problemID := insertProblem(t, db, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	for i := 0; i < 3; i++ {
		if err := repo.IncrementSummary(ctx, problemID, "Amazon"); err != nil {
			t.Fatalf("IncrementSummary: %v", err)
		}
	}
	if got := summaryCount(t, db, problemID, "Amazon"); got != 3 {
		t.Errorf("company tag count = %d, want 3", got)
	}
}

// TestIncrementSummary_KeepsCompaniesApart guards the positional update:
// bumping one company must not touch another's count.
func TestIncrementSummary_KeepsCompaniesApart(t *testing.T) {
	db := testDB(t)
	repo := mongorepo.New(db)
	problemID := insertProblem(t, db, []any{})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	for _, company := range []string{"Google", "Google", "Meta"} {
		if err := repo.IncrementSummary(ctx, problemID, company); err != nil {
			t.Fatalf("IncrementSummary: %v", err)
		}
	}
	if got := summaryCount(t, db, problemID, "Google"); got != 2 {
		t.Errorf("Google count = %d, want 2", got)
	}
	if got := summaryCount(t, db, problemID, "Meta"); got != 1 {
		t.Errorf("Meta count = %d, want 1", got)
	}
}

var _ = companytag.ErrAlreadyTagged
