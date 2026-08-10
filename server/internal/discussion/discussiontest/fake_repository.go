// Package discussiontest provides an in-memory discussion.Repository so
// threading and voting rules can be tested without a database.
package discussiontest

import (
	"context"
	"strconv"
	"sync"

	"github.com/toji339/online-judge/internal/discussion"
)

// FakeRepository stores comments in a map keyed by generated ID.
type FakeRepository struct {
	mu       sync.Mutex
	nextID   int
	comments map[string]*discussion.Comment
}

// New creates an empty FakeRepository.
func New() *FakeRepository {
	return &FakeRepository{comments: make(map[string]*discussion.Comment)}
}

func (r *FakeRepository) Create(_ context.Context, c *discussion.Comment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	c.ID = strconv.Itoa(r.nextID)
	clone := *c
	r.comments[c.ID] = &clone
	return nil
}

func (r *FakeRepository) GetByID(_ context.Context, id string) (*discussion.Comment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.comments[id]
	if !ok {
		return nil, discussion.ErrNotFound
	}
	clone := *c
	return &clone, nil
}

func (r *FakeRepository) ListForProblem(_ context.Context, problemID string) ([]discussion.Comment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := []discussion.Comment{}
	for _, c := range r.comments {
		if c.ProblemID == problemID {
			out = append(out, *c)
		}
	}
	return out, nil
}

func (r *FakeRepository) SetUpvote(_ context.Context, commentID, userID string, up bool) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	c, ok := r.comments[commentID]
	if !ok {
		return 0, discussion.ErrNotFound
	}

	// Rebuild the voter set without this user, then re-add them only for
	// an upvote. That makes repeat calls idempotent in either direction.
	voters := make([]string, 0, len(c.UpvotedBy))
	for _, v := range c.UpvotedBy {
		if v != userID {
			voters = append(voters, v)
		}
	}
	if up {
		voters = append(voters, userID)
	}

	c.UpvotedBy = voters
	c.Upvotes = len(voters)
	return c.Upvotes, nil
}

func (r *FakeRepository) SoftDelete(_ context.Context, commentID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	c, ok := r.comments[commentID]
	if !ok {
		return discussion.ErrNotFound
	}
	c.Deleted = true
	c.Content = ""
	return nil
}
