// Package realtime carries events between processes that do not share
// memory: judge workers, and however many API instances hold a
// participant's WebSocket.
//
// A War Room's participants may be connected to different API instances,
// so an event published on one instance has to reach all of them. Redis
// Pub/Sub provides that fan-out: any process publishes to a channel keyed
// by room, and every process subscribed to that channel receives it.
package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/redis/go-redis/v9"
)

// Event is one broadcast message. Type tells the client how to read the
// payload; Payload is the already-encoded body.
type Event struct {
	Type    string          `json:"type"`
	RoomID  string          `json:"roomId"`
	Payload json.RawMessage `json:"payload"`
}

// Bus publishes events and subscribes to them.
//
// The interface exists so the War Room can be unit-tested against an
// in-memory implementation without a live Redis.
type Bus interface {
	// Publish sends an event to everyone subscribed to the room.
	Publish(ctx context.Context, roomID string, event Event) error
	// Subscribe delivers events for a room until the context is
	// cancelled. The returned cancel function unsubscribes.
	Subscribe(ctx context.Context, roomID string) (<-chan Event, func(), error)
	Close() error
}

// RedisBus is the production Bus.
type RedisBus struct {
	client *redis.Client
}

// ConnectRedis dials Redis and verifies the connection before returning.
func ConnectRedis(url string) (*RedisBus, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}

	client := redis.NewClient(opts)
	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return &RedisBus{client: client}, nil
}

// Client exposes the underlying Redis handle so other packages (the
// cache, the rate limiter) can reuse the same connection pool.
func (b *RedisBus) Client() *redis.Client { return b.client }

// channelFor namespaces a room's channel so unrelated keys cannot clash.
func channelFor(roomID string) string { return "warroom:" + roomID }

func (b *RedisBus) Publish(ctx context.Context, roomID string, event Event) error {
	event.RoomID = roomID
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}
	return b.client.Publish(ctx, channelFor(roomID), body).Err()
}

func (b *RedisBus) Subscribe(ctx context.Context, roomID string) (<-chan Event, func(), error) {
	sub := b.client.Subscribe(ctx, channelFor(roomID))

	// Wait for the subscription to be confirmed so no event published
	// immediately after this call is missed.
	if _, err := sub.Receive(ctx); err != nil {
		_ = sub.Close()
		return nil, nil, fmt.Errorf("subscribe to room %s: %w", roomID, err)
	}

	events := make(chan Event, 16)
	go func() {
		defer close(events)
		for msg := range sub.Channel() {
			var event Event
			if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
				log.Printf("realtime: dropping malformed event on %s: %v", msg.Channel, err)
				continue
			}
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
	}()

	cancel := func() { _ = sub.Close() }
	return events, cancel, nil
}

func (b *RedisBus) Close() error { return b.client.Close() }
