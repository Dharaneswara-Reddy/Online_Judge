// Package rabbitmq is the production queue.Publisher and queue.Consumer,
// backed by RabbitMQ over AMQP.
//
// The client owns the full connection lifecycle. An AMQP connection dies
// for ordinary reasons — a broker restart, a dropped TCP session, a
// channel-level protocol error — and none of them should require
// restarting the judge. Every operation therefore runs against a
// connection that is re-established on demand, and a consumer rebuilds
// its whole topology after a reconnect rather than assuming the old
// channel state survived.
package rabbitmq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/toji339/online-judge/internal/queue"
)

// queueNames maps a logical lane to its durable AMQP queue. Each lane is
// a separate queue so it can be consumed by its own worker pool.
var queueNames = map[queue.Lane]string{
	queue.LaneStandard: "judge.standard",
	queue.LaneWarRoom:  "judge.warroom",
}

// Reconnect backoff. Bounded and jittered: an unbounded retry rate would
// hammer a broker that is still starting up, and identical delays across
// several workers would make them all reconnect in lockstep.
const (
	minBackoff = 1 * time.Second
	maxBackoff = 30 * time.Second
	jitterFrac = 0.2
)

// dialTimeout bounds a single connection attempt.
const dialTimeout = 10 * time.Second

// ErrClosed is returned once the client has been shut down.
var ErrClosed = errors.New("queue client is closed")

// Client manages one AMQP connection and recovers it automatically.
//
// It is safe for concurrent use: publishing takes the channel lock, and
// connection replacement takes the connection lock.
type Client struct {
	url string

	// namespace prefixes every queue name. Production leaves it empty and
	// uses the real lane queues; tests set it so their traffic cannot be
	// consumed by a worker running against the same broker, and so
	// parallel tests cannot consume each other's messages.
	namespace string

	mu     sync.Mutex
	conn   *amqp.Connection
	pub    *amqp.Channel
	closed bool
}

// Connect dials RabbitMQ once and returns an error if the broker is
// unreachable, so a caller that can work without a queue — the API,
// which falls back to judging inline — can decide at startup.
//
// The returned client still recovers on its own if the connection drops
// later; only the first attempt is strict.
func Connect(url string) (*Client, error) {
	c := New(url)
	if err := c.reconnect(); err != nil {
		return nil, fmt.Errorf("%w: %v", queue.ErrUnavailable, err)
	}
	return c, nil
}

// New returns a client that connects lazily and never fails to be
// created. The judge worker uses this: it has nothing to do until the
// broker exists, so an unavailable broker at startup is a wait, not a
// fatal error.
func New(url string) *Client {
	return &Client{url: url}
}

// NewNamespaced returns a client whose queues are prefixed, isolating it
// from every other consumer of the same broker.
//
// This exists for tests: without it a recovery test shares the real lane
// queues with any locally running judge worker, which then steals its
// messages and makes the test fail for reasons unrelated to the code
// under test.
func NewNamespaced(url, namespace string) *Client {
	return &Client{url: url, namespace: namespace}
}

// queueName resolves a lane to its physical queue, applying the
// namespace when one is set.
func (c *Client) queueName(lane queue.Lane) (string, bool) {
	base, ok := queueNames[lane]
	if !ok {
		return "", false
	}
	if c.namespace == "" {
		return base, true
	}
	return c.namespace + "." + base, true
}

