// Package warroomtest provides an in-memory warroom.Repository so the
// service's lifecycle and winner rules can be tested without a database.
package warroomtest

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/toji339/online-judge/internal/warroom"
)

// FakeRepository stores rooms in a map. Its conditional writes hold a
// single mutex for the whole read-and-write, which gives them the same
// all-or-nothing behaviour the MongoDB implementation gets from an
// atomic update.
type FakeRepository struct {
	mu     sync.Mutex
	nextID int
	rooms  map[string]*warroom.Room
}

// New creates an empty FakeRepository.
func New() *FakeRepository {
	return &FakeRepository{rooms: make(map[string]*warroom.Room)}
}

func (r *FakeRepository) Create(_ context.Context, room *warroom.Room) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	room.ID = strconv.Itoa(r.nextID)
	clone := *room
	r.rooms[room.ID] = &clone
	return nil
}

func (r *FakeRepository) GetByID(_ context.Context, id string) (*warroom.Room, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	room, ok := r.rooms[id]
	if !ok {
		return nil, warroom.ErrNotFound
	}
	return copyRoom(room), nil
}

func (r *FakeRepository) GetByCode(_ context.Context, code string) (*warroom.Room, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, room := range r.rooms {
		if room.RoomCode == code {
			return copyRoom(room), nil
		}
	}
	return nil, warroom.ErrNotFound
}

func (r *FakeRepository) ListByStatus(_ context.Context, status warroom.Status, limit int) ([]warroom.Room, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := []warroom.Room{}
	for _, room := range r.rooms {
		if room.Status == status {
			out = append(out, *copyRoom(room))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *FakeRepository) ListForUser(_ context.Context, userID string, limit int) ([]warroom.Room, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := []warroom.Room{}
	for _, room := range r.rooms {
		if room.HasParticipant(userID) {
			out = append(out, *copyRoom(room))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *FakeRepository) AddParticipant(_ context.Context, roomID string, p warroom.Participant) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	room, ok := r.rooms[roomID]
	if !ok {
		return false, warroom.ErrNotFound
	}
	if room.Status != warroom.StatusWaiting {
		return false, warroom.ErrRoomClosed
	}
	if room.IsFull() {
		return false, warroom.ErrRoomFull
	}
	room.Participants = append(room.Participants, p)
	return room.IsFull(), nil
}

func (r *FakeRepository) Start(_ context.Context, roomID string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	room, ok := r.rooms[roomID]
	if !ok {
		return warroom.ErrNotFound
	}
	room.Status = warroom.StatusInProgress
	room.StartedAt = &at
	return nil
}

func (r *FakeRepository) DeclareWinner(_ context.Context, roomID, winnerID, winnerUsername string, at time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	room, ok := r.rooms[roomID]
	if !ok {
		return false, warroom.ErrNotFound
	}
	if room.WinnerID != "" {
		return false, nil
	}
	room.WinnerID = winnerID
	room.WinnerUsername = winnerUsername
	room.Status = warroom.StatusFinished
	room.EndedAt = &at
	return true, nil
}

func (r *FakeRepository) ExpireStale(_ context.Context, waitingBefore, startedBefore time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	expired := 0
	for _, room := range r.rooms {
		switch room.Status {
		case warroom.StatusWaiting:
			if room.CreatedAt.Before(waitingBefore) {
				room.Status = warroom.StatusExpired
				expired++
			}
		case warroom.StatusInProgress:
			if room.StartedAt != nil && room.StartedAt.Before(startedBefore) {
				room.Status = warroom.StatusExpired
				expired++
			}
		}
	}
	return expired, nil
}

// Backdate rewrites a room's timestamps so expiry rules can be tested
// without waiting out a real TTL. Test-only helper.
func (r *FakeRepository) Backdate(roomID string, createdAt time.Time, startedAt *time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if room, ok := r.rooms[roomID]; ok {
		room.CreatedAt = createdAt
		room.StartedAt = startedAt
	}
}

// copyRoom deep-copies the participant slice so callers cannot mutate
// stored state through the returned value.
func copyRoom(room *warroom.Room) *warroom.Room {
	clone := *room
	clone.Participants = append([]warroom.Participant(nil), room.Participants...)
	return &clone
}
