package rabbitmq

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/queue"
)

// --- Backoff ---

func TestBackoffFor_GrowsAndIsBounded(t *testing.T) {
	// Doubling, so each attempt waits longer than the last until the
	// ceiling — a fixed delay would hammer a broker that is still
	// starting, and an unbounded one would stop retrying in practice.
	previous := time.Duration(0)
	for attempt := 1; attempt <= 12; attempt++ {
		delay := backoffFor(attempt)

		assert.GreaterOrEqual(t, delay, minBackoff, "attempt %d must not busy-loop", attempt)
		upper := time.Duration(float64(maxBackoff) * (1 + jitterFrac))
		assert.LessOrEqual(t, delay, upper, "attempt %d must stay bounded", attempt)

		if attempt <= 5 {
			assert.Greater(t, delay, previous/2, "attempt %d should keep growing", attempt)
		}
		previous = delay
	}
}

func TestBackoffFor_IsJittered(t *testing.T) {
	// Identical delays across workers would make them reconnect in
	// lockstep and thunder-herd the broker.
	seen := make(map[time.Duration]bool)
	for range 40 {
		seen[backoffFor(6)] = true
	}
	assert.Greater(t, len(seen), 1, "backoff must not be deterministic")
}

// --- Lifecycle without a broker ---

// TestNew_DoesNotRequireABroker is the property that lets a worker start
// before RabbitMQ exists.
func TestNew_DoesNotRequireABroker(t *testing.T) {
	client := New("amqp://guest:guest@127.0.0.1:1/")
	require.NotNil(t, client)
	defer client.Close()

	conn, ch := client.live()
	assert.Nil(t, conn, "no connection until one is needed")
	assert.Nil(t, ch)
}

func TestConnect_FailsFastWhenTheBrokerIsAbsent(t *testing.T) {
	// The API uses this: it needs to know at startup whether to fall back
	// to judging inline.
	_, err := Connect("amqp://guest:guest@127.0.0.1:1/")

	require.Error(t, err)
	assert.ErrorIs(t, err, queue.ErrUnavailable)
}

// TestConsume_RetriesUntilTheContextEnds covers Test A: the worker stays
// alive and keeps retrying while the broker is unavailable, rather than
// crash-looping or giving up.
func TestConsume_RetriesUntilTheContextEnds(t *testing.T) {
	client := New("amqp://guest:guest@127.0.0.1:1/")
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	err := client.Consume(ctx, queue.LaneStandard, 1, func(context.Context, queue.Job) error {
		t.Fatal("no job can be delivered without a broker")
		return nil
	})

	// It returns because the context ended, not because it despaired.
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.GreaterOrEqual(t, time.Since(start), 1500*time.Millisecond,
		"it should have spent the window retrying, not returned immediately")
}

func TestPublish_ReportsUnavailableRatherThanBlocking(t *testing.T) {
	// The API falls back to inline judging on this error; blocking here
	// would make a user wait out a broker outage.
	client := New("amqp://guest:guest@127.0.0.1:1/")
	defer client.Close()

	err := client.Publish(context.Background(), queue.LaneStandard, queue.Job{SubmissionID: "s1"})

	assert.ErrorIs(t, err, queue.ErrUnavailable)
}

func TestPublish_RejectsAnUnknownLane(t *testing.T) {
	client := New("amqp://guest:guest@127.0.0.1:1/")
	defer client.Close()

	err := client.Publish(context.Background(), queue.Lane("nonsense"), queue.Job{})

	assert.ErrorContains(t, err, "unknown lane")
}

func TestConsume_RejectsAnUnknownLane(t *testing.T) {
	client := New("amqp://guest:guest@127.0.0.1:1/")
	defer client.Close()

	err := client.Consume(context.Background(), queue.Lane("nonsense"), 1, nil)

	assert.ErrorContains(t, err, "unknown lane")
}

// --- Shutdown ---

func TestClose_IsIdempotentAndStopsFurtherUse(t *testing.T) {
	client := New("amqp://guest:guest@127.0.0.1:1/")

	require.NoError(t, client.Close())
	require.NoError(t, client.Close(), "closing twice must not panic or error")

	err := client.Publish(context.Background(), queue.LaneStandard, queue.Job{})
	assert.ErrorIs(t, err, ErrClosed)

	err = client.Consume(context.Background(), queue.LaneStandard, 1, nil)
	assert.ErrorIs(t, err, ErrClosed)
}

// --- Topology ---

func TestQueueNames_CoverEveryLane(t *testing.T) {
	// A lane with no queue would publish into the void, so the mapping
	// must stay exhaustive as lanes are added.
	for _, lane := range []queue.Lane{queue.LaneStandard, queue.LaneWarRoom} {
		name, ok := queueNames[lane]
		assert.True(t, ok, "lane %q has no queue", lane)
		assert.NotEmpty(t, name)
	}
	assert.NotEqual(t, queueNames[queue.LaneStandard], queueNames[queue.LaneWarRoom],
		"the priority lane must be a separate queue, or it cannot be consumed separately")
}
