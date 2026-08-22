package worker_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/judge"
	"github.com/toji339/online-judge/internal/problem"
	"github.com/toji339/online-judge/internal/problem/problemtest"
	"github.com/toji339/online-judge/internal/queue"
	"github.com/toji339/online-judge/internal/submission"
	"github.com/toji339/online-judge/internal/submission/submissiontest"
	"github.com/toji339/online-judge/internal/worker"
)

// --- Test doubles ---

type stubSandbox struct {
	compile judge.ExecuteResult
	run     judge.ExecuteResult
	err     error
	calls   int
}

func (s *stubSandbox) NewSubmission(context.Context, string, string, judge.Limits) (judge.SubmissionSandbox, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.calls++
	return &stubSubmission{parent: s}, nil
}

type stubSubmission struct{ parent *stubSandbox }

func (s *stubSubmission) Compile(context.Context) (judge.ExecuteResult, error) {
	return s.parent.compile, nil
}
func (s *stubSubmission) Run(context.Context, string) (judge.ExecuteResult, error) {
	return s.parent.run, nil
}
func (s *stubSubmission) Close(context.Context) error { return nil }

type recordingNotifier struct{ judged []*submission.Submission }

func (r *recordingNotifier) SubmissionJudged(_ context.Context, sub *submission.Submission) {
	r.judged = append(r.judged, sub)
}

// --- Fixture ---

type fixture struct {
	processor *worker.Processor
	subs      *submission.Service
	repo      *submissiontest.FakeRepository
	problems  *problem.Service
	notifier  *recordingNotifier
	problemID string
}

// newFixture builds a processor over in-memory repositories with one
// problem whose single test case expects "3".
func newFixture(t *testing.T, sandbox judge.Sandbox) *fixture {
	t.Helper()
	ctx := context.Background()

	problems := problem.NewService(problemtest.NewFakeRepository())
	prob, err := problems.Create(ctx, problem.CreateProblemInput{
		Title: "Sum", Statement: "add them", Difficulty: problem.DifficultyEasy,
		TimeLimitMS: 1000, MemoryLimitMB: 64,
	})
	require.NoError(t, err)
	require.NoError(t, problems.AddTestCase(ctx, &problem.TestCase{
		ProblemID: prob.ID, Input: "1 2\n", ExpectedOutput: "3\n",
	}))

	repo := submissiontest.New()
	subs := submission.NewService(repo)
	notifier := &recordingNotifier{}

	return &fixture{
		processor: worker.NewProcessor(subs, problems, sandbox, notifier),
		subs:      subs,
		repo:      repo,
		problems:  problems,
		notifier:  notifier,
		problemID: prob.ID,
	}
}

// enqueue creates a pending submission and returns the matching job.
func (f *fixture) enqueue(t *testing.T, userID string) (string, queue.Job) {
	t.Helper()
	sub, err := f.subs.Create(context.Background(), submission.CreateInput{
		UserID: userID, ProblemID: f.problemID, ProblemSlug: "sum",
		Language: "python", Code: "print(3)",
	})
	require.NoError(t, err)
	return sub.ID, queue.Job{SubmissionID: sub.ID, UserID: userID, ProblemID: f.problemID}
}

// --- Happy path ---

func TestProcess_AcceptsCorrectSolution(t *testing.T) {
	f := newFixture(t, &stubSandbox{run: judge.ExecuteResult{Stdout: "3\n", RuntimeMS: 11}})
	id, job := f.enqueue(t, "user-1")

	require.NoError(t, f.processor.Process(context.Background(), job))

	stored, err := f.subs.GetByID(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, submission.StatusAccepted, stored.Status)
	assert.Equal(t, int64(11), stored.RuntimeMS)
	require.NotNil(t, stored.JudgedAt)
}

func TestProcess_NotifiesListenersWithTheJudgedRecord(t *testing.T) {
	f := newFixture(t, &stubSandbox{run: judge.ExecuteResult{Stdout: "3\n"}})
	id, job := f.enqueue(t, "user-1")

	require.NoError(t, f.processor.Process(context.Background(), job))

	require.Len(t, f.notifier.judged, 1, "listeners are told exactly once")
	assert.Equal(t, id, f.notifier.judged[0].ID)
	assert.Equal(t, submission.StatusAccepted, f.notifier.judged[0].Status,
		"the notified record already carries its final verdict")
}

func TestProcess_RecordsWrongAnswer(t *testing.T) {
	f := newFixture(t, &stubSandbox{run: judge.ExecuteResult{Stdout: "4\n"}})
	id, job := f.enqueue(t, "user-1")

	require.NoError(t, f.processor.Process(context.Background(), job))

	stored, _ := f.subs.GetByID(context.Background(), id)
	assert.Equal(t, submission.StatusWrongAnswer, stored.Status)
	assert.Equal(t, 0, stored.FailedCase)
}

