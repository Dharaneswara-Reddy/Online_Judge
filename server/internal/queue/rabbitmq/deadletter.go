package rabbitmq

// Dead-lettering: where a job goes when the judge has given up on it.
//
// # Why this is done in the application rather than by the broker
//
// The obvious implementation is `x-dead-letter-exchange` as a queue
// argument, and it is the one RabbitMQ documents first. It is not
// available to us. `judge.standard` and `judge.warroom` are already
// durable on the production broker, declared with no arguments, and
// RabbitMQ compares a redeclaration's arguments against the existing
// queue: adding one is answered with
//
//	PRECONDITION_FAILED - inequivalent arg 'x-dead-letter-exchange'
//
// which closes the channel. Since declareTopology runs on every publish
// channel and every consume channel, and again after every reconnect, a
// queue argument would take the channel down each time it ran — judging
// would stop. Renaming the lanes to `.v2` would dodge that, at the price
// of a multi-deploy cutover (consumers, then producers, then a drain,
// then a delete) during which two topologies are live and a mistake
// strands real submissions.
//
// So the capture is published here instead, by the worker that gave up.
// The existing lane declarations are left byte-identical, every new
// object has a name the production broker has never seen, and the
// behaviour is therefore the same on a fresh dev machine, in CI, and in
// production, with no operator action required to switch it on.
//
// An operator may *additionally* apply a broker policy setting
// `dead-letter-exchange` on the lanes — a policy changes a live queue
// without redeclaring it, so it does not collide with the declarations
// here. That is a backstop for the drops only the broker can see (a TTL
// expiry, an overflow), and it cannot produce duplicates: a job captured
// below is acknowledged, and an acknowledgement is not a rejection, so
// the broker never dead-letters a second copy. The routing key used here
// is deliberately the source queue's name, which is exactly what
// RabbitMQ itself would use, so both routes land in the same queue.

import (
	"context"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/toji339/online-judge/internal/queue"
)

// deadLetterExchange carries captures. It is a direct exchange keyed by
// the queue a job came from, matching RabbitMQ's own dead-letter routing.
const deadLetterExchange = "judge.dlx"

// deadLetterQueues maps a lane to the queue holding its captures. The
// lanes are kept apart here for the same reason they are kept apart in
// the first place: a War Room failure needs triaging during a live race
// and must not be buried under practice traffic.
var deadLetterQueues = map[queue.Lane]string{
	queue.LaneStandard: "judge.dead.standard",
	queue.LaneWarRoom:  "judge.dead.warroom",
}

// Bounds on a dead-letter queue.
//
// The judging lanes are deliberately left unbounded. Publish does not use
// publisher confirms, so an `x-max-length` on a lane would either drop the
// oldest queued submission (the default drop-head) or silently discard the
// newest one (reject-publish, whose rejection nobody is listening for).
// Both lose a submission that a user is waiting on, which is the exact
// outcome this change exists to prevent.
//
// A dead-letter queue can be bounded safely because its contents are
// copies. The submission row is already in Mongo and the stale-submission
// reaper already owns its fate; losing the oldest capture under extreme
// pressure costs a diagnostic, not a user's work.
const (
	// deadLetterMaxLength caps each lane's capture queue.
	//
	// The submission service admits one in-flight submission per user, so
	// the number of jobs that can be failing simultaneously is bounded by
	// active users. Five figures is far beyond any plausible incident, and
	// at a few hundred bytes per capture it is single-digit megabytes of
	// broker memory per lane.
	deadLetterMaxLength = 10000

	// deadLetterTTL expires a capture.
	//
	// A capture is a pointer to a Mongo row the reaper has long since
	// resolved; after a fortnight it has no diagnostic value. Expiry is
	// also what guarantees these queues cannot become permanent broker
	// memory if nobody ever drains them.
	deadLetterTTL = 14 * 24 * time.Hour
)

