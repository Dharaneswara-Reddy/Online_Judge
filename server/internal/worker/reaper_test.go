package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/submission"
	"github.com/toji339/online-judge/internal/submission/submissiontest"
	"github.com/toji339/online-judge/internal/worker"
)

// reaperFixture builds a submission service over the in-memory fake.
func reaperFixture(t *testing.T) (*submission.Service, *submissiontest.FakeRepository) {
	t.Helper()
	repo := submissiontest.New()
	return submission.NewService(repo), repo
}

func stale(t *testing.T, svc *submission.Service, repo *submissiontest.FakeRepository, userID string, age time.Duration) string {
	t.Helper()
	sub, err := svc.Create(context.Background(), submission.CreateInput{
		UserID: userID, ProblemID: "problem-1", Language: "python", Code: "print(3)",
	})
	require.NoError(t, err)
	repo.SetSubmittedAt(sub.ID, time.Now().UTC().Add(-age))
	return sub.ID
}

// TestReaper_ReclaimsAStuckSubmission is the whole point: a submission
// whose judging never reported back holds the user's only in-flight slot
// under the partial unique index, and until now nothing ever released it.
func TestReaper_ReclaimsAStuckSubmission(t *testing.T) {
	svc, repo := reaperFixture(t)
	ctx := context.Background()
	id := stale(t, svc, repo, "user-1", time.Hour)

	reaped, err := worker.NewReaper(svc, 15*time.Minute, time.Minute).Sweep(ctx)

	require.NoError(t, err)
	assert.Equal(t, 1, reaped)

	stored, err := svc.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, submission.StatusError, stored.Status,
		"an honest status: the judge never reached a verdict")
	require.NotNil(t, stored.JudgedAt, "a terminal record is stamped server-side")

	_, err = svc.Create(ctx, submission.CreateInput{
		UserID: "user-1", ProblemID: "problem-1", Language: "python", Code: "print(3)",
	})
	assert.NoError(t, err, "the user's admission-control slot is released")
}

func TestReaper_LeavesRecentWorkAlone(t *testing.T) {
	svc, repo := reaperFixture(t)
	ctx := context.Background()
	id := stale(t, svc, repo, "user-1", time.Minute)

	reaped, err := worker.NewReaper(svc, 15*time.Minute, time.Minute).Sweep(ctx)

	require.NoError(t, err)
	assert.Zero(t, reaped, "a submission still being judged is not stuck")
	stored, _ := svc.GetByID(ctx, id)
	assert.Equal(t, submission.StatusPending, stored.Status)
}

// TestReaper_NeverOverwritesAVerdict guards the server-authoritative
// rule: the sweep is a conditional write over non-terminal rows only, so
// it cannot race a worker into clobbering a real result.
func TestReaper_NeverOverwritesAVerdict(t *testing.T) {
	svc, repo := reaperFixture(t)
	ctx := context.Background()
	id := stale(t, svc, repo, "user-1", time.Hour)
	require.NoError(t, svc.MarkJudged(ctx, id, submission.Result{
		Status: submission.StatusAccepted, RuntimeMS: 12, FailedCase: -1, TotalCases: 3,
	}))

	reaped, err := worker.NewReaper(svc, 15*time.Minute, time.Minute).Sweep(ctx)

	require.NoError(t, err)
	assert.Zero(t, reaped)
	stored, _ := svc.GetByID(ctx, id)
	assert.Equal(t, submission.StatusAccepted, stored.Status)
	assert.Equal(t, int64(12), stored.RuntimeMS)
}

// TestReaper_RunSweepsOnATickerAndStops covers the loop the worker
// process starts: it works on a schedule and shuts down with its context.
func TestReaper_RunSweepsOnATickerAndStops(t *testing.T) {
	svc, repo := reaperFixture(t)
	id := stale(t, svc, repo, "user-1", time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.NewReaper(svc, 15*time.Minute, 10*time.Millisecond).Run(ctx)
		close(done)
	}()

	require.Eventually(t, func() bool {
		stored, err := svc.GetByID(context.Background(), id)
		return err == nil && stored.Status == submission.StatusError
	}, 2*time.Second, 10*time.Millisecond, "the ticker sweeps without being asked")

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run must return when its context is cancelled")
	}
}
