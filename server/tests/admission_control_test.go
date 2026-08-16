package tests

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/problem"
	problemmongo "github.com/toji339/online-judge/internal/problem/mongorepo"
	"github.com/toji339/online-judge/internal/submission"
	submissionmongo "github.com/toji339/online-judge/internal/submission/mongorepo"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// These run against the real database on purpose. Admission control is
// enforced by a unique partial index, so a fake cannot demonstrate that
// it actually holds — only MongoDB can.

func admissionFixture(t *testing.T) (*submission.Service, string) {
	t.Helper()
	clearSubmissions(t)

	problemSvc := problem.NewService(problemmongo.New(testDB))
	prob := seedProblem(t, problemSvc)
	return submission.NewService(submissionmongo.New(testDB)), prob.ID
}

func createInput(userID, problemID string) submission.CreateInput {
	return submission.CreateInput{
		UserID:    userID,
		ProblemID: problemID,
		Language:  "python",
		Code:      "print(1)",
	}
}

// TestAdmission_ConcurrentSubmissionsCannotExceedTheLimit is the race the
// audit identified: every request reads "none in flight" before any of
// them writes, so a count-then-insert admits all of them.
func TestAdmission_ConcurrentSubmissionsCannotExceedTheLimit(t *testing.T) {
	svc, problemID := admissionFixture(t)
	userID := bson.NewObjectID().Hex()

	const attempts = 100
	var admitted, rejected, other int64

	var wg sync.WaitGroup
	start := make(chan struct{})
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release them all at once to maximise overlap
			_, err := svc.Create(context.Background(), createInput(userID, problemID))
			switch {
			case err == nil:
				atomic.AddInt64(&admitted, 1)
			case err == submission.ErrTooManyPending:
				atomic.AddInt64(&rejected, 1)
			default:
				atomic.AddInt64(&other, 1)
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	assert.Equal(t, int64(1), atomic.LoadInt64(&admitted),
		"exactly one submission may be in flight per user")
	assert.Equal(t, int64(attempts-1), atomic.LoadInt64(&rejected),
		"every other request must be rejected cleanly, not fail with something else")
	assert.Zero(t, atomic.LoadInt64(&other))

	// And the database agrees with the counters.
	stored, err := svc.List(context.Background(), submission.ListFilter{UserID: userID, PageSize: 100})
	require.NoError(t, err)
	assert.Len(t, stored, 1, "only one submission document was actually written")
}

// TestAdmission_RaceIsStableAcrossRepeats guards against a fix that only
// happens to work once.
func TestAdmission_RaceIsStableAcrossRepeats(t *testing.T) {
	svc, problemID := admissionFixture(t)

	for round := range 5 {
		userID := bson.NewObjectID().Hex()
		var admitted int64

		var wg sync.WaitGroup
		start := make(chan struct{})
		for range 30 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if _, err := svc.Create(context.Background(), createInput(userID, problemID)); err == nil {
					atomic.AddInt64(&admitted, 1)
				}
			}()
		}
		close(start)
		wg.Wait()

		assert.Equal(t, int64(1), atomic.LoadInt64(&admitted), "round %d admitted the wrong number", round)
	}
}

// TestAdmission_CapacityReturnsWhenASubmissionFinishes checks the other
// half: the constraint has to release itself, or a user could submit
// exactly once and never again.
func TestAdmission_CapacityReturnsWhenASubmissionFinishes(t *testing.T) {
	svc, problemID := admissionFixture(t)
	userID := bson.NewObjectID().Hex()
	ctx := context.Background()

	first, err := svc.Create(ctx, createInput(userID, problemID))
	require.NoError(t, err)

	// Still in flight — refused.
	_, err = svc.Create(ctx, createInput(userID, problemID))
	require.ErrorIs(t, err, submission.ErrTooManyPending)

	// Running is still non-terminal, so still refused.
	require.NoError(t, svc.MarkRunning(ctx, first.ID))
	_, err = svc.Create(ctx, createInput(userID, problemID))
	require.ErrorIs(t, err, submission.ErrTooManyPending)

	// A verdict releases the slot.
	require.NoError(t, svc.MarkJudged(ctx, first.ID, submission.Result{
		Status: submission.StatusAccepted, FailedCase: -1,
	}))

	second, err := svc.Create(ctx, createInput(userID, problemID))
	require.NoError(t, err, "a judged submission must free the user to submit again")
	assert.NotEqual(t, first.ID, second.ID)
}

// TestAdmission_FailedSubmissionsAlsoReleaseCapacity covers the path that
// previously locked users out permanently.
func TestAdmission_FailedSubmissionsAlsoReleaseCapacity(t *testing.T) {
	svc, problemID := admissionFixture(t)
	userID := bson.NewObjectID().Hex()
	ctx := context.Background()

	first, err := svc.Create(ctx, createInput(userID, problemID))
	require.NoError(t, err)

	// An infrastructure failure, not a verdict.
	require.NoError(t, svc.MarkFailed(ctx, first.ID, "sandbox unavailable"))

	_, err = svc.Create(ctx, createInput(userID, problemID))
	assert.NoError(t, err, "a failed submission must not hold the slot forever")
}

// TestAdmission_IsPerUser makes sure the constraint scopes to a user
// rather than throttling the whole platform.
func TestAdmission_IsPerUser(t *testing.T) {
	svc, problemID := admissionFixture(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	var admitted int64
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			userID := bson.NewObjectID().Hex() // a different user each time
			if _, err := svc.Create(ctx, createInput(userID, problemID)); err == nil {
				atomic.AddInt64(&admitted, 1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(20), atomic.LoadInt64(&admitted),
		"one user's in-flight submission must not block anybody else")
}

// TestAdmission_WarRoomSubmissionsShareTheSameLimit confirms the limit is
// not bypassable by submitting through the race path instead.
func TestAdmission_WarRoomSubmissionsShareTheSameLimit(t *testing.T) {
	svc, problemID := admissionFixture(t)
	userID := bson.NewObjectID().Hex()
	ctx := context.Background()

	_, err := svc.Create(ctx, createInput(userID, problemID))
	require.NoError(t, err)

	roomSubmission := createInput(userID, problemID)
	roomSubmission.WarRoomID = bson.NewObjectID().Hex()
	_, err = svc.Create(ctx, roomSubmission)

	assert.ErrorIs(t, err, submission.ErrTooManyPending)
}
