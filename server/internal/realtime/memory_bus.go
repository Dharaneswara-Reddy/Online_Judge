package realtime

import (
	"context"
	"sync"
)

// MemoryBus is an in-process Bus used by unit tests and as a fallback
// when Redis is unavailable.
//
// It fans events out to subscribers inside one process only, so a
// deployment running several API instances still needs Redis for a War
// Room to stay in sync across them.
type MemoryBus struct {
	mu     sync.Mutex
	nextID int
	rooms  map[string]map[int]chan Event
}

// NewMemoryBus creates an empty in-process bus.
func NewMemoryBus() *MemoryBus {
	return &MemoryBus{rooms: make(map[string]map[int]chan Event)}
}

func (b *MemoryBus) Publish(_ context.Context, roomID string, event Event) error {
	event.RoomID = roomID

	b.mu.Lock()
	defer b.mu.Unlock()

	for _, ch := range b.rooms[roomID] {
		// Never block on a subscriber that has stopped reading — dropping
		// an update is better than stalling the judge that published it.
		select {
		case ch <- event:
		default:
		}
	}
	return nil
}

func (b *MemoryBus) Subscribe(_ context.Context, roomID string) (<-chan Event, func(), error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.rooms[roomID] == nil {
		b.rooms[roomID] = make(map[int]chan Event)
	}
	b.nextID++
	id := b.nextID
	ch := make(chan Event, 16)
	b.rooms[roomID][id] = ch

	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if subs, ok := b.rooms[roomID]; ok {
			if existing, ok := subs[id]; ok {
				delete(subs, id)
				close(existing)
			}
			if len(subs) == 0 {
				delete(b.rooms, roomID)
			}
		}
	}
	return ch, cancel, nil
}

func (b *MemoryBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for roomID, subs := range b.rooms {
		for id, ch := range subs {
			delete(subs, id)
			close(ch)
		}
		delete(b.rooms, roomID)
	}
	return nil
}
