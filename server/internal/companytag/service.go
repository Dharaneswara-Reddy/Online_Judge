package companytag

import (
	"context"
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
		return nil, err
	}

	// 3. Only a genuinely new report moves the aggregate count
	if err := s.repo.IncrementSummary(ctx, input.ProblemID, company); err != nil {
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
