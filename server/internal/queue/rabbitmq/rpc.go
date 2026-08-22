package rabbitmq

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/toji339/online-judge/internal/queue"
)

// rpcQueue is the durable queue carrying playground run requests.
//
// It is separate from the judging lanes on purpose. A playground run has
// someone watching a spinner, while a queued submission does not, and a
// backlog of one must never delay the other.
const rpcQueue = "judge.playground"

// replyPseudoQueue is RabbitMQ's direct reply-to feature. Consuming from
// it costs nothing — no queue is declared and none has to be cleaned up
// — and replies are routed straight back to this channel. A dedicated
// reply queue per call would leave litter behind whenever an API process
// died mid-request.
const replyPseudoQueue = "amq.rabbitmq.reply-to"

// rpcQueueName applies the test namespace, matching the lane queues.
func (c *Client) rpcQueueName() string {
	if c.namespace == "" {
		return rpcQueue
	}
	return c.namespace + "." + rpcQueue
}

// correlationID returns a unique token used to match a reply to its
// request.
func correlationID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Call publishes a request and blocks until a worker replies or the
// context deadline passes.
//
// Each call takes its own channel. That is a little more work per call
// than sharing one, but a playground run is rate limited and infrequent,
// and an isolated channel means one failed call cannot disturb another —
// nor can a late reply be delivered to the wrong waiter.
func (c *Client) Call(ctx context.Context, payload []byte) ([]byte, error) {
	conn, err := c.ensureConnection(ctx)
	if err != nil {
		return nil, err
	}

	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("open rpc channel: %w", err)
	}
	defer ch.Close()

	name := c.rpcQueueName()
	if _, err := ch.QueueDeclare(name, true, false, false, false, nil); err != nil {
		return nil, fmt.Errorf("declare rpc queue: %w", err)
	}

	// Consume the reply before publishing, so a fast worker cannot answer
	// before anyone is listening.
	replies, err := ch.Consume(replyPseudoQueue, "", true, false, false, false, nil)
	if err != nil {
		return nil, fmt.Errorf("consume replies: %w", err)
	}

	corr, err := correlationID()
	if err != nil {
		return nil, err
	}

	// Expire the message alongside the caller's deadline. Without this a
	// request published while every worker is down would sit in the queue
	// and run much later, against a caller that stopped waiting long ago.
	pub := amqp.Publishing{
		ContentType:   "application/json",
		CorrelationId: corr,
		ReplyTo:       replyPseudoQueue,
		Body:          payload,
	}
	if deadline, ok := ctx.Deadline(); ok {
		if ttl := time.Until(deadline); ttl > 0 {
			pub.Expiration = strconv.FormatInt(ttl.Milliseconds(), 10)
		}
	}

	if err := ch.PublishWithContext(ctx, "", name, false, false, pub); err != nil {
		return nil, fmt.Errorf("publish rpc request: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			// Either no worker took the job, or one is still running it.
			// The caller cannot tell the difference, which is why a
			// playground run must stay free of side effects.
			return nil, queue.ErrNoWorker
		case d, ok := <-replies:
			if !ok {
				return nil, errors.New("rpc reply channel closed")
			}
			if d.CorrelationId != corr {
				// A reply to an abandoned call on a reused channel.
				continue
			}
			if errText := d.Headers["x-error"]; errText != nil {
				return nil, fmt.Errorf("worker: %v", errText)
			}
			return d.Body, nil
		}
	}
}

// Respond serves playground calls until the context is cancelled,
// re-establishing itself after a connection failure exactly as the lane
// consumers do.
func (c *Client) Respond(ctx context.Context, concurrency int, handler queue.RPCHandler) error {
	for {
		conn, err := c.ensureConnection(ctx)
		if err != nil {
			return err
		}

		err = c.respondOnce(ctx, conn, concurrency, handler)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, ErrClosed) {
			return err
		}

		log.Printf("rabbitmq: playground responder stopped (%v), re-establishing", err)
	}
}

