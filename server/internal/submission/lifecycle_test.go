package submission_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/submission"
	"github.com/toji339/online-judge/internal/submission/submissiontest"
)

// --- D3: conditional lifecycle transitions ---

func TestMarkRunning_ClaimsAPendingSubmissionOnce(t *testing.T) {
	svc := submission.NewService(submissiontest.New())
	ctx := context.Background()

	sub, err := svc.Create(ctx, validInput())
	require.NoError(t, err)

	require.NoError(t, svc.MarkRunning(ctx, sub.ID))

	// A redelivery arriving while the first worker is still judging must
	// be refused, not allowed to judge the same submission again.
	err = svc.MarkRunning(ctx, sub.ID)
	assert.ErrorIs(t, err, submission.ErrAlreadyClaimed)
}

func TestMarkRunning_ReclaimsAClaimAbandonedByADeadWorker(t *testing.T) {
	repo := submissiontest.New()
	svc := submission.NewService(repo)
	ctx := context.Background()

	sub, err := svc.Create(ctx, validInput())
	require.NoError(t, err)
	require.NoError(t, svc.MarkRunning(ctx, sub.ID))

	// Pretend the worker that claimed it died long ago.
	repo.SetStartedAt(sub.ID, time.Now().UTC().Add(-2*submission.StaleClaimAfter))

	assert.NoError(t, svc.MarkRunning(ctx, sub.ID),
		"a claim older than StaleClaimAfter may be taken over")
}

func TestMarkRunning_RefusesAnAlreadyJudgedSubmission(t *testing.T) {
	svc := submission.NewService(submissiontest.New())
	ctx := context.Background()

	sub, err := svc.Create(ctx, validInput())
	require.NoError(t, err)
	require.NoError(t, svc.MarkRunning(ctx, sub.ID))
	require.NoError(t, svc.MarkJudged(ctx, sub.ID, submission.Result{
		Status: submission.StatusAccepted, FailedCase: -1,
	}))

	assert.ErrorIs(t, svc.MarkRunning(ctx, sub.ID), submission.ErrAlreadyClaimed)
}

func TestMarkJudged_OnlyTheFirstVerdictIsRecorded(t *testing.T) {
	svc := submission.NewService(submissiontest.New())
	ctx := context.Background()

	sub, err := svc.Create(ctx, validInput())
	require.NoError(t, err)
	require.NoError(t, svc.MarkRunning(ctx, sub.ID))
	require.NoError(t, svc.MarkJudged(ctx, sub.ID, submission.Result{
		Status: submission.StatusAccepted, FailedCase: -1,
	}))

	stored, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	firstJudgedAt := *stored.JudgedAt

	// A second worker finishing the same submission must not flip the
	// verdict or restamp judged_at, which decides War Room winners.
	err = svc.MarkJudged(ctx, sub.ID, submission.Result{
		Status: submission.StatusWrongAnswer, FailedCase: 2,
	})
	assert.ErrorIs(t, err, submission.ErrAlreadyJudged)

	stored, err = svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, submission.StatusAccepted, stored.Status)
	assert.Equal(t, firstJudgedAt, *stored.JudgedAt)
}

func TestMarkJudged_RefusesASubmissionThatWasNeverClaimed(t *testing.T) {
	svc := submission.NewService(submissiontest.New())
	ctx := context.Background()

	sub, err := svc.Create(ctx, validInput())
	require.NoError(t, err)

	assert.ErrorIs(t, svc.MarkJudged(ctx, sub.ID, submission.Result{
		Status: submission.StatusAccepted, FailedCase: -1,
	}), submission.ErrAlreadyJudged)
}

func TestMarkJudged_ConcurrentWritersProduceExactlyOneVerdict(t *testing.T) {
	svc := submission.NewService(submissiontest.New())
	ctx := context.Background()

	sub, err := svc.Create(ctx, validInput())
	require.NoError(t, err)
	require.NoError(t, svc.MarkRunning(ctx, sub.ID))

	const writers = 8
	var wg sync.WaitGroup
	results := make([]error, writers)
	for i := range writers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = svc.MarkJudged(ctx, sub.ID, submission.Result{
				Status: submission.StatusAccepted, FailedCase: -1,
			})
		}(i)
	}
	wg.Wait()

	won := 0
	for _, err := range results {
		if err == nil {
			won++
			continue
		}
		assert.ErrorIs(t, err, submission.ErrAlreadyJudged)
	}
	assert.Equal(t, 1, won, "exactly one writer records the verdict")
}

// --- D13: infrastructure failures get an honest status ---

func TestMarkFailed_UsesTheNonUserBlamingStatus(t *testing.T) {
	svc := submission.NewService(submissiontest.New())
	ctx := context.Background()

	sub, err := svc.Create(ctx, validInput())
	require.NoError(t, err)
	require.NoError(t, svc.MarkFailed(ctx, sub.ID, "execution engine error"))

	stored, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, submission.StatusJudgeError, stored.Status,
		"a judge failure is not the user's runtime error")
	assert.True(t, stored.Status.IsTerminal(), "the pending slot is released")
	assert.Equal(t, "execution engine error", stored.CompileError)
}

