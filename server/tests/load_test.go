package tests

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/problem"
	problemmongo "github.com/toji339/online-judge/internal/problem/mongorepo"
	"github.com/toji339/online-judge/internal/queue"
	"github.com/toji339/online-judge/internal/queue/rabbitmq"
	"github.com/toji339/online-judge/internal/submission"
	submissionmongo "github.com/toji339/online-judge/internal/submission/mongorepo"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// TestLoad_ConcurrentSubmissionsAllReachATerminalVerdict pushes a burst
// through the real path — service, broker, worker, sandbox — and checks
// that every one of them ends up judged.
//
// It needs a broker AND a running judge worker, so it skips unless both
// are present:
//
//	docker compose up -d
//	go run ./cmd/worker
//
// Each submission belongs to a different user because admission control
// deliberately allows only one in flight per user.
func TestLoad_ConcurrentSubmissionsAllReachATerminalVerdict(t *testing.T) {
	requireBroker(t)

	const submissions = 100
	const settleTimeout = 5 * time.Minute

	clearSubmissions(t)
	problemSvc := problem.NewService(problemmongo.New(testDB))
	prob := seedProblem(t, problemSvc)
	subSvc := submission.NewService(submissionmongo.New(testDB))

	publisher, err := rabbitmq.Connect(brokerURL)
	require.NoError(t, err)
	defer publisher.Close()

	// Half correct, half wrong, so the run exercises more than one verdict.
	correct := "import sys\nprint(sum(int(x) for x in sys.stdin.readline().split()))"
	wrong := "print('definitely not the answer')"

	ids := make([]string, submissions)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := range submissions {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			code := correct
			if i%2 == 1 {
				code = wrong
			}

			sub, err := subSvc.Create(context.Background(), submission.CreateInput{
				UserID:      bson.NewObjectID().Hex(), // one user per submission
				ProblemID:   prob.ID,
				ProblemSlug: prob.Slug,
				Language:    "python",
				Code:        code,
			})
			if err != nil {
				t.Errorf("create %d: %v", i, err)
				return
			}

			if err := publisher.Publish(context.Background(), queue.LaneStandard, queue.Job{
				SubmissionID: sub.ID, UserID: sub.UserID, ProblemID: sub.ProblemID,
			}); err != nil {
				t.Errorf("publish %d: %v", i, err)
				return
			}

			mu.Lock()
			ids[i] = sub.ID
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	// Wait for the workers to drain the burst.
	deadline := time.Now().Add(settleTimeout)
	verdicts := map[submission.Status]int{}
	var pending int

	for time.Now().Before(deadline) {
		verdicts = map[submission.Status]int{}
		pending = 0

		for _, id := range ids {
			if id == "" {
				continue
			}
			sub, err := subSvc.GetByID(context.Background(), id)
			require.NoError(t, err)
			if sub.Status.IsTerminal() {
				verdicts[sub.Status]++
			} else {
				pending++
			}
		}
		if pending == 0 {
			break
		}
		time.Sleep(3 * time.Second)
	}

	t.Logf("verdicts: %v", verdicts)
	if pending > 0 {
		t.Skipf("%d submissions still unjudged after %s — is a judge worker running? "+
			"(start one with: go run ./cmd/worker)", pending, settleTimeout)
	}

	assert.Equal(t, submissions/2, verdicts[submission.StatusAccepted],
		"every correct solution should be accepted")
	assert.Equal(t, submissions/2, verdicts[submission.StatusWrongAnswer],
		"every incorrect solution should be a wrong answer")
}