// maxAttempts is how many times a job is delivered before its retry
// budget is spent: the first delivery plus the single redelivery that
// decideAck allows.
const maxAttempts int32 = 2

// Why a job was captured.
const (
	// deadLetterReasonExhausted means judging failed twice.
	deadLetterReasonExhausted = "retry-exhausted"
	// deadLetterReasonMalformed means the body never decoded, so no
	// number of retries could have helped.
	deadLetterReasonMalformed = "malformed"
)

// Headers carried by a capture. They are prefixed so they cannot collide
// with the `x-death` table RabbitMQ writes when a broker policy is also
// in force.
const (
	headerOriginalQueue  = "x-judge-original-queue"
	headerReason         = "x-judge-dead-letter-reason"
	headerError          = "x-judge-error"
	headerAttempts       = "x-judge-attempts"
	headerDeadLetteredAt = "x-judge-dead-lettered-at"
)

// captureDeadLetterTimeout bounds the publish of one capture.
//
// It is short and independent of the caller's context: capture runs on a
// consumer slot, and it also has to work during a drain, when the
// worker's root context is already cancelled.
const captureDeadLetterTimeout = 5 * time.Second

// captureFunc publishes one job to the dead-letter exchange. dispatch
// takes it as a parameter rather than reaching for a Client, so the
// settle-after-capture ordering can be tested without a broker.
type captureFunc func(ctx context.Context, body []byte, reason string, cause error) error

// deadLetterExchangeName resolves the capture exchange, applying the test
// namespace so a test cannot publish into the real one.
func (c *Client) deadLetterExchangeName() string {
	if c.namespace == "" {
		return deadLetterExchange
	}
	return c.namespace + "." + deadLetterExchange
}

// DeadLetterQueueName returns the queue holding captures for a lane, or
// false for an unknown lane.
//
// It is exported because the name is operational: the runbook names it,
// and a triage tool needs to reach it.
func (c *Client) DeadLetterQueueName(lane queue.Lane) (string, bool) {
	base, ok := deadLetterQueues[lane]
	if !ok {
		return "", false
	}
	if c.namespace == "" {
		return base, true
	}
	return c.namespace + "." + base, true
}

// QueueName returns the physical queue backing a lane, or false for an
// unknown lane.
//
// It is exported for the same reason as DeadLetterQueueName: the queue
// name is the dead-letter routing key, so anything inspecting captures
// needs it.
func (c *Client) QueueName(lane queue.Lane) (string, bool) {
	return c.queueName(lane)
}

// deadLetterArgs are the arguments every capture queue is declared with.
//
// These queues are new names, so declaring them with arguments can never
// hit the PRECONDITION_FAILED that rules the arguments out on the lanes.
func deadLetterArgs() amqp.Table {
	return amqp.Table{
		"x-max-length":  int32(deadLetterMaxLength),
		"x-overflow":    "drop-head",
		"x-message-ttl": deadLetterTTL.Milliseconds(),
	}
}

