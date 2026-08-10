package discussion

import "context"

// Repository is the storage boundary for discussion comments.
type Repository interface {
	Create(ctx context.Context, c *Comment) error
	GetByID(ctx context.Context, id string) (*Comment, error)

	// ListForProblem returns every comment on a problem, both top-level
	// posts and replies, oldest first. The service assembles them into
	// threads.
	ListForProblem(ctx context.Context, problemID string) ([]Comment, error)

	// SetUpvote adds or removes one user's vote and returns the resulting
	// count. It is idempotent: voting twice the same way changes nothing.
	SetUpvote(ctx context.Context, commentID, userID string, up bool) (int, error)

	// SoftDelete blanks a comment's content but keeps the row, so replies
	// hanging off it do not vanish from the thread.
	SoftDelete(ctx context.Context, commentID string) error
}
