package main

import (
	"testing"

	"github.com/toji339/online-judge/internal/problem"
)

// The seeder used to skip any problem that already had test cases, which
// is why a wrong expected output could not be corrected in a running
// deployment. Replacing them is now possible but must be asked for: the
// plan below is what decides, and the default must never be destructive.

func seedSet() []problem.TestCase {
	return []problem.TestCase{
		{Input: "4 8\n1 5 2 7", ExpectedOutput: "0 3", IsSample: false},
		{Input: "2 6\n3 3", ExpectedOutput: "0 1", IsSample: true},
	}
}

func storedSet() []problem.TestCase {
	return []problem.TestCase{
		{ID: "a", ProblemID: "p1", Input: "2 6\n3 3", ExpectedOutput: "0 1", IsSample: true},
		{ID: "b", ProblemID: "p1", Input: "4 8\n1 5 2 7", ExpectedOutput: "0 3", IsSample: false},
	}
}

func TestPlanTestCases_InsertsWhenNothingIsStored(t *testing.T) {
	plan := planTestCases("Two Sum", nil, seedSet(), false)
	if plan.Action != actionInsert {
		t.Errorf("action = %q, want %q", plan.Action, actionInsert)
	}
	if plan.Existing != 0 || plan.Desired != 2 {
		t.Errorf("counts = %d/%d, want 0/2", plan.Existing, plan.Desired)
	}
}

func TestPlanTestCases_SkipsWhenStoredDataAlreadyMatches(t *testing.T) {
	plan := planTestCases("Two Sum", storedSet(), seedSet(), false)
	if plan.Action != actionUpToDate {
		t.Errorf("action = %q, want %q", plan.Action, actionUpToDate)
	}
	if plan.Differs {
		t.Error("identical sets must not be reported as differing")
	}

	// Order is not part of the identity: ListTestCases makes no ordering
	// promise, so the same data read back in another order is the same data.
	if forced := planTestCases("Two Sum", storedSet(), seedSet(), true); forced.Action != actionUpToDate {
		t.Errorf("forced action = %q, want %q — matching data must not be rewritten", forced.Action, actionUpToDate)
	}
}

func TestPlanTestCases_DefaultIsNeverDestructive(t *testing.T) {
	stale := []problem.TestCase{
		{ID: "a", ProblemID: "p1", Input: "4 8\n1 5 3 7", ExpectedOutput: "0 3"},
	}

	plan := planTestCases("Two Sum", stale, seedSet(), false)
	if plan.Action != actionNeedsForce {
		t.Errorf("action = %q, want %q", plan.Action, actionNeedsForce)
	}
	if !plan.Differs {
		t.Error("expected the stored data to be reported as differing")
	}
	if plan.Existing != 1 || plan.Desired != 2 {
		t.Errorf("counts = %d/%d, want 1/2", plan.Existing, plan.Desired)
	}
}

func TestPlanTestCases_ReplacesOnlyWhenForced(t *testing.T) {
	stale := []problem.TestCase{
		{ID: "a", ProblemID: "p1", Input: "4 8\n1 5 3 7", ExpectedOutput: "0 3"},
	}

	plan := planTestCases("Two Sum", stale, seedSet(), true)
	if plan.Action != actionReplace {
		t.Errorf("action = %q, want %q", plan.Action, actionReplace)
	}
	if !plan.Differs {
		t.Error("expected the stored data to be reported as differing")
	}
}

func TestPlanTestCases_DetectsADifferentExpectedOutput(t *testing.T) {
	// Same inputs, one wrong expectation — the exact shape of the Two Sum
	// defect. Counting cases would miss it.
	stored := storedSet()
	stored[1].ExpectedOutput = "1 2"

	plan := planTestCases("Two Sum", stored, seedSet(), false)
	if !plan.Differs {
		t.Error("a changed expected output must be detected, not just a changed count")
	}
	if plan.Action != actionNeedsForce {
		t.Errorf("action = %q, want %q", plan.Action, actionNeedsForce)
	}
}

func TestPlanTestCases_DetectsAChangedSampleFlag(t *testing.T) {
	// A case flipped from hidden to sample leaks data that was meant to
	// stay hidden, so the flag is part of the identity.
	stored := storedSet()
	stored[0].IsSample = false

	if plan := planTestCases("Two Sum", stored, seedSet(), false); !plan.Differs {
		t.Error("a changed is_sample flag must be detected")
	}
}
