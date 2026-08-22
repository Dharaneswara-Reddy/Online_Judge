package submission

import (
	"context"
	"time"
)

// Repository is the storage boundary for submissions. The production
// implementation is MongoDB-backed (see the mongorepo subpackage); unit
// tests use the in-memory fake in submissiontest.
//
// Every lifecycle transition below is a *conditional* write: the state
// the submission must currently be in is part of the query predicate, so
// the database — not application code — decides which of two racing
// workers wins. This is the same pattern warroom's AddParticipant and
// DeclareWinner use, and for the same reason: a read-then-write here lets
// a redelivered message overwrite a verdict that has already been
// broadcast.
type Repository interface {
	Create(ctx context.Context, s *Submission) error
	GetByID(ctx context.Context, id string) (*Submission, error)
	List(ctx context.Context, filter ListFilter) ([]Submission, error)
	Count(ctx context.Context, filter ListFilter) (int, error)

	// ClaimForJudging moves a submission into the running state and
	// stamps StartedAt.
	//
	// It succeeds only while the submission is still pending, or while it
	// is running but was claimed before staleBefore — which means the
	// worker holding it is gone and the claim may be taken over. It
	// returns ErrAlreadyClaimed in every other case, including when a
	// verdict has already been recorded, and ErrNotFound when there is no
	// such submission.
	ClaimForJudging(ctx context.Context, id string, staleBefore time.Time) error

	// CompleteJudging writes the final verdict, but only while the
	// submission is still running. It returns ErrAlreadyJudged when the
	// submission is in any other state, so a second worker's verdict is
	// discarded instead of overwriting the first.
	CompleteJudging(ctx context.Context, id string, result Result) error

	// FailNonTerminal records a terminal failure status, but only while
	// the submission has no verdict yet. It returns ErrAlreadyJudged when
	// one has already been recorded — an infrastructure failure must
	// never overwrite a real result.
	FailNonTerminal(ctx context.Context, id string, status Status, reason string) error

	// ExpireStale reclaims submissions left non-terminal past the given
	// cutoffs and reports how many were reclaimed. Pending rows are aged
	// by SubmittedAt and running rows by StartedAt. Without this, a
	// submission nothing will ever finish holds its owner's admission
	// slot forever, locking them out of the judge.
	ExpireStale(ctx context.Context, pendingBefore, runningBefore time.Time) (int, error)

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
