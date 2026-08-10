package submission_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/judge"
	"github.com/toji339/online-judge/internal/submission"
	"github.com/toji339/online-judge/internal/submission/submissiontest"
)

func newService() (*submission.Service, *submissiontest.FakeRepository) {
	repo := submissiontest.New()
	return submission.NewService(repo), repo
}

func validInput() submission.CreateInput {
	return submission.CreateInput{
		UserID:      "user-1",
		ProblemID:   "problem-1",
		ProblemSlug: "two-sum",
		Language:    "python",
		Code:        "print(1)",
	}
}

// --- Create: happy path ---

func TestCreate_StoresPendingSubmission(t *testing.T) {
	svc, _ := newService()

	sub, err := svc.Create(context.Background(), validInput())

	require.NoError(t, err)
	assert.NotEmpty(t, sub.ID)
	assert.Equal(t, submission.StatusPending, sub.Status)
	assert.Equal(t, -1, sub.FailedCase, "no test case has failed yet")
	assert.False(t, sub.SubmittedAt.IsZero(), "submitted_at is stamped by the server")
	assert.Nil(t, sub.JudgedAt)
}

// --- Create: validation failures ---

func TestCreate_RejectsInvalidInput(t *testing.T) {
	cases := map[string]func(*submission.CreateInput){
		"missing user":         func(in *submission.CreateInput) { in.UserID = "" },
		"missing problem":      func(in *submission.CreateInput) { in.ProblemID = "" },
		"unsupported language": func(in *submission.CreateInput) { in.Language = "brainfuck" },
		"blank code":           func(in *submission.CreateInput) { in.Code = "   \n" },
		"oversized code":       func(in *submission.CreateInput) { in.Code = string(make([]byte, 70*1024)) },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			svc, _ := newService()
			in := validInput()
			mutate(&in)

			_, err := svc.Create(context.Background(), in)

			var vErr submission.ValidationError
			assert.ErrorAs(t, err, &vErr, "expected a ValidationError")
		})
	}
}

// --- Create: admission control edge case ---

func TestCreate_RejectsSecondPendingSubmission(t *testing.T) {
	svc, _ := newService()
	ctx := context.Background()
	_, err := svc.Create(ctx, validInput())
	require.NoError(t, err)

	_, err = svc.Create(ctx, validInput())

	assert.ErrorIs(t, err, submission.ErrTooManyPending)
}

func TestCreate_AllowsNewSubmissionOncePreviousIsJudged(t *testing.T) {
	svc, _ := newService()
	ctx := context.Background()
	first, err := svc.Create(ctx, validInput())
	require.NoError(t, err)
	require.NoError(t, svc.MarkJudged(ctx, first.ID, submission.Result{Status: submission.StatusWrongAnswer, FailedCase: 2}))

	second, err := svc.Create(ctx, validInput())

	require.NoError(t, err)
	assert.NotEqual(t, first.ID, second.ID)
}

func TestCreate_AdmissionControlIsPerUser(t *testing.T) {
	svc, _ := newService()
	ctx := context.Background()
	_, err := svc.Create(ctx, validInput())
	require.NoError(t, err)

	other := validInput()
	other.UserID = "user-2"
	_, err = svc.Create(ctx, other)

	assert.NoError(t, err, "one user's pending submission must not block another")
}

// --- Judging lifecycle ---

func TestMarkJudged_RecordsVerdictAndStampsJudgedAt(t *testing.T) {
	svc, _ := newService()
	ctx := context.Background()
	sub, err := svc.Create(ctx, validInput())
	require.NoError(t, err)
	require.NoError(t, svc.MarkRunning(ctx, sub.ID))

	err = svc.MarkJudged(ctx, sub.ID, submission.Result{
		Status: submission.StatusAccepted, RuntimeMS: 42, MemoryKB: 2048, FailedCase: -1,
	})
	require.NoError(t, err)

	stored, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, submission.StatusAccepted, stored.Status)
	assert.Equal(t, int64(42), stored.RuntimeMS)
	assert.Equal(t, int64(2048), stored.MemoryKB)
	require.NotNil(t, stored.JudgedAt, "a terminal verdict stamps judged_at")
}

func TestMarkRunning_DoesNotStampJudgedAt(t *testing.T) {
	svc, _ := newService()
	ctx := context.Background()
	sub, _ := svc.Create(ctx, validInput())

	require.NoError(t, svc.MarkRunning(ctx, sub.ID))

	stored, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, submission.StatusRunning, stored.Status)
	assert.Nil(t, stored.JudgedAt)
}

func TestMarkJudged_RejectsNonTerminalStatus(t *testing.T) {
	svc, _ := newService()
	ctx := context.Background()
	sub, _ := svc.Create(ctx, validInput())

	err := svc.MarkJudged(ctx, sub.ID, submission.Result{Status: submission.StatusRunning})

	var vErr submission.ValidationError
	assert.ErrorAs(t, err, &vErr)
}

func TestMarkFailed_ClearsPendingState(t *testing.T) {
	svc, _ := newService()
	ctx := context.Background()
	sub, _ := svc.Create(ctx, validInput())

	require.NoError(t, svc.MarkFailed(ctx, sub.ID, "sandbox unavailable"))

	stored, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.True(t, stored.Status.IsTerminal(), "a failed submission must not stay pending")
}

func TestGetByID_UnknownReturnsNotFound(t *testing.T) {
	svc, _ := newService()

	_, err := svc.GetByID(context.Background(), "nope")

	assert.True(t, errors.Is(err, submission.ErrNotFound))
}

