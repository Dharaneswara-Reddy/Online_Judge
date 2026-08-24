package rabbitmq

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/queue"
)

// captured records one dead-letter publish, so the capture path can be
// tested without a broker.
type captured struct {
	body   []byte
	reason string
	cause  error
}

// recorder returns a captureFunc that remembers what it was asked to
// dead-letter, plus a pointer to the recorded calls.
func recorder(err error) (captureFunc, *[]captured) {
	var calls []captured
	return func(_ context.Context, body []byte, reason string, cause error) error {
		calls = append(calls, captured{body: body, reason: reason, cause: cause})
		return err
	}, &calls
}

// noCapture fails the test if anything is dead-lettered.
func noCapture(t *testing.T) captureFunc {
	t.Helper()
	return func(_ context.Context, _ []byte, reason string, _ error) error {
		t.Fatalf("nothing should have been dead-lettered, got reason %q", reason)
		return nil
	}
}

// =====================================================================
// Naming and routing
// =====================================================================

// TestDeadLetterRoutingKeyIsTheSourceQueueName pins the property the
// whole design leans on: we publish the capture ourselves using the
// source queue's name as the routing key, which is exactly the routing
// key RabbitMQ would use if an operator later switched the same
// behaviour on with a broker policy. Both paths therefore land in the
// same dead-letter queue, and applying the policy cannot silently start
// splitting failures across two places.
func TestDeadLetterRoutingKeyIsTheSourceQueueName(t *testing.T) {
	c := New("amqp://guest:guest@localhost:5672/")

	standard, ok := c.QueueName(queue.LaneStandard)
	require.True(t, ok)
	assert.Equal(t, "judge.standard", standard)

	warroom, ok := c.QueueName(queue.LaneWarRoom)
	require.True(t, ok)
	assert.Equal(t, "judge.warroom", warroom)
}

// TestDeadLetterQueuesAreLaneSeparate keeps the lane split the rest of
// the package is built around: a War Room failure must be triageable
// without digging through a pile of practice failures.
func TestDeadLetterQueuesAreLaneSeparate(t *testing.T) {
	c := New("amqp://guest:guest@localhost:5672/")

	standard, ok := c.DeadLetterQueueName(queue.LaneStandard)
	require.True(t, ok)
	warroom, ok := c.DeadLetterQueueName(queue.LaneWarRoom)
	require.True(t, ok)

	assert.Equal(t, "judge.dead.standard", standard)
	assert.Equal(t, "judge.dead.warroom", warroom)
	assert.NotEqual(t, standard, warroom)
}

// TestDeadLetterNamesAreNamespaced keeps tests from writing into the real
// dead-letter queues on a broker that is also serving a judge worker.
func TestDeadLetterNamesAreNamespaced(t *testing.T) {
	c := NewNamespaced("amqp://guest:guest@localhost:5672/", "test.ns")

	name, ok := c.DeadLetterQueueName(queue.LaneStandard)
	require.True(t, ok)
	assert.Equal(t, "test.ns.judge.dead.standard", name)
	assert.Equal(t, "test.ns.judge.dlx", c.deadLetterExchangeName())
}

// TestDeadLetterQueueNameRejectsAnUnknownLane mirrors QueueName, so a
// typo cannot silently produce a queue called "judge.dead.".
func TestDeadLetterQueueNameRejectsAnUnknownLane(t *testing.T) {
	c := New("amqp://guest:guest@localhost:5672/")

	_, ok := c.DeadLetterQueueName(queue.Lane("nonsense"))
	assert.False(t, ok)
}

// =====================================================================
// The bound on the dead-letter queues
// =====================================================================

// TestDeadLetterQueuesAreBounded is the "queue bounds" half of the task.
//
// The bound lives here and *only* here. The judging lanes stay unbounded
// on purpose: Publish does not use publisher confirms, so an x-max-length
// on a lane would either drop the oldest queued submission (drop-head) or
// silently discard the newest (reject-publish without confirms). Both are
// message loss, which is exactly what this change is meant to prevent. A
// dead-letter queue is different — it holds forensic copies of jobs whose
// submission rows already exist in Mongo, so shedding the oldest copy
// under extreme pressure costs a diagnostic, not a user's submission.
func TestDeadLetterQueuesAreBounded(t *testing.T) {
	args := deadLetterArgs()

	assert.Equal(t, int32(deadLetterMaxLength), args["x-max-length"],
		"a dead-letter queue must not grow without limit")
	assert.Equal(t, "drop-head", args["x-overflow"],
		"overflow must shed the oldest copy, never reject the publish")
	assert.Equal(t, deadLetterTTL.Milliseconds(), args["x-message-ttl"],
		"captures expire so the queue empties itself")
}

