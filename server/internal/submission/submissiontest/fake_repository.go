// Package submissiontest provides an in-memory submission.Repository for
// unit tests, so service rules can be verified without a live database.
package submissiontest

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/toji339/online-judge/internal/submission"
)

// FakeRepository stores submissions in a map keyed by a generated ID.
// It is safe for concurrent use so worker-pool tests can share one.
type FakeRepository struct {
	mu     sync.Mutex
	nextID int
	items  map[string]*submission.Submission
}

// New creates an empty FakeRepository.
func New() *FakeRepository {
	return &FakeRepository{items: make(map[string]*submission.Submission)}
}

func (r *FakeRepository) Create(_ context.Context, s *submission.Submission) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	s.ID = strconv.Itoa(r.nextID)
	clone := *s
	r.items[s.ID] = &clone
	return nil
}

func (r *FakeRepository) GetByID(_ context.Context, id string) (*submission.Submission, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.items[id]
	if !ok {
		return nil, submission.ErrNotFound
	}
	clone := *s
	return &clone, nil
}

func (r *FakeRepository) matches(s *submission.Submission, f submission.ListFilter) bool {
	if f.UserID != "" && s.UserID != f.UserID {
		return false
	}
	if f.ProblemID != "" && s.ProblemID != f.ProblemID {
		return false
	}
	if f.Status != "" && s.Status != f.Status {
		return false
	}
	return true
}

func (r *FakeRepository) List(_ context.Context, f submission.ListFilter) ([]submission.Submission, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []submission.Submission
	for _, s := range r.items {
		if r.matches(s, f) {
			out = append(out, *s)
		}
	}
	// Newest first, matching the Mongo repository's sort order.
	sort.Slice(out, func(i, j int) bool {
		if out[i].SubmittedAt.Equal(out[j].SubmittedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].SubmittedAt.After(out[j].SubmittedAt)
	})

	pageSize := f.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	page := f.Page
	if page <= 0 {
		page = 1
	}
	start := (page - 1) * pageSize
	if start >= len(out) {
		return []submission.Submission{}, nil
	}
	end := min(start+pageSize, len(out))
	return out[start:end], nil
}

func (r *FakeRepository) Count(_ context.Context, f submission.ListFilter) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.items {
		if r.matches(s, f) {
			n++
		}
	}
	return n, nil
}

func (r *FakeRepository) UpdateStatus(_ context.Context, id string, status submission.Status, result *submission.Result) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.items[id]
	if !ok {
		return submission.ErrNotFound
	}
	s.Status = status
	if result != nil {
		s.RuntimeMS = result.RuntimeMS
		s.MemoryKB = result.MemoryKB
		s.FailedCase = result.FailedCase
		s.TotalCases = result.TotalCases
		s.CompileError = result.CompileError
	}
	if status.IsTerminal() {
		now := time.Now().UTC()
		s.JudgedAt = &now
	}
	return nil
}

func (r *FakeRepository) CountPending(_ context.Context, userID string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.items {
		if s.UserID == userID && !s.Status.IsTerminal() {
			n++
		}
	}
	return n, nil
}

func (r *FakeRepository) SolvedProblemIDs(_ context.Context, userID string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := make(map[string]bool)
	var out []string
	for _, s := range r.items {
		if s.UserID == userID && s.Status == submission.StatusAccepted && !seen[s.ProblemID] {
			seen[s.ProblemID] = true
			out = append(out, s.ProblemID)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (r *FakeRepository) CountAccepted(_ context.Context, userID string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.items {
		if s.UserID == userID && s.Status == submission.StatusAccepted {
			n++
		}
	}
	return n, nil
}

func (r *FakeRepository) FirstAcceptedInRoom(_ context.Context, warRoomID string) (*submission.Submission, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var best *submission.Submission
	for _, s := range r.items {
		if s.WarRoomID != warRoomID || s.Status != submission.StatusAccepted || s.JudgedAt == nil {
			continue
		}
		if best == nil || s.JudgedAt.Before(*best.JudgedAt) {
			best = s
		}
	}
	if best == nil {
		return nil, submission.ErrNotFound
	}
	clone := *best
	return &clone, nil
}
