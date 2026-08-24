package mongorepo_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/toji339/online-judge/internal/database"
	"github.com/toji339/online-judge/internal/problem"
	"github.com/toji339/online-judge/internal/problem/mongorepo"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Slug uniqueness is a database guarantee, not an application one. These
// tests need a real MongoDB because what is under test is how concurrent
// inserts interleave against a unique index — a fake cannot produce the
// duplicate-key rejection that is the whole mechanism.
//
// Set TEST_MONGO_URI to run them; without it they skip.

// raceTitle is unique per run so a crashed run cannot poison the next
// one, and so two runs in parallel do not collide.
func raceTitle() string {
	return "Slug Race " + bson.NewObjectID().Hex()
}

func testDB(t *testing.T) *mongo.Database {
	t.Helper()

	uri := os.Getenv("TEST_MONGO_URI")
	if uri == "" {
		t.Skip("TEST_MONGO_URI not set — skipping MongoDB-backed slug race tests")
	}

	client, err := database.Connect(uri)
	if err != nil {
		t.Fatalf("connect to %s: %v", uri, err)
	}

	// A throwaway database, so the indexes this builds and the rows it
	// writes cannot disturb anything else.
	db := client.Database("online_judge_slug_race_" + bson.NewObjectID().Hex())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = db.Drop(ctx)
		_ = client.Disconnect(ctx)
	})

	// The unique index on slug is the authority the repository leans on.
	if err := database.EnsureIndexes(db); err != nil {
		t.Fatalf("ensure indexes: %v", err)
	}
	return db
}

// TestEnsureIndexes_MakesTheSlugIndexUnique is the precondition for
// everything below: without SetUnique the duplicate-key error never
// happens and the race has nothing to settle it.
func TestEnsureIndexes_MakesTheSlugIndexUnique(t *testing.T) {
	db := testDB(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cursor, err := db.Collection("problems").Indexes().List(ctx)
	if err != nil {
		t.Fatalf("list indexes: %v", err)
	}
	var specs []struct {
		Name   string `bson:"name"`
		Key    bson.D `bson:"key"`
		Unique bool   `bson:"unique"`
	}
	if err := cursor.All(ctx, &specs); err != nil {
		t.Fatalf("decode indexes: %v", err)
	}

	for _, spec := range specs {
		if spec.Name == "slug_1" {
			if !spec.Unique {
				t.Fatal("slug_1 exists but is not unique — nothing enforces one problem per slug")
			}
			return
		}
	}
	t.Fatalf("no slug_1 index on problems; got %v", specs)
}

// TestRepositoryCreate_ExactlyOneConcurrentInsertWins drives the
// mechanism directly: N goroutines insert the identical slug at once.
// The index admits exactly one and rejects the rest, and every rejection
// must arrive as the typed conflict rather than a raw driver error.
func TestRepositoryCreate_ExactlyOneConcurrentInsertWins(t *testing.T) {
	db := testDB(t)
	repo := mongorepo.New(db)

	const racers = 16
	slug := "slug-race-" + bson.NewObjectID().Hex()

	// Every goroutine blocks on the same channel so the inserts are
	// genuinely simultaneous rather than merely concurrent.
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, racers)

	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			p := &problem.Problem{
				Title: "Slug Race", Slug: slug,
				Difficulty: problem.DifficultyEasy,
				Tags:       []string{}, CompanyTags: []problem.CompanyTagSummary{},
				TimeLimitMS: 2000, MemoryLimitMB: 256,
				StarterCode: map[string]string{}, CreatedAt: time.Now().UTC(),
			}
			errs[i] = repo.Create(context.Background(), p)
		}(i)
	}
	close(start)
	wg.Wait()

	winners := 0
	for i, err := range errs {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, problem.ErrSlugConflict):
			// The documented, clean outcome for a loser.
		default:
			t.Errorf("racer %d failed with an unexpected error: %v", i, err)
		}
	}
	if winners != 1 {
		t.Errorf("%d inserts of the same slug succeeded, want exactly 1", winners)
	}

	assertRowCount(t, db, slug, 1)
}

