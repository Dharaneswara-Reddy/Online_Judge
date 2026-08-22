package worker_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/judge"
	"github.com/toji339/online-judge/internal/queue"
	"github.com/toji339/online-judge/internal/submission"
)

// assertErr stands in for any infrastructure failure.
var assertErr = errors.New("infrastructure is down")

// blockingSandbox holds every submission inside Run until released, so a
// redelivery can be made to arrive while the first judge is mid-flight —
// which is exactly the window the unconditional writes left open.
type blockingSandbox struct {
	entered chan struct{}
	release chan struct{}

	mu    sync.Mutex
	calls int
	out   string
}

func newBlockingSandbox(out string) *blockingSandbox {
	return &blockingSandbox{
		entered: make(chan struct{}, 8),
		release: make(chan struct{}),
		out:     out,
	}
}

func (s *blockingSandbox) NewSubmission(context.Context, string, string, judge.Limits) (judge.SubmissionSandbox, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return &blockingSubmission{parent: s}, nil
}

func (s *blockingSandbox) sandboxCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type blockingSubmission struct{ parent *blockingSandbox }

func (s *blockingSubmission) Compile(context.Context) (judge.ExecuteResult, error) {
	return judge.ExecuteResult{}, nil
}

func (s *blockingSubmission) Run(ctx context.Context, _ string) (judge.ExecuteResult, error) {
	s.parent.entered <- struct{}{}
	select {
	case <-s.parent.release:
	case <-ctx.Done():
		return judge.ExecuteResult{}, ctx.Err()
	}
	return judge.ExecuteResult{Stdout: s.parent.out}, nil
}

func (s *blockingSubmission) Close(context.Context) error { return nil }

// --- D3: a redelivery arriving mid-judge ---

func TestProcess_RedeliveryDuringJudgingDoesNotJudgeTwice(t *testing.T) {
	sandbox := newBlockingSandbox("3\n")
	f := newFixture(t, sandbox)
	id, job := f.enqueue(t, "user-1")

	first := make(chan error, 1)
	go func() { first <- f.processor.Process(context.Background(), job) }()

	// Wait until the first worker is inside the sandbox, i.e. it has
	// claimed the submission and is judging it.
	select {
	case <-sandbox.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first worker never reached the sandbox")
	}

	// The broker redelivers the same job to a second worker.
	require.NoError(t, f.processor.Process(context.Background(), job),
		"a redelivery is acknowledged, not retried forever")
	assert.Equal(t, 1, sandbox.sandboxCalls(),
		"the second worker abandons the job instead of starting a container")

	close(sandbox.release)
	require.NoError(t, <-first)

	assert.Len(t, f.notifier.judged, 1, "the verdict is broadcast exactly once")

	stored, err := f.subs.GetByID(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, submission.StatusAccepted, stored.Status)
}

func TestProcess_LosingWorkerDiscardsItsVerdict(t *testing.T) {
	// Both workers judge (as they would if the first one's claim had gone
	// stale), but only one verdict may reach the database.
	f := newFixture(t, &stubSandbox{run: judge.ExecuteResult{Stdout: "3\n"}})
	id, job := f.enqueue(t, "user-1")

	require.NoError(t, f.processor.Process(context.Background(), job))
	before, err := f.subs.GetByID(context.Background(), id)
	require.NoError(t, err)

	require.NoError(t, f.processor.Process(context.Background(), job))

	after, err := f.subs.GetByID(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, *before.JudgedAt, *after.JudgedAt,
		"judged_at is never restamped — War Room winners are decided by it")
}

func TestProcess_ReclaimsASubmissionAbandonedByADeadWorker(t *testing.T) {
	f := newFixture(t, &stubSandbox{run: judge.ExecuteResult{Stdout: "3\n"}})
	id, job := f.enqueue(t, "user-1")

	// A worker claimed it and then died without ever finishing.
	require.NoError(t, f.subs.MarkRunning(context.Background(), id))
	f.repo.SetStartedAt(id, time.Now().UTC().Add(-2*submission.StaleClaimAfter))

	require.NoError(t, f.processor.Process(context.Background(), job))

	stored, _ := f.subs.GetByID(context.Background(), id)
	assert.Equal(t, submission.StatusAccepted, stored.Status,
		"a redelivery still rescues work a dead worker abandoned")
}

// --- D2: a load failure must not leave the slot held ---

func TestProcess_MissingSubmissionIsAcknowledgedNotRetried(t *testing.T) {
	f := newFixture(t, &stubSandbox{})

	err := f.processor.Process(context.Background(), queue.Job{SubmissionID: "missing"})

	assert.NoError(t, err,
		"there is no record to fix, so the message is dropped rather than looped")
}

func TestProcess_TransientLoadFailureIsReportedForRetry(t *testing.T) {
	f := newFixture(t, &stubSandbox{})
	id, job := f.enqueue(t, "user-1")
	f.repo.FailNextGet(assertErr)

	err := f.processor.Process(context.Background(), job)

	assert.Error(t, err, "a transient failure is reported so the consumer can retry")
	stored, getErr := f.subs.GetByID(context.Background(), id)
	require.NoError(t, getErr)
	assert.Equal(t, submission.StatusPending, stored.Status,
		"nothing was written, so the reaper is what releases the slot")
}

// --- D13: an infrastructure failure is not the user's runtime error ---

func TestProcess_SandboxFailureIsRecordedAsAJudgeError(t *testing.T) {
	f := newFixture(t, &stubSandbox{err: assertErr})
	id, job := f.enqueue(t, "user-1")

	require.Error(t, f.processor.Process(context.Background(), job))

	stored, _ := f.subs.GetByID(context.Background(), id)
	assert.Equal(t, submission.StatusJudgeError, stored.Status,
		"the judge failed; the user's program is not accused of anything")
}