func TestProcess_RecordsCompileError(t *testing.T) {
	f := newFixture(t, &stubSandbox{compile: judge.ExecuteResult{ExitCode: 1, Stderr: "syntax error"}})
	id, job := f.enqueue(t, "user-1")

	require.NoError(t, f.processor.Process(context.Background(), job))

	stored, _ := f.subs.GetByID(context.Background(), id)
	assert.Equal(t, submission.StatusCompileError, stored.Status)
	assert.Contains(t, stored.CompileError, "syntax error")
}

// --- Idempotency and failure handling ---

func TestProcess_SkipsAlreadyJudgedSubmission(t *testing.T) {
	sandbox := &stubSandbox{run: judge.ExecuteResult{Stdout: "3\n"}}
	f := newFixture(t, sandbox)
	id, job := f.enqueue(t, "user-1")
	require.NoError(t, f.processor.Process(context.Background(), job))
	require.Equal(t, 1, sandbox.calls)

	// A redelivered message must not judge the submission a second time.
	require.NoError(t, f.processor.Process(context.Background(), job))

	assert.Equal(t, 1, sandbox.calls, "the sandbox is not entered again")
	assert.Len(t, f.notifier.judged, 1, "listeners are not notified twice")
	stored, _ := f.subs.GetByID(context.Background(), id)
	assert.Equal(t, submission.StatusAccepted, stored.Status)
	_ = id
}

func TestProcess_SandboxFailureLeavesNoPendingSubmission(t *testing.T) {
	f := newFixture(t, &stubSandbox{err: errors.New("docker is down")})
	id, job := f.enqueue(t, "user-1")

	err := f.processor.Process(context.Background(), job)

	assert.Error(t, err, "the job is reported as failed to the consumer")
	stored, _ := f.subs.GetByID(context.Background(), id)
	assert.True(t, stored.Status.IsTerminal(), "the user never sees a stuck pending submission")
	assert.Len(t, f.notifier.judged, 1, "listeners still learn the attempt is over")
}

// TestProcess_UnknownSubmissionIsAcknowledged: a submission that does not
// exist will not exist on a retry either, so the message is dropped
// rather than cycling through the queue forever.
func TestProcess_UnknownSubmissionIsAcknowledged(t *testing.T) {
	f := newFixture(t, &stubSandbox{})

	err := f.processor.Process(context.Background(), queue.Job{SubmissionID: "missing"})

	assert.NoError(t, err)
}

// TestProcess_TransientLoadFailureIsRetryable is the stuck-submission
// defect. The very first step can fail before any status is written; the
// consumer then discarded the message, leaving the row pending forever
// and the user's single in-flight slot held with it.
func TestProcess_TransientLoadFailureIsRetryable(t *testing.T) {
	repo := &flakyRepository{FakeRepository: submissiontest.New()}
	subs := submission.NewService(repo)
	problems := problem.NewService(problemtest.NewFakeRepository())
	processor := worker.NewProcessor(subs, problems, &stubSandbox{}, nil)

	ctx := context.Background()
	sub, err := subs.Create(ctx, submission.CreateInput{
		UserID: "user-1", ProblemID: "problem-1", Language: "python", Code: "print(3)",
	})
	require.NoError(t, err)

	repo.getErr = errors.New("connection refused")
	err = processor.Process(ctx, queue.Job{SubmissionID: sub.ID})

	require.Error(t, err,
		"the job must go back on the queue rather than being dropped: decideAck "+
			"requeues a failed delivery once, and the reaper is the backstop after that")

	repo.getErr = nil
	stored, err := subs.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, submission.StatusPending, stored.Status,
		"nothing was written, so the record is untouched and a retry judges it properly")
}

// flakyRepository makes the submission store fail on demand, standing in
// for a database that is briefly unreachable.
type flakyRepository struct {
	*submissiontest.FakeRepository
	getErr error
}

func (r *flakyRepository) GetByID(ctx context.Context, id string) (*submission.Submission, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	return r.FakeRepository.GetByID(ctx, id)
}

func TestProcess_ProblemWithoutTestCasesFailsTheSubmission(t *testing.T) {
	f := newFixture(t, &stubSandbox{run: judge.ExecuteResult{Stdout: "3\n"}})
	ctx := context.Background()

	empty, err := f.problems.Create(ctx, problem.CreateProblemInput{
		Title: "Empty", Statement: "x", Difficulty: problem.DifficultyEasy,
		TimeLimitMS: 1000, MemoryLimitMB: 64,
	})
	require.NoError(t, err)
	sub, err := f.subs.Create(ctx, submission.CreateInput{
		UserID: "user-9", ProblemID: empty.ID, Language: "python", Code: "print(3)",
	})
	require.NoError(t, err)

	require.NoError(t, f.processor.Process(ctx, queue.Job{SubmissionID: sub.ID}))

	stored, _ := f.subs.GetByID(ctx, sub.ID)
	assert.True(t, stored.Status.IsTerminal())
}

// --- Lane routing ---

func TestLaneFor(t *testing.T) {
	assert.Equal(t, queue.LaneStandard, queue.LaneFor(""))
	assert.Equal(t, queue.LaneWarRoom, queue.LaneFor("room-1"),
		"War Room submissions take the dedicated high-priority lane")
}
