package companytag_test

import (
	"context"
	"errors"
	"testing"

	"github.com/toji339/online-judge/internal/companytag"
	"github.com/toji339/online-judge/internal/companytag/companytagtest"
)

// Tagging writes to two collections: the authoritative report, and the
// denormalised per-problem summary. They cannot be one write, so the
// pair has to be recoverable — and it was not. A summary write that
// failed left the report behind, and the unique index then rejected the
// user's retry, so the count stayed wrong with no way back through the
// API.

func TestTag_SummaryFailureLeavesNothingBehind(t *testing.T) {
	repo := companytagtest.New()
	svc := companytag.NewService(repo)
	ctx := context.Background()

	repo.FailIncrementSummary = errors.New("mongo is down")

	if _, err := svc.Tag(ctx, companytag.TagInput{
		ProblemID: "p1", UserID: "u1", Company: "Google",
	}); err == nil {
		t.Fatal("expected the tag to fail")
	}

	// The report must not survive a failed tag, or the unique index would
	// reject the retry that is meant to fix it.
	tags, err := svc.ListUserTags(ctx, "p1", "u1")
	if err != nil {
		t.Fatalf("list user tags: %v", err)
	}
	if len(tags) != 0 {
		t.Errorf("the failed tag left %d report(s) behind", len(tags))
	}
}

func TestTag_RetryAfterASummaryFailureSucceeds(t *testing.T) {
	repo := companytagtest.New()
	svc := companytag.NewService(repo)
	ctx := context.Background()

	repo.FailIncrementSummary = errors.New("mongo is down")
	if _, err := svc.Tag(ctx, companytag.TagInput{
		ProblemID: "p1", UserID: "u1", Company: "Google",
	}); err == nil {
		t.Fatal("expected the tag to fail")
	}

	repo.FailIncrementSummary = nil
	if _, err := svc.Tag(ctx, companytag.TagInput{
		ProblemID: "p1", UserID: "u1", Company: "Google",
	}); err != nil {
		t.Fatalf("retry: %v", err)
	}

	if got := repo.SummaryCount("p1", "Google"); got != 1 {
		t.Errorf("summary count = %d, want 1", got)
	}
	tags, _ := svc.ListUserTags(ctx, "p1", "u1")
	if len(tags) != 1 {
		t.Errorf("stored %d report(s), want 1", len(tags))
	}
}

// TestTag_UnrepairableFailureIsReported covers the case where even the
// compensation fails: the caller must be told the summary is stale
// rather than being handed a plain "try again".
func TestTag_UnrepairableFailureIsReported(t *testing.T) {
	repo := companytagtest.New()
	svc := companytag.NewService(repo)
	ctx := context.Background()

	repo.FailIncrementSummary = errors.New("mongo is down")
	repo.FailRemove = errors.New("still down")

	_, err := svc.Tag(ctx, companytag.TagInput{
		ProblemID: "p1", UserID: "u1", Company: "Google",
	})
	if err == nil {
		t.Fatal("expected the tag to fail")
	}
	if !errors.Is(err, companytag.ErrSummaryOutOfStep) {
		t.Errorf("err = %v, want it to wrap ErrSummaryOutOfStep", err)
	}
}

// TestTag_RepairsADriftedSummary is the reconciliation path. A crash
// between the two writes leaves a report with no matching increment, and
// the user who made it can never retry. Anyone tagging that company
// again puts the count back, because the count is recomputed from the
// reports, which are the authority.
func TestTag_RepairsADriftedSummary(t *testing.T) {
	repo := companytagtest.New()
	svc := companytag.NewService(repo)
	ctx := context.Background()

	if _, err := svc.Tag(ctx, companytag.TagInput{
		ProblemID: "p1", UserID: "u1", Company: "Google",
	}); err != nil {
		t.Fatalf("tag: %v", err)
	}
	// Reproduce a crash between the report and the summary write: the
	// report is there, the count is not.
	repo.SetSummaryCount("p1", "Google", 0)

	_, err := svc.Tag(ctx, companytag.TagInput{
		ProblemID: "p1", UserID: "u1", Company: "Google",
	})
	if !errors.Is(err, companytag.ErrAlreadyTagged) {
		t.Fatalf("err = %v, want ErrAlreadyTagged", err)
	}

	if got := repo.SummaryCount("p1", "Google"); got != 1 {
		t.Errorf("summary count = %d, want the drift repaired to 1", got)
	}
}

func TestTag_ADuplicateStillDoesNotInflateTheCount(t *testing.T) {
	repo := companytagtest.New()
	svc := companytag.NewService(repo)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, err := svc.Tag(ctx, companytag.TagInput{
			ProblemID: "p1", UserID: "u1", Company: "Google",
		})
		if i > 0 && !errors.Is(err, companytag.ErrAlreadyTagged) {
			t.Fatalf("tag %d: err = %v, want ErrAlreadyTagged", i, err)
		}
	}

	if got := repo.SummaryCount("p1", "Google"); got != 1 {
		t.Errorf("summary count = %d, want 1", got)
	}
}
