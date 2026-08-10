package discussion

import (
	"errors"
	"fmt"
)

var (
	// ErrNotFound is returned when a comment does not exist.
	ErrNotFound = errors.New("comment not found")
	// ErrNestingTooDeep is returned when replying to a reply. Threads are
	// kept one level deep so they stay readable.
	ErrNestingTooDeep = errors.New("you can only reply to a top-level comment")
	// ErrNotAuthor is returned when a user tries to delete someone else's
	// comment.
	ErrNotAuthor = errors.New("you can only delete your own comments")
)

// ValidationError describes a rejected input field.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}
