package submission

import "context"

// Repository is the storage boundary for submissions. The production
// implementation is MongoDB-backed (see the mongorepo subpackage); unit
// tests use the in-memory fake in submissiontest.
type Repository interface {
	Create(ctx context.Context, s *Submission) error
	GetByID(ctx context.Context, id string) (*Submission, error)
	List(ctx context.Context, filter ListFilter) ([]Submission, error)
	Count(ctx context.Context, filter ListFilter) (int, error)

	// UpdateStatus moves a submission to a new lifecycle state. Result is
	// nil for transient states (running) and populated for verdicts.
	UpdateStatus(ctx context.Context, id string, status Status, result *Result) error

	// CountPending returns how many of a user's submissions are still
	// waiting or running. Used for admission control.
	CountPending(ctx context.Context, userID string) (int, error)

	// SolvedProblemIDs returns the distinct problems a user has solved.
	SolvedProblemIDs(ctx context.Context, userID string) ([]string, error)

	// CountAccepted returns how many of a user's submissions were accepted.
	CountAccepted(ctx context.Context, userID string) (int, error)

	// FirstAcceptedInRoom returns the earliest accepted submission for a
	// War Room, which decides the winner. It returns ErrNotFound when no
	// participant has been accepted yet.
	FirstAcceptedInRoom(ctx context.Context, warRoomID string) (*Submission, error)
}