// deadLetterHeaders describe why a job was captured.
//
// cause is a judging error. The broker URL is never part of one, and
// nothing here may carry a credential.
func deadLetterHeaders(originalQueue, reason string, cause error) amqp.Table {
	text := ""
	if cause != nil {
		text = cause.Error()
	}
	return amqp.Table{
		headerOriginalQueue:  originalQueue,
		headerReason:         reason,
		headerError:          text,
		headerAttempts:       maxAttempts,
		headerDeadLetteredAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
}

// declareDeadLetterTopology declares the capture exchange, one queue per
// lane, and the bindings between them.
//
// Declarations are idempotent, so this runs alongside the lane declares
// on every channel and after every reconnect. A broker that has never
// seen this topology gets it on first connect, which is what keeps a
// fresh dev machine working without any migration step.
func (c *Client) declareDeadLetterTopology(ch *amqp.Channel) error {
	exchange := c.deadLetterExchangeName()

	if err := ch.ExchangeDeclare(
		exchange,
		"direct", // routed by the source queue's name
		true,     // durable — captures must survive a broker restart
		false,    // do not auto-delete
		false,    // not internal: the application publishes to it directly
		false,    // wait for confirmation
		nil,
	); err != nil {
		return fmt.Errorf("declare dead-letter exchange %s: %w", exchange, err)
	}

	for lane := range deadLetterQueues {
		name, _ := c.DeadLetterQueueName(lane)
		source, ok := c.queueName(lane)
		if !ok {
			return fmt.Errorf("no source queue for lane %q", lane)
		}

		if _, err := ch.QueueDeclare(
			name,
			true,  // durable
			false, // do not auto-delete
			false, // not exclusive
			false, // wait for confirmation
			deadLetterArgs(),
		); err != nil {
			return fmt.Errorf("declare dead-letter queue %s: %w", name, err)
		}

		// The binding key is the source queue name, which is both the
		// routing key captureDeadLetter publishes with and the one
		// RabbitMQ would use if a policy dead-lettered the same job.
		if err := ch.QueueBind(name, source, exchange, false, nil); err != nil {
			return fmt.Errorf("bind dead-letter queue %s: %w", name, err)
		}
	}
	return nil
}

// captureDeadLetter publishes one job to the dead-letter exchange.
//
// It publishes on the shared publish channel under the same lock Publish
// uses, because a channel is not safe for concurrent publishes and
// captures are raised from handler goroutines.
//
// A failure here is reported rather than retried. The caller settles the
// delivery only on success, so an unpublishable capture falls back to the
// pre-existing discard instead of leaving a delivery unsettled.
func (c *Client) captureDeadLetter(ctx context.Context, lane queue.Lane, body []byte, reason string, cause error) error {
	source, ok := c.queueName(lane)
	if !ok {
		return fmt.Errorf("unknown lane %q", lane)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrClosed
	}
	if c.pub == nil || c.conn == nil || c.conn.IsClosed() {
		return queue.ErrUnavailable
	}

	return c.pub.PublishWithContext(ctx,
		c.deadLetterExchangeName(),
		source, // routing key — the queue the job came from
		false,  // not mandatory: an unroutable capture must not block judging
		false,  // not immediate
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Headers:      deadLetterHeaders(source, reason, cause),
			Body:         body,
		},
	)
}

// captureFor binds captureDeadLetter to one lane, giving dispatch the
// narrow function it needs.
//
// The capture runs on a context detached from the caller's: a drain
// cancels the root context, and a job that is being given up on during
// shutdown should still be captured rather than silently dropped.
func (c *Client) captureFor(lane queue.Lane) captureFunc {
	return func(ctx context.Context, body []byte, reason string, cause error) error {
		pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), captureDeadLetterTimeout)
		defer cancel()
		return c.captureDeadLetter(pubCtx, lane, body, reason, cause)
	}
}

// DeleteDeadLetterTopology removes this client's capture exchange and
// queues. Like DeleteQueues it is only meaningful for a namespaced
// client, and exists so a test leaves no topology behind.
func (c *Client) DeleteDeadLetterTopology(ctx context.Context) error {
	if c.namespace == "" {
		return fmt.Errorf("refusing to delete the shared dead-letter topology")
	}

	conn, err := c.ensureConnection(ctx)
	if err != nil {
		return err
	}
	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("open channel for cleanup: %w", err)
	}
	defer ch.Close()

	for lane := range deadLetterQueues {
		name, _ := c.DeadLetterQueueName(lane)
		if _, err := ch.QueueDelete(name, false, false, false); err != nil {
			return fmt.Errorf("delete dead-letter queue %s: %w", name, err)
		}
	}
	if err := ch.ExchangeDelete(c.deadLetterExchangeName(), false, false); err != nil {
		return fmt.Errorf("delete dead-letter exchange: %w", err)
	}
	return nil
}
