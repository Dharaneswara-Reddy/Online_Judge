package warroom

import (
	"context"
	"time"
)

// Repository is the storage boundary for war rooms.
//
// Two of its methods are deliberately conditional writes rather than
// read-modify-write pairs, because both decide a race between concurrent
// requests and must be settled by the database:
//
//   - AddParticipant refuses to overfill a room
//   - DeclareWinner only succeeds for the first caller
type Repository interface {
	Create(ctx context.Context, room *Room) error
	GetByID(ctx context.Context, id string) (*Room, error)
	GetByCode(ctx context.Context, code string) (*Room, error)
	ListByStatus(ctx context.Context, status Status, limit int) ([]Room, error)
	ListForUser(ctx context.Context, userID string, limit int) ([]Room, error)

	// AddParticipant appends a player only if the room is still waiting
	// and below its cap. It reports whether the room became full, which
	// is what starts the race.
	AddParticipant(ctx context.Context, roomID string, p Participant) (becameFull bool, err error)

	// Start moves a full room into the in_progress state.
	Start(ctx context.Context, roomID string, at time.Time) error

	// DeclareWinner records the winner only if the room does not have one
	// yet. It returns false when another submission got there first, so
	// exactly one caller ever announces a result.
	DeclareWinner(ctx context.Context, roomID, winnerID, winnerUsername string, at time.Time) (bool, error)

	// ExpireStale marks abandoned rooms expired: rooms still waiting that
	// were created before waitingBefore, and rooms still in progress that
	// started before startedBefore.
	ExpireStale(ctx context.Context, waitingBefore, startedBefore time.Time) (int, error)
}
