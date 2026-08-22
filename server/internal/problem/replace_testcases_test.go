package problem_test

import (
	"context"
	"testing"

	"github.com/toji339/online-judge/internal/problem"
	"github.com/toji339/online-judge/internal/problem/problemtest"
)

// Seeded test data used to be write-once: the seeder skipped any problem
// that already had cases, and the admin API had no update or delete for
// them, so a wrong expected output could not be corrected through any
// code path. ReplaceTestCases is that path.

func TestService_ReplaceTestCases_SwapsTheWholeSet(t *testing.T) {
	svc := problem.NewService(problemtest.NewFakeRepository())
	ctx := context.Background()

	p, err := svc.Create(ctx, validInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	original := &problem.TestCase{ProblemID: p.ID, Input: "4 8\n1 5 3 7", ExpectedOutput: "0 3"}
	if err := svc.AddTestCase(ctx, original); err != nil {
		t.Fatalf("add: %v", err)
	}

	replacement := []problem.TestCase{
		{Input: "4 8\n1 5 2 7", ExpectedOutput: "0 3", IsSample: false},
		{Input: "2 6\n3 3", ExpectedOutput: "0 1", IsSample: true},
	}
	if err := svc.ReplaceTestCases(ctx, p.ID, replacement); err != nil {
		t.Fatalf("replace: %v", err)
	}

	stored, err := svc.ListAllTestCases(ctx, p.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("stored %d test cases, want 2", len(stored))
	}
	for _, tc := range stored {
		if tc.Input == "4 8\n1 5 3 7" {
			t.Error("the old test case survived the replacement")
		}
		if tc.ProblemID != p.ID {
			t.Errorf("problemID = %q, want %q", tc.ProblemID, p.ID)
		}
		if tc.ID == "" {
			t.Error("expected the replacement to be assigned an ID")
		}
	}
}

func TestService_ReplaceTestCases_LeavesOtherProblemsAlone(t *testing.T) {
	svc := problem.NewService(problemtest.NewFakeRepository())
	ctx := context.Background()

	first, err := svc.Create(ctx, validInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	other := validInput()
	other.Title = "Merge Intervals"
	second, err := svc.Create(ctx, other)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.AddTestCase(ctx, &problem.TestCase{ProblemID: second.ID, Input: "1", ExpectedOutput: "1"}); err != nil {
		t.Fatalf("add: %v", err)
	}

	if err := svc.ReplaceTestCases(ctx, first.ID, []problem.TestCase{{Input: "2", ExpectedOutput: "2"}}); err != nil {
		t.Fatalf("replace: %v", err)
	}

	stored, err := svc.ListAllTestCases(ctx, second.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("the other problem's test cases were disturbed: %+v", stored)
	}
}

func TestService_ReplaceTestCases_RejectsAnInvalidSetBeforeDeleting(t *testing.T) {
	svc := problem.NewService(problemtest.NewFakeRepository())
	ctx := context.Background()

	p, err := svc.Create(ctx, validInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.AddTestCase(ctx, &problem.TestCase{ProblemID: p.ID, Input: "1", ExpectedOutput: "1"}); err != nil {
		t.Fatalf("add: %v", err)
	}

	// The second entry is invalid. Nothing may be deleted, or a typo in
	// new seed data would wipe the good data it was meant to fix.
	err = svc.ReplaceTestCases(ctx, p.ID, []problem.TestCase{
		{Input: "2", ExpectedOutput: "2"},
		{Input: "3", ExpectedOutput: "   "},
	})
	if err == nil {
		t.Fatal("expected a validation error")
	}

	stored, err := svc.ListAllTestCases(ctx, p.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(stored) != 1 || stored[0].Input != "1" {
		t.Fatalf("the existing test cases were disturbed: %+v", stored)
	}
}

func TestService_ReplaceTestCases_RejectsAnEmptySet(t *testing.T) {
	svc := problem.NewService(problemtest.NewFakeRepository())
	ctx := context.Background()

	p, err := svc.Create(ctx, validInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.AddTestCase(ctx, &problem.TestCase{ProblemID: p.ID, Input: "1", ExpectedOutput: "1"}); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Replacing with nothing would leave a problem that accepts anything.
	if err := svc.ReplaceTestCases(ctx, p.ID, nil); err == nil {
		t.Fatal("expected replacing with an empty set to be rejected")
	}

	stored, _ := svc.ListAllTestCases(ctx, p.ID)
	if len(stored) != 1 {
		t.Fatalf("stored %d test cases, want the original 1", len(stored))
	}
}
