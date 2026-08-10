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

// ValidationError describes a rejected input field. Controllers map it to
// a 400 response, so the field name is safe to show the user.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}
