package problem

import (
	"errors"
	"fmt"
)

var ErrNotFound = errors.New("problem not found")

// ErrSlugConflict is returned when a problem could not be stored because
// another problem already holds its slug.
//
// Slug allocation reads the collection, picks a free slug, and inserts.
// Those three steps are not one operation, so two concurrent creates of
// the same title both see the slug free and both attempt the insert. The
// unique index on "slug" is the authority that settles it: one insert is
// admitted and the rest are rejected with a duplicate-key error, which
// the repository translates into this.
//
// The loser is a conflict, not a failure — the caller's request was
// valid and simply arrived second. Controllers map it to 409 so the
// admin sees "that title was just taken, try again" instead of an opaque
// 500, and nothing is stored for a request told it failed.
var ErrSlugConflict = errors.New("a problem with that slug already exists")

type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}
