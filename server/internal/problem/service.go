package problem

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

type CreateProblemInput struct {
	Title         string
	Statement     string
	Difficulty    Difficulty
	Tags          []string
	TimeLimitMS   int
	MemoryLimitMB int
	StarterCode   map[string]string
}

func (s *Service) Create(ctx context.Context, input CreateProblemInput) (*Problem, error) {
	if err := validateProblemInput(input); err != nil {
		return nil, err
	}

	slug, err := s.uniqueSlug(ctx, Slugify(input.Title))
	if err != nil {
		return nil, err
	}

	p := &Problem{
		Title: input.Title, Slug: slug, Statement: input.Statement,
		Difficulty: input.Difficulty, Tags: input.Tags,
		TimeLimitMS: input.TimeLimitMS, MemoryLimitMB: input.MemoryLimitMB,
		StarterCode: input.StarterCode, CreatedAt: time.Now().UTC(),
	}
	if err := s.repo.Create(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Service) uniqueSlug(ctx context.Context, base string) (string, error) {
	slug := base
	for i := 2; ; i++ {
		_, err := s.repo.GetBySlug(ctx, slug)
		if errors.Is(err, ErrNotFound) {
			return slug, nil
		}
		if err != nil {
			return "", err
		}
		slug = fmt.Sprintf("%s-%d", base, i)
	}
}

func (s *Service) GetBySlug(ctx context.Context, slug string) (*Problem, error) {
	return s.repo.GetBySlug(ctx, slug)
}

func (s *Service) GetByID(ctx context.Context, id string) (*Problem, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *Service) List(ctx context.Context, filter ListFilter) ([]Problem, error) {
	return s.repo.List(ctx, filter)
}

func (s *Service) Update(ctx context.Context, id string, input CreateProblemInput) (*Problem, error) {
	if err := validateProblemInput(input); err != nil {
		return nil, err
	}
	p := &Problem{
		Title: input.Title, Statement: input.Statement, Difficulty: input.Difficulty,
		Tags: input.Tags, TimeLimitMS: input.TimeLimitMS, MemoryLimitMB: input.MemoryLimitMB,
		StarterCode: input.StarterCode,
	}
	if err := s.repo.Update(ctx, id, p); err != nil {
		return nil, err
	}
	return s.repo.GetByID(ctx, id)
}

func (s *Service) AddTestCase(ctx context.Context, tc *TestCase) error {
	if strings.TrimSpace(tc.ExpectedOutput) == "" {
		return ValidationError{Field: "expectedOutput", Message: "must not be empty"}
	}
	return s.repo.AddTestCase(ctx, tc)
}

// ListPublicTestCases returns ONLY sample test cases. This is the sole
// entry point the public-facing problem detail handler may call — never
// call repo.ListTestCases(..., false) from a public/unauthenticated path.
func (s *Service) ListPublicTestCases(ctx context.Context, problemID string) ([]TestCase, error) {
	return s.repo.ListTestCases(ctx, problemID, true)
}

// ListAllTestCases returns every test case including hidden ones. Only
// call this from admin-gated paths or the judge's own submission flow.
func (s *Service) ListAllTestCases(ctx context.Context, problemID string) ([]TestCase, error) {
	return s.repo.ListTestCases(ctx, problemID, false)
}

func validateProblemInput(input CreateProblemInput) error {
	if strings.TrimSpace(input.Title) == "" {
		return ValidationError{Field: "title", Message: "title is required"}
	}
	switch input.Difficulty {
	case DifficultyEasy, DifficultyMedium, DifficultyHard:
	default:
		return ValidationError{Field: "difficulty", Message: "must be easy, medium, or hard"}
	}
	if input.TimeLimitMS <= 0 {
		return ValidationError{Field: "timeLimitMs", Message: "must be greater than zero"}
	}
	if input.MemoryLimitMB <= 0 {
		return ValidationError{Field: "memoryLimitMb", Message: "must be greater than zero"}
	}
	return nil
}