// TestDeadLetterBoundsAreJustifiable states the reasoning behind the two
// numbers in a form that fails if someone changes them without thinking.
//
// 10,000 per lane: the submission service admits at most one in-flight
// submission per user, so the number of jobs that can be failing at once
// is bounded by active users, and a five-figure backlog is far past any
// plausible incident. At roughly a few hundred bytes per capture that is
// single-digit megabytes of broker memory per lane.
//
// 14 days: a capture is a pointer to a Mongo row that the stale-submission
// reaper has long since resolved. After a fortnight it has no diagnostic
// value left, and expiry is what guarantees the queue cannot become
// permanent broker memory even if nobody ever drains it.
func TestDeadLetterBoundsAreJustifiable(t *testing.T) {
	assert.Equal(t, 10000, deadLetterMaxLength)
	assert.Equal(t, 14*24*time.Hour, deadLetterTTL)
}

// =====================================================================
// dispatch: capture on an exhausted retry
// =====================================================================

// TestDispatch_CapturesAnExhaustedJob is the headline behaviour. A job
// that has spent its retry budget used to be nacked without requeue and
// vanish. It must now be published to the dead-letter exchange first and
// only then settled.
func TestDispatch_CapturesAnExhaustedJob(t *testing.T) {
	d, ack := delivery(t, true)
	capture, calls := recorder(nil)

	dispatch(context.Background(), d, func(context.Context, queue.Job) error {
		return errors.New("load submission sub-1: connection refused")
	}, capture)

	require.Len(t, *calls, 1, "the exhausted job must be captured")
	assert.Equal(t, deadLetterReasonExhausted, (*calls)[0].reason)

	var job queue.Job
	require.NoError(t, json.Unmarshal((*calls)[0].body, &job))
	assert.Equal(t, "sub-1", job.SubmissionID, "the captured body is the original job")

	assert.True(t, ack.acked, "a captured job is acknowledged, not rejected")
	assert.False(t, ack.nacked)
}

// TestDispatch_CaptureHappensBeforeSettling guards the ordering that
// makes this lossless. Publishing the copy first and settling second
// means a crash in between costs a redelivery (at-least-once already
// tolerates that), whereas settling first would cost the message.
func TestDispatch_CaptureHappensBeforeSettling(t *testing.T) {
	ack := &fakeAcknowledger{}
	body, err := json.Marshal(queue.Job{SubmissionID: "ordering"})
	require.NoError(t, err)
	d := amqp.Delivery{Acknowledger: ack, Body: body, Redelivered: true}

	var settledBeforeCapture bool
	capture := func(context.Context, []byte, string, error) error {
		settledBeforeCapture = ack.acked || ack.nacked
		return nil
	}

	dispatch(context.Background(), d, func(context.Context, queue.Job) error {
		return errors.New("boom")
	}, capture)

	assert.False(t, settledBeforeCapture,
		"the delivery must still be unsettled while the copy is being published")
	assert.True(t, ack.acked)
}

// TestDispatch_FallsBackToDiscardWhenCaptureFails keeps the change from
// being a regression. If the dead-letter publish cannot be made, the old
// behaviour — drop it and leave the row to the stale-submission reaper —
// is still strictly better than requeueing a poison message forever.
func TestDispatch_FallsBackToDiscardWhenCaptureFails(t *testing.T) {
	d, ack := delivery(t, true)
	capture, calls := recorder(errors.New("dead-letter exchange unreachable"))

	dispatch(context.Background(), d, func(context.Context, queue.Job) error {
		return errors.New("boom")
	}, capture)

	require.Len(t, *calls, 1)
	assert.True(t, ack.nacked, "an uncapturable job falls back to the old discard")
	assert.False(t, ack.requeue, "it must still not loop forever")
	assert.False(t, ack.acked)
}

// =====================================================================
// dispatch: the paths that must NOT dead-letter
// =====================================================================

