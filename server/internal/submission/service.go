package submission

import (
	"context"
	"strings"
	"time"

	"github.com/toji339/online-judge/internal/judge"
)

// maxPendingPerUser is the admission-control limit described in the HLD:
// one in-flight submission per user. It is enforced at the service layer
// so it applies no matter which transport (HTTP or War Room) submits.
const maxPendingPerUser = 1

// maxCodeBytes caps the source we accept, keeping oversized payloads out
// of both the queue and the database.
const maxCodeBytes = 64 * 1024

// Service holds the submission business rules and is the only type the
// controllers and the judge worker talk to.
type Service struct {
	repo Repository
}

// NewService creates a Service backed by the given Repository.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// CreateInput is everything needed to record a new submission attempt.
// The problem slug and title are denormalised onto the submission so the
// history page can render without a second lookup per row.
type CreateInput struct {
	UserID       string
	ProblemID    string
	ProblemSlug  string
	ProblemTitle string
	WarRoomID    string
	Language     string
	Code         string
}

// Create validates the input, enforces per-user admission control, and
// stores the submission in the pending state ready for the queue.
//
// It returns ErrTooManyPending when the user already has work in flight.
func (s *Service) Create(ctx context.Context, input CreateInput) (*Submission, error) {
	// Steps to follow while creating a submission
	// =============================================

	// 1. Validate the input before touching the database
	if err := validateCreateInput(input); err != nil {
		return nil, err
	}

	// 2. Admission control — refuse a second in-flight submission
	pending, err := s.repo.CountPending(ctx, input.UserID)
	if err != nil {
		return nil, err
	}
	if pending >= maxPendingPerUser {
		return nil, ErrTooManyPending
	}

	// 3. Build the pending record. Verdict fields stay zeroed until the
	//    worker judges it; FailedCase of -1 means "no case failed yet".
	sub := &Submission{
		UserID:       input.UserID,
		ProblemID:    input.ProblemID,
		ProblemSlug:  input.ProblemSlug,
		ProblemTitle: input.ProblemTitle,
		WarRoomID:    input.WarRoomID,
		Language:     input.Language,
		Code:         input.Code,
		Status:       StatusPending,
		FailedCase:   -1,
		SubmittedAt:  time.Now().UTC(),
	}

	// 4. Persist it
	if err := s.repo.Create(ctx, sub); err != nil {
		return nil, err
	}
	return sub, nil
}

// MarkRunning flags a submission as picked up by a judge worker.
func (s *Service) MarkRunning(ctx context.Context, id string) error {
	return s.repo.UpdateStatus(ctx, id, StatusRunning, nil)
}

// MarkJudged writes the final verdict. It is called only by the judge
// worker — the user-facing API never sets a verdict, which keeps the
// outcome server-authoritative.
func (s *Service) MarkJudged(ctx context.Context, id string, result Result) error {
	if !result.Status.IsTerminal() {
		return ValidationError{Field: "status", Message: "must be a terminal verdict"}
	}
	return s.repo.UpdateStatus(ctx, id, result.Status, &result)
}

// MarkFailed records an infrastructure failure (sandbox crash, worker
// giving up) as a runtime error so the submission never sits pending
// forever.
func (s *Service) MarkFailed(ctx context.Context, id, reason string) error {
	return s.repo.UpdateStatus(ctx, id, StatusRuntimeError, &Result{
		Status:       StatusRuntimeError,
		FailedCase:   -1,
		CompileError: reason,
	})
}

// GetByID returns one submission, or ErrNotFound.
func (s *Service) GetByID(ctx context.Context, id string) (*Submission, error) {
	return s.repo.GetByID(ctx, id)
}

// List returns a page of submissions matching the filter, newest first.
func (s *Service) List(ctx context.Context, filter ListFilter) ([]Submission, error) {
	return s.repo.List(ctx, filter)
}

// Count returns the total number of submissions matching the filter,
// ignoring pagination, so the client can render page controls.
func (s *Service) Count(ctx context.Context, filter ListFilter) (int, error) {
	return s.repo.Count(ctx, filter)
}

// Stats summarises one user's judging record for the profile page.
func (s *Service) Stats(ctx context.Context, userID string) (Stats, error) {
	total, err := s.repo.Count(ctx, ListFilter{UserID: userID})
	if err != nil {
		return Stats{}, err
	}
	accepted, err := s.repo.CountAccepted(ctx, userID)
	if err != nil {
		return Stats{}, err
	}
	solved, err := s.repo.SolvedProblemIDs(ctx, userID)
	if err != nil {
		return Stats{}, err
	}
	return Stats{TotalSubmissions: total, Accepted: accepted, SolvedProblemIDs: solved}, nil
}

// FirstAcceptedInRoom returns the submission that won a War Room, judged
// by server-stamped JudgedAt rather than any client-reported time.
func (s *Service) FirstAcceptedInRoom(ctx context.Context, warRoomID string) (*Submission, error) {
	return s.repo.FirstAcceptedInRoom(ctx, warRoomID)
}

// StatusFromVerdict maps a judge verdict onto a submission status. The
// two vocabularies are deliberately identical, so this is a total
// mapping with a safe fallback.
func StatusFromVerdict(v judge.Verdict) Status {
	switch v {
	case judge.VerdictAccepted:
		return StatusAccepted
	case judge.VerdictWrongAnswer:
		return StatusWrongAnswer
	case judge.VerdictTimeLimitExceeded:
		return StatusTLE
	case judge.VerdictMemoryLimitExceeded:
		return StatusMLE
	case judge.VerdictCompileError:
		return StatusCompileError
	default:
		return StatusRuntimeError
	}
}

func validateCreateInput(input CreateInput) error {
	if strings.TrimSpace(input.UserID) == "" {
		return ValidationError{Field: "userId", Message: "is required"}
	}
	if strings.TrimSpace(input.ProblemID) == "" {
		return ValidationError{Field: "problemId", Message: "is required"}
	}
	if _, err := judge.GetLanguage(input.Language); err != nil {
		return ValidationError{Field: "language", Message: "is not supported"}
	}
	if strings.TrimSpace(input.Code) == "" {
		return ValidationError{Field: "code", Message: "must not be empty"}
	}
	if len(input.Code) > maxCodeBytes {
		return ValidationError{Field: "code", Message: "exceeds the 64KB size limit"}
	}
	return nil
}
