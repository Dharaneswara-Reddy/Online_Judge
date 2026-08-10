package warroom

import (
	"errors"
	"fmt"
)

var (
	// ErrNotFound is returned when no room matches the given code or ID.
	ErrNotFound = errors.New("war room not found")
	// ErrRoomFull is returned when a room has reached its participant cap.
	ErrRoomFull = errors.New("this war room is already full")
	// ErrRoomClosed is returned when a room is no longer accepting players
	// because the race has started, finished, or expired.
	ErrRoomClosed = errors.New("this war room is no longer open to join")
	// ErrNotParticipant is returned when a user acts on a room they never
	// joined — submitting into someone else's race, for example.
	ErrNotParticipant = errors.New("you are not a participant in this war room")
)

// ValidationError describes a rejected input field.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}