// DeleteQueues removes this client's queues from the broker. It is only
// meaningful for a namespaced client, and exists so a test can clean up
// the topology it created rather than leaving it behind.
func (c *Client) DeleteQueues(ctx context.Context) error {
	if c.namespace == "" {
		return fmt.Errorf("refusing to delete the shared lane queues")
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

	for lane := range queueNames {
		name, _ := c.queueName(lane)
		if _, err := ch.QueueDelete(name, false, false, false); err != nil {
			return fmt.Errorf("delete queue %s: %w", name, err)
		}
	}
	return nil
}

// reconnect establishes a fresh connection and publish channel, and
// declares the lane queues. Any previous connection is discarded.
func (c *Client) reconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrClosed
	}

	// Drop whatever we had. Errors here are expected — the connection is
	// usually already dead, which is why we are reconnecting.
	if c.pub != nil {
		_ = c.pub.Close()
		c.pub = nil
	}
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}

	conn, err := amqp.DialConfig(c.url, amqp.Config{
		Dial:      amqp.DefaultDial(dialTimeout),
		Heartbeat: 10 * time.Second,
	})
	if err != nil {
		return err
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("open publish channel: %w", err)
	}

	// Declaring here means either process can start first, and a broker
	// that lost its definitions gets them back on reconnect.
	if err := c.declareTopology(ch); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return err
	}

	c.conn = conn
	c.pub = ch
	log.Println("rabbitmq: connection established")
	return nil
}

// declareTopology declares every lane queue. Declarations are idempotent,
// so redeclaring after a reconnect is safe and cheap.
//
// The default exchange is used deliberately: each lane routes straight to
// a queue by name, so no exchange or binding of our own is needed.
func (c *Client) declareTopology(ch *amqp.Channel) error {
	for lane := range queueNames {
		name, _ := c.queueName(lane)
		_, err := ch.QueueDeclare(
			name,
			true,  // durable — survive a broker restart
			false, // do not auto-delete when the last consumer leaves
			false, // not exclusive
			false, // wait for the declare to be confirmed
			nil,
		)
		if err != nil {
			return fmt.Errorf("declare queue %s: %w", name, err)
		}
	}
	return nil
}

// live returns the current connection and publish channel if the
// connection is up, or nil when it needs re-establishing.
func (c *Client) live() (*amqp.Connection, *amqp.Channel) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil || c.conn.IsClosed() {
		return nil, nil
	}
	return c.conn, c.pub
}

// ensureConnection blocks until a connection is available, retrying with
// bounded, jittered backoff. It returns an error only when the context
// is cancelled or the client is closed — never because the broker is
// merely down.
func (c *Client) ensureConnection(ctx context.Context) (*amqp.Connection, error) {
	attempt := 0
	for {
		if conn, _ := c.live(); conn != nil {
			return conn, nil
		}

		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		if closed {
			return nil, ErrClosed
		}

		if err := c.reconnect(); err == nil {
			conn, _ := c.live()
			if conn != nil {
				if attempt > 0 {
					log.Printf("rabbitmq: reconnect succeeded after %d attempt(s)", attempt)
				}
				return conn, nil
			}
		} else {
			attempt++
			delay := backoffFor(attempt)
			// The URL carries credentials, so it is never logged.
			log.Printf("rabbitmq: reconnect attempt %d failed (%v), retrying in %s", attempt, err, delay.Round(time.Millisecond))

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
	}
}

// backoffFor returns the delay before attempt n, doubling up to a ceiling
// and then applying jitter so several workers do not retry in lockstep.
func backoffFor(attempt int) time.Duration {
	delay := minBackoff
	for range attempt - 1 {
		delay *= 2
		if delay >= maxBackoff {
			delay = maxBackoff
			break
		}
	}

	jitter := (rand.Float64()*2 - 1) * jitterFrac * float64(delay)
	delayed := time.Duration(float64(delay) + jitter)
	if delayed < minBackoff {
		delayed = minBackoff
	}
	return delayed
}

// Publish sends a job to the given lane. Messages are marked persistent
// so a broker restart cannot silently lose queued submissions.
//
// If the connection is down it reports queue.ErrUnavailable rather than
// blocking, letting the API fall back to judging inline instead of making
// a user wait on a broker that may be gone for minutes.
func (c *Client) Publish(ctx context.Context, lane queue.Lane, job queue.Job) error {
	body, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("encode job: %w", err)
	}

	name, ok := c.queueName(lane)
	if !ok {
		return fmt.Errorf("unknown lane %q", lane)
	}

	publish := func() error {
		c.mu.Lock()
		defer c.mu.Unlock()

		if c.closed {
			return ErrClosed
		}
		if c.pub == nil || c.conn == nil || c.conn.IsClosed() {
			return queue.ErrUnavailable
		}

		// One channel is not safe for concurrent publishes, which the
		// lock above already serialises.
		return c.pub.PublishWithContext(ctx,
			"",    // default exchange — route straight to the named queue
			name,  // routing key is the queue name
			false, // not mandatory
			false, // not immediate
			amqp.Publishing{
				ContentType:  "application/json",
				DeliveryMode: amqp.Persistent,
				Body:         body,
			},
		)
	}

	err = publish()
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrClosed) {
		return err
	}

	// One retry after a re-dial covers the common case of publishing into
	// a connection that died since the last message.
	if rErr := c.reconnect(); rErr != nil {
		return fmt.Errorf("%w: %v", queue.ErrUnavailable, rErr)
	}
	return publish()
}

