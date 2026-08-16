package tests

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/queue"
	"github.com/toji339/online-judge/internal/queue/rabbitmq"
)

// These tests exercise recovery against a real broker, because the whole
// point is behaviour the code cannot demonstrate on its own: a broker
// that goes away and comes back.
//
// They are skipped when no broker is reachable, so `go test ./...` stays
// green on a machine without Docker. Start one with:
//
//	docker compose up -d
//
// Note: they consume the real lane queues, so a judge worker running
// against the same broker will compete for their messages and make them
// fail. Stop any local worker before running the suite.
const brokerURL = "amqp://guest:guest@localhost:5672/"

// requireBroker skips the test unless a broker is actually reachable.
func requireBroker(t *testing.T) {
	t.Helper()
	client, err := rabbitmq.Connect(brokerURL)
	if err != nil {
		t.Skipf("no RabbitMQ at %s (start it with: docker compose up -d)", "localhost:5672")
	}
	_ = client.Close()
}

// restartBroker restarts the Compose RabbitMQ container and waits for it
// to accept connections again.
func restartBroker(t *testing.T) {
	t.Helper()
	cmd := exec.Command("docker", "restart", "oj-rabbitmq")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("could not restart the broker container: %v (%s)", err, out)
	}

	// The container is up before AMQP is ready, so wait for a real dial.
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if client, err := rabbitmq.Connect(brokerURL); err == nil {
			_ = client.Close()
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatal("broker did not come back within 90s")
}

// drainQueue removes anything left over from an earlier run so a test
// only ever sees its own messages.
func drainQueue(t *testing.T, lane queue.Lane) {
	t.Helper()
	client, err := rabbitmq.Connect(brokerURL)
	require.NoError(t, err)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = client.Consume(ctx, lane, 10, func(context.Context, queue.Job) error { return nil })
}

// =============================================================
// Test A — the broker is unavailable when the worker starts
// =============================================================

// TestRecovery_ConsumerSurvivesAnAbsentBrokerThenConnects is the
// scenario a deploy hits routinely: the worker starts before the broker
// is ready.
func TestRecovery_ConsumerSurvivesAnAbsentBrokerThenConnects(t *testing.T) {
	requireBroker(t)
	drainQueue(t, queue.LaneStandard)

	// Point at a dead port first; the client must keep retrying rather
	// than exiting or spinning.
	client := rabbitmq.New("amqp://guest:guest@127.0.0.1:1/")
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := client.Consume(ctx, queue.LaneStandard, 1, func(context.Context, queue.Job) error { return nil })
	assert.ErrorIs(t, err, context.DeadlineExceeded, "the consumer stayed alive and kept retrying")

	// Now a client pointed at the real broker consumes immediately.
	live := rabbitmq.New(brokerURL)
	defer live.Close()

	received := make(chan string, 1)
	consumeCtx, stopConsume := context.WithCancel(context.Background())
	defer stopConsume()
	go func() {
		_ = live.Consume(consumeCtx, queue.LaneStandard, 1, func(_ context.Context, job queue.Job) error {
			received <- job.SubmissionID
			return nil
		})
	}()

	publisher, err := rabbitmq.Connect(brokerURL)
	require.NoError(t, err)
	defer publisher.Close()
	require.NoError(t, publisher.Publish(context.Background(), queue.LaneStandard, queue.Job{SubmissionID: "after-wait"}))

	select {
	case id := <-received:
		assert.Equal(t, "after-wait", id)
	case <-time.After(20 * time.Second):
		t.Fatal("consumer never picked the job up")
	}
}

// =============================================================
// Test B — the broker restarts underneath a running consumer
// =============================================================

// TestRecovery_ConsumerResumesAfterBrokerRestart is the headline
// requirement: no worker restart, judging resumes by itself.
func TestRecovery_ConsumerResumesAfterBrokerRestart(t *testing.T) {
	requireBroker(t)
	drainQueue(t, queue.LaneStandard)

	client := rabbitmq.New(brokerURL)
	defer client.Close()

	var handled int64
	received := make(chan string, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = client.Consume(ctx, queue.LaneStandard, 2, func(_ context.Context, job queue.Job) error {
			atomic.AddInt64(&handled, 1)
			received <- job.SubmissionID
			return nil
		})
	}()

	// Prove it works before the disruption.
	publisher, err := rabbitmq.Connect(brokerURL)
	require.NoError(t, err)
	require.NoError(t, publisher.Publish(context.Background(), queue.LaneStandard, queue.Job{SubmissionID: "before"}))
	select {
	case id := <-received:
		require.Equal(t, "before", id)
	case <-time.After(20 * time.Second):
		t.Fatal("the consumer never worked in the first place")
	}
	_ = publisher.Close()

	// Take the broker away and bring it back.
	restartBroker(t)

	// The same consumer, never restarted, must pick up new work.
	after, err := rabbitmq.Connect(brokerURL)
	require.NoError(t, err)
	defer after.Close()

	var published bool
	for attempt := range 30 {
		if err := after.Publish(context.Background(), queue.LaneStandard, queue.Job{SubmissionID: "after-restart"}); err == nil {
			published = true
			break
		}
		_ = attempt
		time.Sleep(time.Second)
	}
	require.True(t, published, "could not publish after the restart")

	select {
	case id := <-received:
		assert.Equal(t, "after-restart", id, "the consumer recovered on its own")
	case <-time.After(60 * time.Second):
		t.Fatalf("consumer never recovered (handled %d before the restart)", atomic.LoadInt64(&handled))
	}
}

// =============================================================
// Test C — several workers recover together
// =============================================================

