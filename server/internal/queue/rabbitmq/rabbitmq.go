// Package rabbitmq is the production queue.Publisher and queue.Consumer,
// backed by RabbitMQ over AMQP.
package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/toji339/online-judge/internal/queue"
)

// queueNames maps a logical lane to its durable AMQP queue. Each lane is
// a separate queue so it can be consumed by its own worker pool.
var queueNames = map[queue.Lane]string{
	queue.LaneStandard: "judge.standard",
	queue.LaneWarRoom:  "judge.warroom",
}

// Client is a RabbitMQ connection that can both publish and consume.
type Client struct {
	conn *amqp.Connection

	mu          sync.Mutex
	publishChan *amqp.Channel
}

// Connect dials RabbitMQ, declares both lane queues, and returns a ready
// client. It returns queue.ErrUnavailable when the broker is unreachable
// so the caller can degrade gracefully instead of failing to start.
func Connect(url string) (*Client, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", queue.ErrUnavailable, err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w: open channel: %v", queue.ErrUnavailable, err)
	}

	// Declaring here means either process (API or worker) can start first.
	for _, name := range queueNames {
		if _, err := declareQueue(ch, name); err != nil {
			ch.Close()
			conn.Close()
			return nil, err
		}
	}

	return &Client{conn: conn, publishChan: ch}, nil
}

// declareQueue declares one durable lane queue idempotently.
func declareQueue(ch *amqp.Channel, name string) (amqp.Queue, error) {
	q, err := ch.QueueDeclare(
		name,
		true,  // durable — survive a broker restart
		false, // do not auto-delete when the last consumer leaves
		false, // not exclusive
		false, // wait for the declare to be confirmed
		nil,
	)
	if err != nil {
		return amqp.Queue{}, fmt.Errorf("declare queue %s: %w", name, err)
	}
	return q, nil
}

// Publish sends a job to the given lane. Messages are marked persistent
// so a broker restart cannot silently lose queued submissions.
func (c *Client) Publish(ctx context.Context, lane queue.Lane, job queue.Job) error {
	body, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("encode job: %w", err)
	}

	name, ok := queueNames[lane]
	if !ok {
		return fmt.Errorf("unknown lane %q", lane)
	}

	// One channel is not safe for concurrent publishes.
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.publishChan.PublishWithContext(ctx,
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

// Consume reads jobs from one lane and dispatches them to handler until
// the context is cancelled or the connection drops.
//
// prefetch bounds how many unacknowledged jobs this consumer holds. A
// value of 1 keeps the queue itself as the buffer, so a slow worker
// never hoards jobs a faster one could have taken.
func (c *Client) Consume(ctx context.Context, lane queue.Lane, prefetch int, handler queue.Handler) error {
	name, ok := queueNames[lane]
	if !ok {
		return fmt.Errorf("unknown lane %q", lane)
	}

	// Each consumer gets its own channel so lanes cannot block each other.
	ch, err := c.conn.Channel()
	if err != nil {
		return fmt.Errorf("open consume channel: %w", err)
	}
	defer ch.Close()

	if _, err := declareQueue(ch, name); err != nil {
		return err
	}
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

	log.Printf("worker: consuming lane %q (%d concurrent slots)", lane, prefetch)

	// Handlers run concurrently up to the prefetch count. Dispatching
	// inline instead would make each lane strictly serial, so one slow
	// submission would block every job queued behind it — and a prefetch
	// above one would merely hoard jobs an idle worker could have taken.
	slots := make(chan struct{}, prefetch)
	var wg sync.WaitGroup
	defer wg.Wait() // let in-flight judging finish before the channel closes

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case delivery, ok := <-deliveries:
			if !ok {
				return fmt.Errorf("%w: consumer channel closed", queue.ErrUnavailable)
			}

			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				// Shutting down: hand the job back so another worker takes it.
				_ = delivery.Nack(false, true)
				return ctx.Err()
			}

			wg.Add(1)
			go func(d amqp.Delivery) {
				defer wg.Done()
				defer func() { <-slots }()
				c.dispatch(ctx, d, handler)
			}(delivery)
		}
	}
}

// dispatch decodes one delivery and runs the handler, acknowledging the
// message according to the outcome.
func (c *Client) dispatch(ctx context.Context, delivery amqp.Delivery, handler queue.Handler) {
	var job queue.Job
	if err := json.Unmarshal(delivery.Body, &job); err != nil {
		// A malformed job can never succeed, so drop it rather than
		// letting it loop through the queue forever.
		log.Printf("worker: discarding malformed job: %v", err)
		_ = delivery.Nack(false, false)
		return
	}

	if err := handler(ctx, job); err != nil {
		// The handler already records the failure on the submission, so
		// requeuing would double-judge it. Drop the message instead.
		log.Printf("worker: job %s failed: %v", job.SubmissionID, err)
		_ = delivery.Nack(false, false)
		return
	}

	if err := delivery.Ack(false); err != nil {
		log.Printf("worker: could not ack job %s: %v", job.SubmissionID, err)
	}
}

// Close releases the publish channel and the underlying connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.publishChan != nil {
		_ = c.publishChan.Close()
	}
	return c.conn.Close()
}
