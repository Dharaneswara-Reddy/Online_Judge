package problem_test

import (
	"context"
	"errors"
	"testing"

	"github.com/toji339/online-judge/internal/problem"
	"github.com/toji339/online-judge/internal/problem/problemtest"
)

// Slug allocation reads, checks, then writes. Two concurrent creates of
// the same title both see the slug free and both try to insert it. The
// database's unique index is what settles that — one insert wins and the
// other is rejected — and the loser must come back as a typed conflict
// the controller can turn into a 409, not as an opaque failure.

// TestService_Create_MapsARaceLostAtInsertToAConflict simulates the race
// deterministically: the hook fires after the service has picked a slug
// and is about to insert, which is exactly the window a second request
// slips through.
func TestService_Create_MapsARaceLostAtInsertToAConflict(t *testing.T) {
	repo := problemtest.NewFakeRepository()
	svc := problem.NewService(repo)
	ctx := context.Background()

	// The rival commits "two-sum" in the window between our slug check
	// and our insert.
	repo.BeforeCreate = func(*problem.Problem) {
		repo.BeforeCreate = nil
		rival := problem.Problem{Title: "Two Sum", Slug: "two-sum"}
		if err := repo.Create(ctx, &rival); err != nil {
			t.Errorf("rival create: %v", err)
		}
	}

	_, err := svc.Create(ctx, validInput())
	if !errors.Is(err, problem.ErrSlugConflict) {
		t.Fatalf("err = %v, want ErrSlugConflict — the loser of the race must be a clean conflict", err)
	}
}

// TestFakeRepository_CreateRejectsADuplicateSlug pins the fake to the
// same contract the unique index gives the real repository. A fake that
// happily stores two identical slugs would let the service pass a test
// that production fails.
func TestFakeRepository_CreateRejectsADuplicateSlug(t *testing.T) {
	repo := problemtest.NewFakeRepository()
	ctx := context.Background()

	first := problem.Problem{Title: "Two Sum", Slug: "two-sum"}
	if err := repo.Create(ctx, &first); err != nil {
		t.Fatalf("first create: %v", err)
	}

	second := problem.Problem{Title: "Two Sum", Slug: "two-sum"}
	if err := repo.Create(ctx, &second); !errors.Is(err, problem.ErrSlugConflict) {
		t.Fatalf("err = %v, want ErrSlugConflict", err)
	}
	if second.ID != "" {
		t.Error("a rejected create must not hand back an ID")
	}
}

// A create that loses the race must leave nothing behind — no half-built
// problem row for the next request to trip over.
func TestService_Create_ConflictStoresNothing(t *testing.T) {
	repo := problemtest.NewFakeRepository()
	svc := problem.NewService(repo)
	ctx := context.Background()

	repo.BeforeCreate = func(*problem.Problem) {
		repo.BeforeCreate = nil
		rival := problem.Problem{Title: "Two Sum", Slug: "two-sum"}
		_ = repo.Create(ctx, &rival)
	}

	if _, err := svc.Create(ctx, validInput()); err == nil {
		t.Fatal("expected the create to fail")
	}

	n, err := repo.Count(ctx, problem.ListFilter{})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("collection holds %d problems, want only the rival's 1", n)
	}
}
