package discussion

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Comment length bounds. The lower bound rejects empty noise; the upper
// bound keeps one post from dominating a thread.
const (
	minContentLength = 2
	maxContentLength = 4000
)

// Service holds the discussion business rules.
type Service struct {
	repo Repository
}

// NewService creates a Service backed by the given Repository.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// Create posts a comment or a reply.
//
// Replying to a reply is rejected, which keeps every thread exactly one
// level deep.
func (s *Service) Create(ctx context.Context, input CreateInput) (*Comment, error) {
	// Steps to follow while posting a comment
	// =========================================

	// 1. Validate the input
	content := strings.TrimSpace(input.Content)
	if utf8.RuneCountInString(content) < minContentLength {
		return nil, ValidationError{Field: "content", Message: "is too short"}
	}
	if utf8.RuneCountInString(content) > maxContentLength {
		return nil, ValidationError{Field: "content", Message: "is too long"}
	}
	if input.ProblemID == "" {
		return nil, ValidationError{Field: "problemId", Message: "is required"}
	}
	if input.UserID == "" {
		return nil, ValidationError{Field: "userId", Message: "is required"}
	}

	// 2. If this is a reply, the parent must exist and be top-level
	if input.ParentID != "" {
		parent, err := s.repo.GetByID(ctx, input.ParentID)
		if err != nil {
			return nil, err
		}
		if parent.IsReply() {
			return nil, ErrNestingTooDeep
		}
		// A reply always belongs to its parent's problem, whatever the
		// caller claimed.
		input.ProblemID = parent.ProblemID
	}

	// 3. Store it
	comment := &Comment{
		ProblemID: input.ProblemID,
		UserID:    input.UserID,
		Username:  input.Username,
		ParentID:  input.ParentID,
		Content:   content,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.repo.Create(ctx, comment); err != nil {
		return nil, err
	}
	return comment, nil
}

// ListThreads returns a problem's discussion assembled into threads:
// top-level comments, newest first, each with its replies oldest first.
//
// viewerID marks which comments the caller has already upvoted so the UI
// can render the vote control correctly; pass an empty string for an
// anonymous reader.
func (s *Service) ListThreads(ctx context.Context, problemID, viewerID string) ([]Comment, error) {
	all, err := s.repo.ListForProblem(ctx, problemID)
	if err != nil {
		return nil, err
	}

	// Index replies by their parent in one pass.
	repliesByParent := make(map[string][]Comment)
	var roots []Comment
	for _, c := range all {
		c.UpvotedByMe = viewerID != "" && contains(c.UpvotedBy, viewerID)
		c.UpvotedBy = nil // never leak the voter list to clients
		if c.IsReply() {
			repliesByParent[c.ParentID] = append(repliesByParent[c.ParentID], c)
		} else {
			roots = append(roots, c)
		}
	}

	// Newest discussions first; replies read naturally oldest first.
	sort.SliceStable(roots, func(i, j int) bool {
		return roots[i].CreatedAt.After(roots[j].CreatedAt)
	})

	threads := make([]Comment, 0, len(roots))
	for _, root := range roots {
		replies := repliesByParent[root.ID]
		sort.SliceStable(replies, func(i, j int) bool {
			return replies[i].CreatedAt.Before(replies[j].CreatedAt)
		})
		root.Replies = replies
		threads = append(threads, root)
	}
	return threads, nil
}

// Upvote records a user's vote and returns the new count. Voting is
// idempotent, and a unique voter set is what prevents vote stuffing.
func (s *Service) Upvote(ctx context.Context, commentID, userID string) (int, error) {
	if userID == "" {
		return 0, ValidationError{Field: "userId", Message: "is required"}
	}
	return s.repo.SetUpvote(ctx, commentID, userID, true)
}

// RemoveUpvote withdraws a user's vote and returns the new count.
func (s *Service) RemoveUpvote(ctx context.Context, commentID, userID string) (int, error) {
	if userID == "" {
		return 0, ValidationError{Field: "userId", Message: "is required"}
	}
	return s.repo.SetUpvote(ctx, commentID, userID, false)
}

// Delete removes a user's own comment. Deletion is soft so that replies
// to it keep their place in the thread.
//
// Admins may delete any comment, which is what the moderation view in
// the admin dashboard uses.
func (s *Service) Delete(ctx context.Context, commentID, userID string, isAdmin bool) error {
	comment, err := s.repo.GetByID(ctx, commentID)
	if err != nil {
		return err
	}
	if comment.UserID != userID && !isAdmin {
		return ErrNotAuthor
	}
	return s.repo.SoftDelete(ctx, commentID)
}

func contains(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}
