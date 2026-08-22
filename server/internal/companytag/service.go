package companytag

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Repository is the storage boundary for company tags.
type Repository interface {
	// Add stores one user's report. It returns ErrAlreadyTagged when the
	// unique (problem, user, company) constraint rejects a duplicate.
	Add(ctx context.Context, tag *Tag) error

	// IncrementSummary bumps the denormalised per-problem count that the
	// problem list filter reads.
	IncrementSummary(ctx context.Context, problemID, company string) error

	// Remove deletes one report by id. It exists so a tag whose summary
	// write failed can be undone, leaving no half-applied state for the
	// unique index to reject on retry.
	Remove(ctx context.Context, tagID string) error

	// RecountSummary sets the denormalised count for one company to the
	// number of reports actually stored for it. The reports are the
	// authority; the summary is a cache of them, and this is how a cache
	// that drifted is put back.
	RecountSummary(ctx context.Context, problemID, company string) error

	// ListForProblem returns the aggregated companies for one problem.
	ListForProblem(ctx context.Context, problemID string) ([]CompanyCount, error)

	// ListCompanies returns every company that has at least one tag,
	// most-tagged first.
	ListCompanies(ctx context.Context, limit int) ([]CompanyCount, error)

	// ListUserTags returns the companies one user reported for a problem,
	// so the UI can show what they already answered.
	ListUserTags(ctx context.Context, problemID, userID string) ([]Tag, error)
}

// Service holds the company tag business rules.
type Service struct {
	repo Repository
}

// NewService creates a Service backed by the given Repository.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// TagInput is one user's answer to "have you seen this in an interview?".
type TagInput struct {
	ProblemID string
	UserID    string
	Company   string
	Round     string
}

// Tag records a report and keeps the per-problem summary in step.
func (s *Service) Tag(ctx context.Context, input TagInput) (*Tag, error) {
	// Steps to follow while tagging a problem
	// =========================================

	// 1. Normalise and validate, so "google " and "Google" agree
	company, round, err := Validate(input.Company, input.Round)
	if err != nil {
		return nil, err
	}
	if input.ProblemID == "" {
		return nil, ValidationError{Field: "problemId", Message: "is required"}
	}
	if input.UserID == "" {
		return nil, ValidationError{Field: "userId", Message: "is required"}
	}

	// 2. Store the report. The unique index rejects a repeat from the
	//    same user, which is what stops one person inflating a count.
	tag := &Tag{
		ProblemID: input.ProblemID,
		UserID:    input.UserID,
		Company:   company,
		Round:     round,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.repo.Add(ctx, tag); err != nil {
		if errors.Is(err, ErrAlreadyTagged) {
			// A duplicate is the one moment we know a report exists and can
			// cheaply check the cache built from it. If an earlier request
			// died between the two writes, this is what puts the count back
			// — the repair is best effort, and its failure must not replace
			// the answer the caller actually asked about.
			_ = s.repo.RecountSummary(ctx, input.ProblemID, company)
		}
		return nil, err
	}

	// 3. Only a genuinely new report moves the aggregate count.
	//
	//    The two writes cannot be one, so the pair is made recoverable
	//    instead: a failed summary write undoes the report, which leaves
	//    the two in step and lets the caller simply try again. Without
	//    that, the report stayed and the unique index rejected the very
	//    retry that would have fixed the count.
	if err := s.repo.IncrementSummary(ctx, input.ProblemID, company); err != nil {
		if removeErr := s.repo.Remove(ctx, tag.ID); removeErr != nil {
			// Both writes failed to settle. Say so plainly: the count is
			// short by one until someone tags this company again, and a
			// caller told only "try again" would be misled.
			return nil, fmt.Errorf("%w: tagged %s but the count was not updated (%v), and the report could not be undone (%v)",
				ErrSummaryOutOfStep, company, err, removeErr)
		}
		return nil, err
	}
	return tag, nil
}

// ListForProblem returns the aggregated company tags for one problem.
func (s *Service) ListForProblem(ctx context.Context, problemID string) ([]CompanyCount, error) {
	return s.repo.ListForProblem(ctx, problemID)
}

// ListCompanies powers the company explorer.
func (s *Service) ListCompanies(ctx context.Context, limit int) ([]CompanyCount, error) {
	if limit <= 0 {
		limit = 100
	}
	return s.repo.ListCompanies(ctx, limit)
}

// ListUserTags returns what one user already reported for a problem.
func (s *Service) ListUserTags(ctx context.Context, problemID, userID string) ([]Tag, error) {
	if userID == "" {
		return []Tag{}, nil
	}
	return s.repo.ListUserTags(ctx, problemID, userID)
}