// Consume reads jobs from one lane and dispatches them to handler,
// re-establishing the connection, channel, topology and consumer for as
// long as the context is live.
//
// It returns only when the context is cancelled or the client is closed;
// a broker failure is handled internally, so the worker process never
// needs restarting to resume judging.
//
// prefetch bounds both unacknowledged deliveries and concurrent handlers,
// so the queue stays the buffer and a worker never holds more work than
// it can actually run.
func (c *Client) Consume(ctx context.Context, lane queue.Lane, prefetch int, handler queue.Handler) error {
	if _, ok := c.queueName(lane); !ok {
		return fmt.Errorf("unknown lane %q", lane)
	}

	for {
		conn, err := c.ensureConnection(ctx)
		if err != nil {
			return err
		}

		err = c.consumeOnce(ctx, conn, lane, prefetch, handler)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, ErrClosed) {
			return err
		}

		log.Printf("rabbitmq: consumer for lane %q stopped (%v), re-establishing", lane, err)
	}
}

// consumeOnce runs a consumer against one connection and returns when
// that connection or channel fails, or the context ends. Everything the
// consumer depends on — channel, topology, QoS, consumer registration —
// is created here, so a reconnect rebuilds all of it.
func (c *Client) consumeOnce(ctx context.Context, conn *amqp.Connection, lane queue.Lane, prefetch int, handler queue.Handler) error {
	name, _ := c.queueName(lane)

	// Each consumer gets its own channel so lanes cannot block each other.
	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("open consume channel: %w", err)
	}
	defer ch.Close()

	if err := c.declareTopology(ch); err != nil {
		return err
	}
	log.Printf("rabbitmq: topology declared for lane %q", lane)

	if err := ch.Qos(prefetch, 0, false); err != nil {
		return fmt.Errorf("set qos: %w", err)
	}

	deliveries, err := ch.Consume(
		name,
		"",    // let the broker generate a consumer tag
		false, // manual acknowledgement — we ack only after judging
		false, // not exclusive
		false, // no-local is unsupported by RabbitMQ
		false, // wait for the consume to be confirmed
		nil,
	)
	if err != nil {
		return fmt.Errorf("consume %s: %w", name, err)
	}

	// Watch for the two ways a consumer dies without the delivery channel
	// necessarily telling us why: the channel erroring out, and the broker
	// cancelling our consumer (which happens if the queue is deleted).
	closed := ch.NotifyClose(make(chan *amqp.Error, 1))
	cancelled := ch.NotifyCancel(make(chan string, 1))

	log.Printf("rabbitmq: consumer started on lane %q (%d concurrent slots)", lane, prefetch)

	// Handlers run concurrently up to the prefetch count. Dispatching
	// inline would make each lane strictly serial, so one slow submission
	// would block every job queued behind it.
	slots := make(chan struct{}, prefetch)
	var wg sync.WaitGroup
	// In-flight judging finishes before the channel closes, so results are
	// still acknowledged on the connection they arrived on.
	defer wg.Wait()

	for {
		select {
		case <-ctx.Done():
			log.Printf("rabbitmq: shutdown started, draining lane %q", lane)
			return ctx.Err()

		case amqpErr := <-closed:
			return fmt.Errorf("channel closed: %v", amqpErr)

		case tag := <-cancelled:
			return fmt.Errorf("consumer %q cancelled by broker", tag)

		case delivery, ok := <-deliveries:
			if !ok {
				return fmt.Errorf("%w: delivery channel closed", queue.ErrUnavailable)
			}

			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				// Shutting down before we could start: hand it straight
				// back so another worker takes it.
				_ = delivery.Nack(false, true)
				return ctx.Err()
			}

			wg.Add(1)
			go func(d amqp.Delivery) {
				defer wg.Done()
				defer func() { <-slots }()
				dispatch(ctx, d, handler)
			}(delivery)
		}
	}
}

