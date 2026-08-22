// Package discussiontest provides an in-memory discussion.Repository so
// threading and voting rules can be tested without a database.
package discussiontest

import (
	"context"
	"sort"
	"strconv"
	"sync"

	"github.com/toji339/online-judge/internal/discussion"
)

// FakeRepository stores comments in a map keyed by generated ID.
type FakeRepository struct {
	mu       sync.Mutex
	nextID   int
	comments map[string]*discussion.Comment

	// What the last ListReplies call asked for and handed back. The
	// service is supposed to bound that query, and a test can only assert
	// it by recording it here.
	lastReplyLimit int
	lastReplyRows  int
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

// ListRoots mirrors the Mongo query: top-level comments only, newest
// first by (createdAt, id), bounded by limit, starting past the cursor.
func (r *FakeRepository) ListRoots(_ context.Context, problemID string, after *discussion.Cursor, limit int) ([]discussion.Comment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	roots := []discussion.Comment{}
	for _, c := range r.comments {
		if c.ProblemID == problemID && !c.IsReply() {
			roots = append(roots, *c)
		}
	}

	sort.Slice(roots, func(i, j int) bool {
		if roots[i].CreatedAt.Equal(roots[j].CreatedAt) {
			return roots[i].ID > roots[j].ID
		}
		return roots[i].CreatedAt.After(roots[j].CreatedAt)
	})

	if after != nil {
		filtered := roots[:0:0]
		for _, c := range roots {
			past := c.CreatedAt.Before(after.CreatedAt) ||
				(c.CreatedAt.Equal(after.CreatedAt) && c.ID < after.ID)
			if past {
				filtered = append(filtered, c)
			}
		}
		roots = filtered
	}

	if limit > 0 && len(roots) > limit {
		roots = roots[:limit]
	}
	return roots, nil
}

// ListReplies returns the oldest replies of each parent, oldest first
// and at most limitPerParent per parent — the same bound the MongoDB
// implementation applies, so a test cannot pass here and overrun there.
func (r *FakeRepository) ListReplies(_ context.Context, parentIDs []string, limitPerParent int) ([]discussion.Comment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.lastReplyLimit = limitPerParent
	r.lastReplyRows = 0

	if limitPerParent <= 0 {
		return []discussion.Comment{}, nil
	}

	byParent := make(map[string][]discussion.Comment, len(parentIDs))
	for _, c := range r.comments {
		if c.IsReply() {
			byParent[c.ParentID] = append(byParent[c.ParentID], *c)
		}
	}

	out := []discussion.Comment{}
	for _, parentID := range parentIDs {
		replies := byParent[parentID]
		sortByAge(replies)
		if len(replies) > limitPerParent {
			replies = replies[:limitPerParent]
		}
		out = append(out, replies...)
	}
	r.lastReplyRows = len(out)

	sortByAge(out)
	return out, nil
}

// sortByAge orders comments oldest first, with the id as a stable
// tie-break because timestamps collide.
func sortByAge(comments []discussion.Comment) {
	sort.Slice(comments, func(i, j int) bool {
		if comments[i].CreatedAt.Equal(comments[j].CreatedAt) {
			return comments[i].ID < comments[j].ID
		}
		return comments[i].CreatedAt.Before(comments[j].CreatedAt)
	})
}

// LastReplyLimit returns the per-parent limit the last ListReplies call
// arrived with, so a test can assert the query was actually bounded.
func (r *FakeRepository) LastReplyLimit() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastReplyLimit
}

// LastReplyRowCount returns how many replies the last ListReplies call
// handed back — the rows the process would have held in memory.
func (r *FakeRepository) LastReplyRowCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastReplyRows
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
