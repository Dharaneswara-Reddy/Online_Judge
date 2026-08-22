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
	// failNextGet, when set, is returned by the next GetByID and then
	// cleared. Tests use it to simulate a transient database failure.
	failNextGet error
}

// New creates an empty FakeRepository.
func New() *FakeRepository {
	return &FakeRepository{items: make(map[string]*submission.Submission)}
}

func (r *FakeRepository) Create(_ context.Context, s *submission.Submission) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Mirrors the unique partial index in MongoDB: at most one
	// non-terminal submission per user. Holding the lock across the check
	// and the insert gives the same all-or-nothing behaviour the database
	// constraint provides, so a concurrency test against the fake is
	// meaningful rather than vacuous.
	for _, existing := range r.items {
		if existing.UserID == s.UserID && !existing.Status.IsTerminal() {
			return submission.ErrTooManyPending
		}
	}

	r.nextID++
	s.ID = strconv.Itoa(r.nextID)
	clone := *s
	r.items[s.ID] = &clone
	return nil
}

func (r *FakeRepository) GetByID(_ context.Context, id string) (*submission.Submission, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// A one-shot injected failure, so a test can reproduce the read error
	// that used to leave a submission pending forever.
	if r.failNextGet != nil {
		err := r.failNextGet
		r.failNextGet = nil
		return nil, err
	}

	s, ok := r.items[id]
	if !ok {
		return nil, submission.ErrNotFound
	}
	clone := *s
	return &clone, nil
}

// FailNextGet makes the next GetByID return err, standing in for a
// database that is briefly unreachable. Test-only.
func (r *FakeRepository) FailNextGet(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failNextGet = err
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

// ClaimForJudging mirrors the Mongo conditional update: the whole
// check-and-transition happens under the lock, so a concurrency test
// against the fake means the same thing a race against the database
// would.
func (r *FakeRepository) ClaimForJudging(_ context.Context, id string, staleBefore time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, ok := r.items[id]
	if !ok {
		return submission.ErrNotFound
	}
	claimable := s.Status == submission.StatusPending ||
		(s.Status == submission.StatusRunning && s.StartedAt != nil && s.StartedAt.Before(staleBefore))
	if !claimable {
		return submission.ErrAlreadyClaimed
	}

	now := time.Now().UTC()
	s.Status = submission.StatusRunning
	s.StartedAt = &now
	return nil
}

// CompleteJudging records a verdict only while the submission is running.
func (r *FakeRepository) CompleteJudging(_ context.Context, id string, result submission.Result) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, ok := r.items[id]
	if !ok {
		return submission.ErrNotFound
	}
	if s.Status != submission.StatusRunning {
		return submission.ErrAlreadyJudged
	}
	applyVerdict(s, result.Status, result)
	return nil
}

// FailNonTerminal records a failure only while no verdict exists.
func (r *FakeRepository) FailNonTerminal(_ context.Context, id string, status submission.Status, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, ok := r.items[id]
	if !ok {
		return submission.ErrNotFound
	}
	if s.Status.IsTerminal() {
		return submission.ErrAlreadyJudged
	}
	applyVerdict(s, status, submission.Result{
		Status:       status,
		FailedCase:   -1,
		CompileError: reason,
	})
	return nil
}

// ExpireStale reclaims submissions left non-terminal past the cutoffs.
func (r *FakeRepository) ExpireStale(_ context.Context, pendingBefore, runningBefore time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := 0
	for _, s := range r.items {
		stale := false
		switch s.Status {
		case submission.StatusPending:
			stale = s.SubmittedAt.Before(pendingBefore)
		case submission.StatusRunning:
			aged := s.SubmittedAt
			if s.StartedAt != nil {
				aged = *s.StartedAt
			}
			stale = aged.Before(runningBefore)
		}
		if !stale {
			continue
		}
		applyVerdict(s, submission.StatusJudgeError, submission.Result{
			Status:       submission.StatusJudgeError,
			FailedCase:   -1,
			CompileError: submission.StaleReason,
		})
		n++
	}
	return n, nil
}

// applyVerdict writes a terminal state onto a stored submission. The
// caller must hold the lock.
func applyVerdict(s *submission.Submission, status submission.Status, result submission.Result) {
	s.Status = status
	s.RuntimeMS = result.RuntimeMS
	s.MemoryKB = result.MemoryKB
	s.FailedCase = result.FailedCase
	s.TotalCases = result.TotalCases
	s.CompileError = result.CompileError
	if status.IsTerminal() {
		now := time.Now().UTC()
		s.JudgedAt = &now
	}
}

// SetSubmittedAt back-dates a submission so a test can age it past a
// reaper cutoff without sleeping. Test-only.
func (r *FakeRepository) SetSubmittedAt(id string, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.items[id]; ok {
		s.SubmittedAt = at
	}
}

// SetStartedAt back-dates a running claim, standing in for the worker
// that took it having died. Test-only.
func (r *FakeRepository) SetStartedAt(id string, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.items[id]; ok {
		s.StartedAt = &at
	}
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
