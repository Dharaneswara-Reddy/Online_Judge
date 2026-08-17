package discussion

import "context"

// Repository is the storage boundary for discussion comments.
type Repository interface {
	Create(ctx context.Context, c *Comment) error
	GetByID(ctx context.Context, id string) (*Comment, error)

	// ListRoots returns one bounded page of top-level comments, newest
	// first, starting after the cursor when one is given. It fetches at
	// most limit rows from the database — the thread is never loaded
	// whole and sliced in Go.
	ListRoots(ctx context.Context, problemID string, after *Cursor, limit int) ([]Comment, error)

	// ListReplies returns the replies belonging to the given parents.
	// Only the parents on the current page are ever asked for, so this
	// stays bounded by the page size too.
	ListReplies(ctx context.Context, parentIDs []string) ([]Comment, error)

	// SetUpvote adds or removes one user's vote and returns the resulting
	// count. It is idempotent: voting twice the same way changes nothing.
	SetUpvote(ctx context.Context, commentID, userID string, up bool) (int, error)

	// SoftDelete blanks a comment's content but keeps the row, so replies
	// hanging off it do not vanish from the thread.
	SoftDelete(ctx context.Context, commentID string) error
}