// --- History listing ---

func TestList_FiltersByStatusAndProblem(t *testing.T) {
	svc, _ := newService()
	ctx := context.Background()
	seed(t, svc, "user-1", "problem-1", submission.StatusAccepted)
	seed(t, svc, "user-1", "problem-2", submission.StatusWrongAnswer)
	seed(t, svc, "user-2", "problem-1", submission.StatusAccepted)

	accepted, err := svc.List(ctx, submission.ListFilter{UserID: "user-1", Status: submission.StatusAccepted})
	require.NoError(t, err)
	assert.Len(t, accepted, 1)
	assert.Equal(t, "problem-1", accepted[0].ProblemID)

	byProblem, err := svc.List(ctx, submission.ListFilter{UserID: "user-1", ProblemID: "problem-2"})
	require.NoError(t, err)
	assert.Len(t, byProblem, 1)
}

func TestList_PaginatesNewestFirst(t *testing.T) {
	svc, _ := newService()
	ctx := context.Background()
	for range 5 {
		seed(t, svc, "user-1", "problem-1", submission.StatusWrongAnswer)
	}

	page1, err := svc.List(ctx, submission.ListFilter{UserID: "user-1", Page: 1, PageSize: 2})
	require.NoError(t, err)
	page3, err := svc.List(ctx, submission.ListFilter{UserID: "user-1", Page: 3, PageSize: 2})
	require.NoError(t, err)

	assert.Len(t, page1, 2)
	assert.Len(t, page3, 1)

	total, err := svc.Count(ctx, submission.ListFilter{UserID: "user-1"})
	require.NoError(t, err)
	assert.Equal(t, 5, total)
}

// --- Stats ---

func TestStats_CountsDistinctSolvedProblems(t *testing.T) {
	svc, _ := newService()
	ctx := context.Background()
	seed(t, svc, "user-1", "problem-1", submission.StatusAccepted)
	seed(t, svc, "user-1", "problem-1", submission.StatusAccepted) // same problem twice
	seed(t, svc, "user-1", "problem-2", submission.StatusAccepted)
	seed(t, svc, "user-1", "problem-3", submission.StatusWrongAnswer)

	stats, err := svc.Stats(ctx, "user-1")

	require.NoError(t, err)
	assert.Equal(t, 4, stats.TotalSubmissions)
	assert.Equal(t, 3, stats.Accepted)
	assert.ElementsMatch(t, []string{"problem-1", "problem-2"}, stats.SolvedProblemIDs)
}

// --- War Room winner ---

func TestFirstAcceptedInRoom_PicksEarliestJudgedSubmission(t *testing.T) {
	svc, _ := newService()
	ctx := context.Background()

	winner := seedRoom(t, svc, "user-1", "room-1", submission.StatusAccepted)
	seedRoom(t, svc, "user-2", "room-1", submission.StatusAccepted)
	seedRoom(t, svc, "user-3", "room-2", submission.StatusAccepted)

	got, err := svc.FirstAcceptedInRoom(ctx, "room-1")

	require.NoError(t, err)
	assert.Equal(t, winner, got.ID, "the first submission judged accepted wins")
}

func TestFirstAcceptedInRoom_NoWinnerYet(t *testing.T) {
	svc, _ := newService()
	ctx := context.Background()
	seedRoom(t, svc, "user-1", "room-1", submission.StatusWrongAnswer)

	_, err := svc.FirstAcceptedInRoom(ctx, "room-1")

	assert.ErrorIs(t, err, submission.ErrNotFound)
}

// --- Verdict mapping ---

func TestStatusFromVerdict(t *testing.T) {
	assert.Equal(t, submission.StatusAccepted, submission.StatusFromVerdict(judge.VerdictAccepted))
	assert.Equal(t, submission.StatusTLE, submission.StatusFromVerdict(judge.VerdictTimeLimitExceeded))
	assert.Equal(t, submission.StatusMLE, submission.StatusFromVerdict(judge.VerdictMemoryLimitExceeded))
	assert.Equal(t, submission.StatusCompileError, submission.StatusFromVerdict(judge.VerdictCompileError))
	assert.Equal(t, submission.StatusWrongAnswer, submission.StatusFromVerdict(judge.VerdictWrongAnswer))
	assert.Equal(t, submission.StatusRuntimeError, submission.StatusFromVerdict(judge.Verdict("something-new")))
}

// seed creates a submission and immediately judges it, returning its ID.
// Judging it keeps admission control from blocking the next seed call.
func seed(t *testing.T, svc *submission.Service, userID, problemID string, status submission.Status) string {
	t.Helper()
	in := validInput()
	in.UserID = userID
	in.ProblemID = problemID
	sub, err := svc.Create(context.Background(), in)
	require.NoError(t, err)
	require.NoError(t, svc.MarkJudged(context.Background(), sub.ID, submission.Result{Status: status, FailedCase: -1}))
	return sub.ID
}

func seedRoom(t *testing.T, svc *submission.Service, userID, roomID string, status submission.Status) string {
	t.Helper()
	in := validInput()
	in.UserID = userID
	in.WarRoomID = roomID
	sub, err := svc.Create(context.Background(), in)
	require.NoError(t, err)
	require.NoError(t, svc.MarkJudged(context.Background(), sub.ID, submission.Result{Status: status, FailedCase: -1}))
	return sub.ID
}