func TestMarkFailed_NeverOverwritesARecordedVerdict(t *testing.T) {
	svc := submission.NewService(submissiontest.New())
	ctx := context.Background()

	sub, err := svc.Create(ctx, validInput())
	require.NoError(t, err)
	require.NoError(t, svc.MarkRunning(ctx, sub.ID))
	require.NoError(t, svc.MarkJudged(ctx, sub.ID, submission.Result{
		Status: submission.StatusAccepted, FailedCase: -1,
	}))

	assert.ErrorIs(t, svc.MarkFailed(ctx, sub.ID, "engine error"), submission.ErrAlreadyJudged)

	stored, _ := svc.GetByID(ctx, sub.ID)
	assert.Equal(t, submission.StatusAccepted, stored.Status)
}

// --- D14: stored compile output is capped ---

func TestMarkJudged_TruncatesAPathologicalCompileError(t *testing.T) {
	svc := submission.NewService(submissiontest.New())
	ctx := context.Background()

	sub, err := svc.Create(ctx, validInput())
	require.NoError(t, err)
	require.NoError(t, svc.MarkRunning(ctx, sub.ID))
	require.NoError(t, svc.MarkJudged(ctx, sub.ID, submission.Result{
		Status:       submission.StatusCompileError,
		FailedCase:   -1,
		CompileError: strings.Repeat("e", 1<<20),
	}))

	stored, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(stored.CompileError), submission.MaxCompileErrorBytes+64,
		"a 1MiB compile error is not persisted verbatim")
	assert.Contains(t, stored.CompileError, "truncated")
}

func TestMarkJudged_LeavesAShortCompileErrorAlone(t *testing.T) {
	svc := submission.NewService(submissiontest.New())
	ctx := context.Background()

	sub, err := svc.Create(ctx, validInput())
	require.NoError(t, err)
	require.NoError(t, svc.MarkRunning(ctx, sub.ID))
	require.NoError(t, svc.MarkJudged(ctx, sub.ID, submission.Result{
		Status: submission.StatusCompileError, FailedCase: -1, CompileError: "syntax error",
	}))

	stored, _ := svc.GetByID(ctx, sub.ID)
	assert.Equal(t, "syntax error", stored.CompileError)
}

// --- D2: the reaper reclaims submissions nothing will ever finish ---

func TestExpireStale_ReleasesTheSlotHeldByAStuckPendingSubmission(t *testing.T) {
	repo := submissiontest.New()
	svc := submission.NewService(repo)
	ctx := context.Background()

	sub, err := svc.Create(ctx, validInput())
	require.NoError(t, err)
	repo.SetSubmittedAt(sub.ID, time.Now().UTC().Add(-2*submission.PendingTTL))

	n, err := svc.ExpireStale(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	stored, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, submission.StatusJudgeError, stored.Status)
	assert.True(t, stored.Status.IsTerminal())
	require.NotNil(t, stored.JudgedAt)

	// The whole point: the user can submit again.
	_, err = svc.Create(ctx, validInput())
	assert.NoError(t, err)
}

func TestExpireStale_ReclaimsARunningSubmissionNoWorkerOwns(t *testing.T) {
	repo := submissiontest.New()
	svc := submission.NewService(repo)
	ctx := context.Background()

	sub, err := svc.Create(ctx, validInput())
	require.NoError(t, err)
	require.NoError(t, svc.MarkRunning(ctx, sub.ID))
	repo.SetStartedAt(sub.ID, time.Now().UTC().Add(-2*submission.RunningTTL))

	n, err := svc.ExpireStale(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	stored, _ := svc.GetByID(ctx, sub.ID)
	assert.Equal(t, submission.StatusJudgeError, stored.Status)
	assert.NotEmpty(t, stored.CompileError, "the record says why, without blaming the user")
}

func TestExpireStale_LeavesFreshWorkAlone(t *testing.T) {
	svc := submission.NewService(submissiontest.New())
	ctx := context.Background()

	pending, err := svc.Create(ctx, validInput())
	require.NoError(t, err)

	running := validInput()
	running.UserID = "user-running"
	runningSub, err := svc.Create(ctx, running)
	require.NoError(t, err)
	require.NoError(t, svc.MarkRunning(ctx, runningSub.ID))

	n, err := svc.ExpireStale(ctx)
	require.NoError(t, err)
	assert.Zero(t, n, "a submission still being judged is never reclaimed")

	stored, _ := svc.GetByID(ctx, pending.ID)
	assert.Equal(t, submission.StatusPending, stored.Status)
	stored, _ = svc.GetByID(ctx, runningSub.ID)
	assert.Equal(t, submission.StatusRunning, stored.Status)
}

func TestExpireStale_NeverTouchesAJudgedSubmission(t *testing.T) {
	repo := submissiontest.New()
	svc := submission.NewService(repo)
	ctx := context.Background()

	sub, err := svc.Create(ctx, validInput())
	require.NoError(t, err)
	require.NoError(t, svc.MarkRunning(ctx, sub.ID))
	require.NoError(t, svc.MarkJudged(ctx, sub.ID, submission.Result{
		Status: submission.StatusAccepted, FailedCase: -1,
	}))
	repo.SetSubmittedAt(sub.ID, time.Now().UTC().Add(-30*24*time.Hour))

	n, err := svc.ExpireStale(ctx)
	require.NoError(t, err)
	assert.Zero(t, n)

	stored, _ := svc.GetByID(ctx, sub.ID)
	assert.Equal(t, submission.StatusAccepted, stored.Status)
}