// TestDispatch_DoesNotCaptureASuccess is the obvious one, and cheap to
// keep honest.
func TestDispatch_DoesNotCaptureASuccess(t *testing.T) {
	d, ack := delivery(t, true)

	dispatch(context.Background(), d, func(context.Context, queue.Job) error {
		return nil
	}, noCapture(t))

	assert.True(t, ack.acked)
}

// TestDispatch_DoesNotCaptureAFirstFailure protects the retry budget.
// Capturing on the first failure would turn a transient Mongo blip into
// a dead-lettered submission and skip the retry that decideAck exists to
// provide.
func TestDispatch_DoesNotCaptureAFirstFailure(t *testing.T) {
	d, ack := delivery(t, false)

	dispatch(context.Background(), d, func(context.Context, queue.Job) error {
		return errors.New("mongo unreachable")
	}, noCapture(t))

	assert.True(t, ack.nacked)
	assert.True(t, ack.requeue, "a first failure still goes back on the queue")
}

// TestDispatch_DoesNotCaptureOnShutdown keeps a drain clean. Cancelling
// the context hands work back to the queue; that is not a failure and
// must not fill the dead-letter queue with jobs that were about to be
// judged perfectly well by the next worker.
func TestDispatch_DoesNotCaptureOnShutdown(t *testing.T) {
	d, ack := delivery(t, true)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	dispatch(ctx, d, func(context.Context, queue.Job) error {
		return errors.New("interrupted")
	}, noCapture(t))

	assert.True(t, ack.nacked)
	assert.True(t, ack.requeue)
}

// =====================================================================
// dispatch: malformed jobs
// =====================================================================

// TestDispatch_CapturesAMalformedJob covers the classic poison message.
// A body that will never decode cannot be retried, but throwing it away
// unseen means nobody can ever find out what produced it.
func TestDispatch_CapturesAMalformedJob(t *testing.T) {
	ack := &fakeAcknowledger{}
	d := amqp.Delivery{Acknowledger: ack, Body: []byte("{not json")}
	capture, calls := recorder(nil)

	dispatch(context.Background(), d, func(context.Context, queue.Job) error {
		t.Fatal("the handler must not see a malformed job")
		return nil
	}, capture)

	require.Len(t, *calls, 1)
	assert.Equal(t, deadLetterReasonMalformed, (*calls)[0].reason)
	assert.Equal(t, []byte("{not json"), (*calls)[0].body,
		"the raw body is preserved so it can actually be diagnosed")
	assert.True(t, ack.acked)
}

// TestDispatch_MalformedFallsBackToDiscard mirrors the exhausted case:
// an unpublishable capture must not turn into an unsettled delivery.
func TestDispatch_MalformedFallsBackToDiscard(t *testing.T) {
	ack := &fakeAcknowledger{}
	d := amqp.Delivery{Acknowledger: ack, Body: []byte("{not json")}
	capture, _ := recorder(errors.New("broker gone"))

	dispatch(context.Background(), d, func(context.Context, queue.Job) error { return nil }, capture)

	assert.True(t, ack.nacked)
	assert.False(t, ack.requeue)
}

// =====================================================================
// Headers
// =====================================================================

// TestDeadLetterHeaders checks that a capture carries enough context to
// be worth keeping: which lane it came from, why it died, and what the
// judge said. The error text is a judging error; the broker URL is never
// part of it, and nothing here may leak credentials.
func TestDeadLetterHeaders(t *testing.T) {
	headers := deadLetterHeaders("judge.warroom", deadLetterReasonExhausted,
		errors.New("sandbox exited 137"))

	assert.Equal(t, "judge.warroom", headers[headerOriginalQueue])
	assert.Equal(t, deadLetterReasonExhausted, headers[headerReason])
	assert.Equal(t, "sandbox exited 137", headers[headerError])
	assert.Equal(t, maxAttempts, headers[headerAttempts])

	at, ok := headers[headerDeadLetteredAt].(string)
	require.True(t, ok, "the capture is timestamped")
	_, err := time.Parse(time.RFC3339Nano, at)
	assert.NoError(t, err)
}

// TestDeadLetterHeadersTolerateANilCause covers the malformed path,
// where there is a decode error but the shape is the same.
func TestDeadLetterHeadersTolerateANilCause(t *testing.T) {
	headers := deadLetterHeaders("judge.standard", deadLetterReasonMalformed, nil)

	assert.Equal(t, "", headers[headerError])
	assert.Equal(t, deadLetterReasonMalformed, headers[headerReason])
}
