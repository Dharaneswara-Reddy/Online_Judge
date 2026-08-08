package problem_test

import (
	"context"
	"errors"
	"testing"

	"github.com/toji339/online-judge/internal/problem"
	"github.com/toji339/online-judge/internal/problem/problemtest"
)

func validInput() problem.CreateProblemInput {
	return problem.CreateProblemInput{
		Title:         "Two Sum",
		Statement:     "Given an array of integers...",
		Difficulty:    problem.DifficultyEasy,
		Tags:          []string{"arrays", "hash-table"},
		TimeLimitMS:   2000,
		MemoryLimitMB: 256,
	}
}

func TestService_Create_HappyPath(t *testing.T) {
	svc := problem.NewService(problemtest.NewFakeRepository())
	p, err := svc.Create(context.Background(), validInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.ID == "" {
		t.Fatal("expected ID to be set")
	}
	if p.Slug != "two-sum" {
		t.Errorf("slug = %q, want %q", p.Slug, "two-sum")
	}
	if p.Title != "Two Sum" {
		t.Errorf("title = %q, want %q", p.Title, "Two Sum")
	}
}

func TestService_Create_DuplicateTitle_IncrementsSuffix(t *testing.T) {
	svc := problem.NewService(problemtest.NewFakeRepository())
	_, err := svc.Create(context.Background(), validInput())
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	p2, err := svc.Create(context.Background(), validInput())
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if p2.Slug != "two-sum-2" {
		t.Errorf("slug = %q, want %q", p2.Slug, "two-sum-2")
	}
}

func TestService_Create_MissingTitle(t *testing.T) {
	svc := problem.NewService(problemtest.NewFakeRepository())
	input := validInput()
	input.Title = "   "
	_, err := svc.Create(context.Background(), input)

	var ve problem.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	if ve.Field != "title" {
		t.Errorf("field = %q, want %q", ve.Field, "title")
	}
}

func TestService_Create_InvalidDifficulty(t *testing.T) {
	svc := problem.NewService(problemtest.NewFakeRepository())
	input := validInput()
	input.Difficulty = "insane"
	_, err := svc.Create(context.Background(), input)

	var ve problem.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	if ve.Field != "difficulty" {
		t.Errorf("field = %q, want %q", ve.Field, "difficulty")
	}
}

func TestService_Create_ZeroTimeLimit(t *testing.T) {
	svc := problem.NewService(problemtest.NewFakeRepository())
	input := validInput()
	input.TimeLimitMS = 0
	_, err := svc.Create(context.Background(), input)

	var ve problem.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	if ve.Field != "timeLimitMs" {
		t.Errorf("field = %q, want %q", ve.Field, "timeLimitMs")
	}
}

func TestService_Update_HappyPath(t *testing.T) {
	repo := problemtest.NewFakeRepository()
	svc := problem.NewService(repo)

	p, err := svc.Create(context.Background(), validInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	input := validInput()
	input.Title = "Three Sum"
	updated, err := svc.Update(context.Background(), p.ID, input)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Title != "Three Sum" {
		t.Errorf("title = %q, want %q", updated.Title, "Three Sum")
	}
	// Slug should NOT change on update — it was set at creation.
	if updated.Slug != "two-sum" {
		t.Errorf("slug changed unexpectedly to %q", updated.Slug)
	}
}

func TestService_AddTestCase_EmptyExpectedOutput(t *testing.T) {
	svc := problem.NewService(problemtest.NewFakeRepository())
	err := svc.AddTestCase(context.Background(), &problem.TestCase{
		ProblemID:      "fake-1",
		Input:          "1 2",
		ExpectedOutput: "   ",
	})

	var ve problem.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	if ve.Field != "expectedOutput" {
		t.Errorf("field = %q, want %q", ve.Field, "expectedOutput")
	}
}

func TestService_ListPublicTestCases_OnlySamples(t *testing.T) {
	repo := problemtest.NewFakeRepository()
	svc := problem.NewService(repo)
	ctx := context.Background()

	_ = svc.AddTestCase(ctx, &problem.TestCase{ProblemID: "p1", Input: "1", ExpectedOutput: "2", IsSample: true})
	_ = svc.AddTestCase(ctx, &problem.TestCase{ProblemID: "p1", Input: "3", ExpectedOutput: "4", IsSample: false})
	_ = svc.AddTestCase(ctx, &problem.TestCase{ProblemID: "p1", Input: "5", ExpectedOutput: "6", IsSample: true})

	tcs, err := svc.ListPublicTestCases(ctx, "p1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tcs) != 2 {
		t.Fatalf("got %d test cases, want 2", len(tcs))
	}
	for _, tc := range tcs {
		if !tc.IsSample {
			t.Errorf("got non-sample test case %q in public list", tc.ID)
		}
	}
}

func TestService_ListAllTestCases_IncludesHidden(t *testing.T) {
	repo := problemtest.NewFakeRepository()
	svc := problem.NewService(repo)
	ctx := context.Background()

	_ = svc.AddTestCase(ctx, &problem.TestCase{ProblemID: "p1", Input: "1", ExpectedOutput: "2", IsSample: true})
	_ = svc.AddTestCase(ctx, &problem.TestCase{ProblemID: "p1", Input: "3", ExpectedOutput: "4", IsSample: false})

	tcs, err := svc.ListAllTestCases(ctx, "p1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tcs) != 2 {
		t.Fatalf("got %d test cases, want 2", len(tcs))
	}
}

func TestService_GetBySlug_NotFound(t *testing.T) {
	svc := problem.NewService(problemtest.NewFakeRepository())
	_, err := svc.GetBySlug(context.Background(), "nonexistent")
	if !errors.Is(err, problem.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