// TestServiceCreate_ConcurrentSameTitleLeavesNoPartialRows is the
// end-to-end shape: N admin requests post the same title at once. The
// service allocates a slug and inserts, so a racer either wins its slug
// or loses cleanly — never a 500, and never a row for a request that was
// told it failed.
func TestServiceCreate_ConcurrentSameTitleLeavesNoPartialRows(t *testing.T) {
	db := testDB(t)
	svc := problem.NewService(mongorepo.New(db))

	const racers = 16
	title := raceTitle()

	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]*problem.Problem, racers)
	errs := make([]error, racers)

	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = svc.Create(context.Background(), problem.CreateProblemInput{
				Title:         title,
				Statement:     "Given an array of integers...",
				Difficulty:    problem.DifficultyEasy,
				Tags:          []string{"arrays"},
				TimeLimitMS:   2000,
				MemoryLimitMB: 256,
			})
		}(i)
	}
	close(start)
	wg.Wait()

	// Every failure must be the documented conflict. Anything else is the
	// opaque 500 this change exists to remove.
	slugs := map[string]int{}
	winners := 0
	conflicts := 0
	for i, err := range errs {
		switch {
		case err == nil:
			winners++
			if results[i] == nil || results[i].ID == "" {
				t.Errorf("racer %d succeeded without an ID", i)
				continue
			}
			slugs[results[i].Slug]++
		case errors.Is(err, problem.ErrSlugConflict):
			conflicts++
			if results[i] != nil {
				t.Errorf("racer %d lost the race but was handed a problem back", i)
			}
		default:
			t.Errorf("racer %d failed with an unexpected error: %v", i, err)
		}
	}
	t.Logf("%d/%d creates won, %d lost cleanly with ErrSlugConflict", winners, racers, conflicts)

	if winners == 0 {
		t.Fatal("no create succeeded at all")
	}
	for slug, n := range slugs {
		if n != 1 {
			t.Errorf("slug %q was handed to %d winners", slug, n)
		}
	}

	// The rows in the database must match the answers handed out exactly:
	// no row for a caller told it failed, and no caller told it succeeded
	// without a row.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stored, err := db.Collection("problems").CountDocuments(ctx, bson.M{"title": title})
	if err != nil {
		t.Fatalf("count problems: %v", err)
	}
	if int(stored) != winners {
		t.Errorf("%d rows stored for %d successful creates — partial rows were left behind", stored, winners)
	}
	for slug := range slugs {
		assertRowCount(t, db, slug, 1)
	}
}

// TestServiceCreate_SequentialSameTitleStillSuffixes guards the
// behaviour the conflict must not cost: creating the same title twice,
// one after the other, still yields "<slug>" then "<slug>-2".
func TestServiceCreate_SequentialSameTitleStillSuffixes(t *testing.T) {
	db := testDB(t)
	svc := problem.NewService(mongorepo.New(db))
	ctx := context.Background()

	input := problem.CreateProblemInput{
		Title:         "Two Sum",
		Statement:     "Given an array of integers...",
		Difficulty:    problem.DifficultyEasy,
		TimeLimitMS:   2000,
		MemoryLimitMB: 256,
	}

	first, err := svc.Create(ctx, input)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	second, err := svc.Create(ctx, input)
	if err != nil {
		t.Fatalf("second create: %v", err)
	}

	if first.Slug != "two-sum" {
		t.Errorf("first slug = %q, want %q", first.Slug, "two-sum")
	}
	if second.Slug != "two-sum-2" {
		t.Errorf("second slug = %q, want %q", second.Slug, "two-sum-2")
	}
}

func assertRowCount(t *testing.T, db *mongo.Database, slug string, want int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	n, err := db.Collection("problems").CountDocuments(ctx, bson.M{"slug": slug})
	if err != nil {
		t.Fatalf("count %q: %v", slug, err)
	}
	if int(n) != want {
		t.Error(fmt.Sprintf("slug %q is stored on %d documents, want %d", slug, n, want))
	}
}
