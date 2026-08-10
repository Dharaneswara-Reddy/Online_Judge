// Package discussion implements the per-problem comment thread where
// users ask questions and share approaches.
//
// Threading is deliberately one level deep: a comment may have replies,
// but a reply may not. Unbounded nesting makes a thread hard to render
// and harder to follow, and the design document explicitly allows the
// simpler shape.
package discussion

import "time"

// Comment is one post in a problem's discussion.
//
// The author's username is denormalised so rendering a thread needs no
// per-row user lookup. UpvotedBy stores who voted rather than a bare
// counter, which is what makes voting idempotent.
type Comment struct {
	ID        string    `bson:"_id,omitempty" json:"id"`
	ProblemID string    `bson:"problem_id" json:"problemId"`
	UserID    string    `bson:"user_id" json:"userId"`
	Username  string    `bson:"username" json:"username"`
	ParentID  string    `bson:"parent_id,omitempty" json:"parentId,omitempty"`
	Content   string    `bson:"content" json:"content"`
	Upvotes   int       `bson:"upvotes" json:"upvotes"`
	UpvotedBy []string  `bson:"upvoted_by,omitempty" json:"-"`
	Deleted   bool      `bson:"deleted,omitempty" json:"deleted,omitempty"`
	CreatedAt time.Time `bson:"created_at" json:"createdAt"`

	// Replies is assembled when a thread is read; it is never stored.
	Replies []Comment `bson:"-" json:"replies,omitempty"`
	// UpvotedByMe is filled in per request for the calling user.
	UpvotedByMe bool `bson:"-" json:"upvotedByMe"`
}

// IsReply reports whether this comment is a reply to another.
func (c *Comment) IsReply() bool { return c.ParentID != "" }

// CreateInput is a new post. ParentID is empty for a top-level comment.
type CreateInput struct {
	ProblemID string
	UserID    string
	Username  string
	ParentID  string
	Content   string
}
