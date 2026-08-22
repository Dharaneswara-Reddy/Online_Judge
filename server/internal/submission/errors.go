package submission

import (
	"errors"
	"fmt"
)

// ErrNotFound is returned when a submission does not exist.
var ErrNotFound = errors.New("submission not found")

// ErrTooManyPending is returned by admission control when a user already
// has a submission waiting in the queue. Only one pending submission per
// user is allowed, which stops accidental spam from filling the queue.
var ErrTooManyPending = errors.New("you already have a submission being judged")

// ErrAlreadyClaimed is returned when a judge worker tries to start
// judging a submission another worker already holds, or one that already
// carries a verdict. The caller must abandon the job rather than judge it
// a second time.
var ErrAlreadyClaimed = errors.New("submission is already being judged")

// ErrAlreadyJudged is returned when a verdict is written for a submission
// that is no longer running — because another worker got there first, or
// because the reaper reclaimed it. The verdict in hand is discarded: the
// stored one is authoritative, and rewriting judged_at would corrupt War
// Room winner history.
var ErrAlreadyJudged = errors.New("submission already has a verdict")

// ValidationError describes a rejected input field. Controllers map it to
// a 400 response, so the field name is safe to show the user.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}