// ackAction is what to do with a delivery once its handler has returned.
type ackAction int

const (
	// ackDone acknowledges the delivery; the broker forgets it.
	ackDone ackAction = iota
	// ackRequeue hands the job back so it is delivered again.
	ackRequeue
	// ackDiscard drops the job without retrying it.
	ackDiscard
)

// decideAck is the redelivery policy, kept separate from the AMQP calls
// so it can be tested directly.
//
// A failed job used to be discarded outright, on the assumption that the
// handler had already recorded a terminal state on the submission. That
// assumption does not hold for the failure that matters most: if the
// handler could not even read the submission, nothing was written, the
// row stays pending, and — because the admission-control index covers
// pending rows — its owner can never submit again.
//
// So a failure is retried, but exactly once. RabbitMQ marks the second
// delivery of a message Redelivered, which bounds the retry without any
// state of our own and stops a poison message looping forever. Anything
// still failing after that is dropped and left to the stale-submission
// reaper, which is the backstop for a row nothing will ever finish.
func decideAck(handlerErr error, shuttingDown, redelivered bool) ackAction {
	switch {
	case handlerErr == nil:
		return ackDone
	case shuttingDown:
		// The handler deliberately leaves the submission non-terminal
		// when judging is interrupted, so the job must go back.
		return ackRequeue
	case !redelivered:
		return ackRequeue
	default:
		return ackDiscard
	}
}

// dispatch decodes one delivery, runs the handler, and acknowledges
// according to the outcome.
//
// Delivery semantics are at-least-once: a message is acknowledged only
// after the handler has returned successfully, so a worker that dies
// mid-judge leaves the message unacknowledged and the broker redelivers
// it. The judging pipeline is idempotent — the claim and the verdict are
// both conditional writes — so a duplicate delivery cannot double-judge
// or double-count a solve.
func dispatch(ctx context.Context, delivery amqp.Delivery, handler queue.Handler) {
	var job queue.Job
	if err := json.Unmarshal(delivery.Body, &job); err != nil {
		// A malformed job can never succeed, so drop it rather than
		// letting it loop through the queue forever.
		log.Printf("rabbitmq: discarding malformed job: %v", err)
		_ = delivery.Nack(false, false)
		return
	}

	err := handler(ctx, job)

	switch decideAck(err, ctx.Err() != nil, delivery.Redelivered) {
	case ackDone:
		if ackErr := delivery.Ack(false); ackErr != nil {
			// The verdict is already recorded, so the worst case is a
			// redelivery that the conditional writes refuse.
			log.Printf("rabbitmq: could not ack job %s: %v", job.SubmissionID, ackErr)
		}

	case ackRequeue:
		log.Printf("rabbitmq: requeueing job %s for one more attempt: %v", job.SubmissionID, err)
		if nackErr := delivery.Nack(false, true); nackErr != nil {
			log.Printf("rabbitmq: could not requeue job %s: %v", job.SubmissionID, nackErr)
		}

	case ackDiscard:
		log.Printf("ERROR: rabbitmq: job %s failed twice, dropping it: %v", job.SubmissionID, err)
		_ = delivery.Nack(false, false)
	}
}

// Close shuts the client down and releases the connection. Consumers
// return once their in-flight work has finished.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true
	log.Println("rabbitmq: shutdown started")

	if c.pub != nil {
		_ = c.pub.Close()
		c.pub = nil
	}
	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}
