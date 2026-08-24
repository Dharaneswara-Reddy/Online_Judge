package rabbitmq

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/queue"
)

// fakeAcknowledger records how a delivery was settled, so acknowledgement
// policy can be tested without a broker.
type fakeAcknowledger struct {
	acked   bool
	nacked  bool
	requeue bool
}

func (f *fakeAcknowledger) Ack(uint64, bool) error { f.acked = true; return nil }

func (f *fakeAcknowledger) Nack(_ uint64, _ bool, requeue bool) error {
	f.nacked = true
	f.requeue = requeue
	return nil
}

func (f *fakeAcknowledger) Reject(_ uint64, requeue bool) error {
	f.nacked = true
	f.requeue = requeue
	return nil
}

func delivery(t *testing.T, redelivered bool) (amqp.Delivery, *fakeAcknowledger) {
	t.Helper()
	body, err := json.Marshal(queue.Job{SubmissionID: "sub-1", UserID: "user-1"})
	require.NoError(t, err)

	ack := &fakeAcknowledger{}
	return amqp.Delivery{Acknowledger: ack, Body: body, Redelivered: redelivered}, ack
}

func TestDispatch_AcksOnSuccess(t *testing.T) {
	d, ack := delivery(t, false)

	dispatch(context.Background(), d, func(context.Context, queue.Job) error { return nil }, noCapture(t))

	assert.True(t, ack.acked)
	assert.False(t, ack.nacked)
}

// TestDispatch_RequeuesAFailedJobOnce is the defect this guards. A
// handler that could not even load the submission has written no status,
// so discarding the message leaves the row pending forever — and the
// partial unique index then holds the user's only submission slot until
// somebody edits the database by hand.
//
// Any failure is retried, not just a marked one: the handler records a
// terminal state before returning whenever it knows the verdict, so a
// job that comes back is one that got far enough to be worth another
// attempt, and the conditional writes make a needless retry harmless.
func TestDispatch_RequeuesAFailedJobOnce(t *testing.T) {
	d, ack := delivery(t, false)

	dispatch(context.Background(), d, func(context.Context, queue.Job) error {
		return errors.New("mongo unreachable")
	}, noCapture(t))

	assert.True(t, ack.nacked)
	assert.True(t, ack.requeue, "a first failure goes back on the queue")
}

// TestDispatch_StopsRetryingARedeliveredJob bounds the retry. Requeueing
// is immediate, so an unbounded policy turns one poisonous message into
// a hot loop that starves every other submission.
func TestDispatch_StopsRetryingARedeliveredJob(t *testing.T) {
	d, ack := delivery(t, true)
	var captured []string

	dispatch(context.Background(), d, func(context.Context, queue.Job) error {
		return errors.New("load submission sub-1: connection refused")
	}, recordingCapture(&captured))

	// Acked, not nacked: the copy is safely filed on the dead-letter
	// queue, so removing the original is the correct settlement. Before
	// dead-lettering existed this was a discard, and the message was
	// simply gone.
	assert.True(t, ack.acked, "a captured job is acked once its copy is filed")
	assert.False(t, ack.requeue)
	assert.Equal(t, []string{deadLetterReasonExhausted}, captured,
		"a job that has spent its retries is dead-lettered rather than dropped silently")
}

func TestDispatch_DiscardsAMalformedJob(t *testing.T) {
	var captured []string
	ack := &fakeAcknowledger{}
	d := amqp.Delivery{Acknowledger: ack, Body: []byte("{not json")}

	dispatch(context.Background(), d, func(context.Context, queue.Job) error {
		t.Fatal("the handler must not see a malformed job")
		return nil
	}, recordingCapture(&captured))

	assert.True(t, ack.acked, "a captured job is acked once its copy is filed")
	assert.False(t, ack.requeue)
	assert.Equal(t, []string{deadLetterReasonMalformed}, captured,
		"a message we cannot even decode is the one most worth keeping a copy of")
}

// recordingCapture notes what was dead-lettered instead of failing on it.
// Two of the cases below now capture on purpose — a malformed job and one
// that has spent its retry budget are exactly the messages worth keeping
// a copy of, since nothing else records that they existed.
func recordingCapture(reasons *[]string) captureFunc {
	return func(_ context.Context, _ []byte, reason string, _ error) error {
		*reasons = append(*reasons, reason)
		return nil
	}
}