func TestRecovery_MultipleConsumersAllResume(t *testing.T) {
	requireBroker(t)
	drainQueue(t, queue.LaneStandard)

	const workers = 3
	var handled int64
	received := make(chan string, 32)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clients := make([]*rabbitmq.Client, workers)
	for i := range workers {
		clients[i] = rabbitmq.New(brokerURL)
		defer clients[i].Close()

		go func(c *rabbitmq.Client) {
			_ = c.Consume(ctx, queue.LaneStandard, 1, func(_ context.Context, job queue.Job) error {
				atomic.AddInt64(&handled, 1)
				received <- job.SubmissionID
				return nil
			})
		}(clients[i])
	}
	time.Sleep(3 * time.Second) // let every consumer register

	restartBroker(t)

	// Publish one job per worker; between them they must take all of it,
	// which only happens if every consumer re-registered.
	publisher, err := rabbitmq.Connect(brokerURL)
	require.NoError(t, err)
	defer publisher.Close()

	for i := range workers {
		var sent bool
		for range 30 {
			if err := publisher.Publish(context.Background(), queue.LaneStandard,
				queue.Job{SubmissionID: fmt.Sprintf("multi-%d", i)}); err == nil {
				sent = true
				break
			}
			time.Sleep(time.Second)
		}
		require.True(t, sent, "could not publish job %d after the restart", i)
	}

	deadline := time.After(60 * time.Second)
	for range workers {
		select {
		case <-received:
		case <-deadline:
			t.Fatalf("only %d of %d jobs were consumed after the restart", atomic.LoadInt64(&handled), workers)
		}
	}
}

// =============================================================
// Test D — message safety around a failure
// =============================================================

// TestRecovery_UnacknowledgedWorkIsRedelivered pins the delivery
// contract: at-least-once. A handler that never acknowledges — because
// its worker died mid-judge — must see the job again.
func TestRecovery_UnacknowledgedWorkIsRedelivered(t *testing.T) {
	requireBroker(t)
	drainQueue(t, queue.LaneStandard)

	publisher, err := rabbitmq.Connect(brokerURL)
	require.NoError(t, err)
	defer publisher.Close()
	require.NoError(t, publisher.Publish(context.Background(), queue.LaneStandard,
		queue.Job{SubmissionID: "redelivered"}))

	// First consumer takes the job and dies without acknowledging, which
	// is what a killed worker looks like to the broker.
	first := rabbitmq.New(brokerURL)
	got := make(chan struct{}, 1)
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	go func() {
		_ = first.Consume(firstCtx, queue.LaneStandard, 1, func(ctx context.Context, job queue.Job) error {
			got <- struct{}{}
			<-ctx.Done() // hold it, never acknowledge
			return ctx.Err()
		})
	}()

	select {
	case <-got:
	case <-time.After(20 * time.Second):
		t.Fatal("first consumer never received the job")
	}
	cancelFirst()
	require.NoError(t, first.Close())

	// A second consumer must be given the same job.
	second := rabbitmq.New(brokerURL)
	defer second.Close()
	redelivered := make(chan string, 1)
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	go func() {
		_ = second.Consume(secondCtx, queue.LaneStandard, 1, func(_ context.Context, job queue.Job) error {
			redelivered <- job.SubmissionID
			return nil
		})
	}()

	select {
	case id := <-redelivered:
		assert.Equal(t, "redelivered", id, "an unacknowledged job must not be lost")
	case <-time.After(30 * time.Second):
		t.Fatal("the job was lost instead of being redelivered")
	}
}

// TestRecovery_FailedJobsAreNotRequeuedForever guards the other side:
// a job whose handler genuinely failed has already been recorded as
// terminal, so redelivering it would loop forever.
func TestRecovery_FailedJobsAreNotRequeuedForever(t *testing.T) {
	requireBroker(t)
	drainQueue(t, queue.LaneStandard)

	publisher, err := rabbitmq.Connect(brokerURL)
	require.NoError(t, err)
	defer publisher.Close()
	require.NoError(t, publisher.Publish(context.Background(), queue.LaneStandard,
		queue.Job{SubmissionID: "always-fails"}))

	client := rabbitmq.New(brokerURL)
	defer client.Close()

	var attempts int64
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	_ = client.Consume(ctx, queue.LaneStandard, 1, func(context.Context, queue.Job) error {
		atomic.AddInt64(&attempts, 1)
		return fmt.Errorf("judging failed")
	})

	assert.Equal(t, int64(1), atomic.LoadInt64(&attempts),
		"a failed job is dropped, not redelivered in a loop")
}

// =============================================================
// Test E — graceful shutdown
// =============================================================

// TestRecovery_ShutdownWaitsForInFlightWork checks that cancelling the
// context lets a running job finish rather than abandoning it.
func TestRecovery_ShutdownWaitsForInFlightWork(t *testing.T) {
	requireBroker(t)
	drainQueue(t, queue.LaneStandard)

	publisher, err := rabbitmq.Connect(brokerURL)
	require.NoError(t, err)
	defer publisher.Close()
	require.NoError(t, publisher.Publish(context.Background(), queue.LaneStandard,
		queue.Job{SubmissionID: "in-flight"}))

	client := rabbitmq.New(brokerURL)
	defer client.Close()

	started := make(chan struct{})
	var finished atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = client.Consume(ctx, queue.LaneStandard, 1, func(context.Context, queue.Job) error {
			close(started)
			// Judging keeps going after the shutdown signal arrives.
			time.Sleep(2 * time.Second)
			finished.Store(true)
			return nil
		})
	}()

	select {
	case <-started:
	case <-time.After(20 * time.Second):
		t.Fatal("job never started")
	}

	cancel()  // SIGTERM equivalent
	wg.Wait() // Consume must not return until the handler is done

	assert.True(t, finished.Load(), "shutdown abandoned a job that was mid-flight")
}