func (c *Client) respondOnce(ctx context.Context, conn *amqp.Connection, concurrency int, handler queue.RPCHandler) error {
	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("open responder channel: %w", err)
	}
	defer ch.Close()

	name := c.rpcQueueName()
	if _, err := ch.QueueDeclare(name, true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare rpc queue: %w", err)
	}
	if err := ch.Qos(concurrency, 0, false); err != nil {
		return fmt.Errorf("set qos: %w", err)
	}

	deliveries, err := ch.Consume(name, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("consume rpc queue: %w", err)
	}

	closed := ch.NotifyClose(make(chan *amqp.Error, 1))
	log.Printf("rabbitmq: playground responder started (%d concurrent slots)", concurrency)

	// A semaphore rather than one goroutine per delivery: prefetch bounds
	// what the broker hands over, but nothing else would bound how many
	// containers this worker starts at once.
	slots := make(chan struct{}, concurrency)

	// Drain before the channel closes. Deferred calls run last-in-first-out,
	// so this waits for in-flight runs *before* ch.Close() above tears the
	// channel out from under them — otherwise a shutdown would abandon a
	// container mid-execution and never acknowledge the request.
	var inflight sync.WaitGroup
	defer inflight.Wait()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case amqpErr := <-closed:
			return fmt.Errorf("channel closed: %w", amqpErr)

		case d, ok := <-deliveries:
			if !ok {
				return errors.New("rpc deliveries closed")
			}
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				_ = d.Nack(false, true)
				return ctx.Err()
			}
			inflight.Add(1)
			go func(d amqp.Delivery) {
				defer inflight.Done()
				defer func() { <-slots }()
				c.answer(ctx, ch, d, handler)
			}(d)
		}
	}
}

// maxHandlerRuntime is the ceiling on one synchronous handler
// invocation.
//
// Without it the handler ran on the responder's root context, which
// lives as long as the worker process: one request could hold a
// concurrency slot — and the CPU behind it — indefinitely, which on a
// two-vCPU host is most of the machine. Every caller of this queue is a
// browser waiting on a spinner (the API gives up after 45 seconds), so
// anything past that is compute spent on an answer nobody reads.
const maxHandlerRuntime = 45 * time.Second

// handlerBudget decides how long one invocation may run.
//
// The publisher stamps the message's expiry from its own deadline, which
// is useful information: a caller that will stop waiting in five seconds
// should not cost fifty seconds of CPU. It is also untrusted, so it can
// only shorten the budget — never extend it, and never zero it.
func handlerBudget(expiration string) time.Duration {
	ms, err := strconv.ParseInt(expiration, 10, 64)
	if err != nil || ms <= 0 {
		return maxHandlerRuntime
	}
	if d := time.Duration(ms) * time.Millisecond; d < maxHandlerRuntime {
		return d
	}
	return maxHandlerRuntime
}

// answer runs one handler and publishes its result back to the caller.
//
// The delivery is always acknowledged, even when the handler fails. A
// playground run is not worth redelivering: the caller has a deadline and
// has very likely already given up, and a poisonous request would
// otherwise be redelivered forever.
func (c *Client) answer(ctx context.Context, ch *amqp.Channel, d amqp.Delivery, handler queue.RPCHandler) {
	defer func() {
		if err := d.Ack(false); err != nil {
			log.Printf("rabbitmq: ack playground request: %v", err)
		}
	}()

	// The handler gets a bounded context, not the responder's root one.
	// It still cancels with the root context on shutdown, so a drain is
	// unaffected.
	handlerCtx, cancelHandler := context.WithTimeout(ctx, handlerBudget(d.Expiration))
	defer cancelHandler()

	body, err := handler(handlerCtx, d.Body)

	reply := amqp.Publishing{
		ContentType:   "application/json",
		CorrelationId: d.CorrelationId,
	}
	if err != nil {
		reply.Headers = amqp.Table{"x-error": err.Error()}
	} else {
		reply.Body = body
	}

	if d.ReplyTo == "" {
		return
	}

	// Reply on a short, independent deadline: the caller's context is not
	// available here, and a blocked publish would hold a concurrency slot.
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	if err := ch.PublishWithContext(pubCtx, "", d.ReplyTo, false, false, reply); err != nil {
		// Nothing to recover: the caller is gone or the channel is dying.
		log.Printf("rabbitmq: publish playground reply: %v", err)
	}
}
