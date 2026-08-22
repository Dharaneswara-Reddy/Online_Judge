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

	// Empty slices rather than nil: a nil slice is stored as BSON null,
	// and later array updates ($push of a company tag, for instance)
	// fail against null.
	tags := input.Tags
	if tags == nil {
		tags = []string{}
	}

	p := &Problem{
		Title: input.Title, Slug: slug, Statement: input.Statement,
		Difficulty: input.Difficulty, Tags: tags,
		CompanyTags: []CompanyTagSummary{},
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

func (s *Service) GetByIDs(ctx context.Context, ids []string) ([]Problem, error) {
	if len(ids) == 0 {
		return []Problem{}, nil
	}
	return s.repo.GetByIDs(ctx, ids)
}

// maxPageSize bounds any listing. Without it a single unauthenticated
// request can ask for the entire collection, statements included.
const maxPageSize = 100

// clampPaging keeps pagination inside sane bounds. It lives in the
// service rather than a controller so every caller inherits it.
func clampPaging(filter ListFilter) ListFilter {
	if filter.PageSize <= 0 {
		filter.PageSize = 20
	}
	if filter.PageSize > maxPageSize {
		filter.PageSize = maxPageSize
	}
	if filter.Page <= 0 {
		filter.Page = 1
	}
	return filter
}

// maxSearchLength caps the search term. The term becomes a regex against
// every title, so an unbounded string lets one request hand the database
// arbitrarily expensive work. Titles are far shorter than this, and a
// longer query is truncated rather than rejected: truncation only widens
// the match set, so a paste into the box still finds something instead of
// erroring.
const maxSearchLength = 100

// normalizeSearch trims the search term and bounds its length. It lives in
// the service so List and Count normalise identically — if they disagreed,
// a padded query would list every problem while reporting a total of zero.
func normalizeSearch(filter ListFilter) ListFilter {
	search := strings.TrimSpace(filter.Search)
	// Count runes, not bytes, so a multi-byte term is never cut mid-character.
	if runes := []rune(search); len(runes) > maxSearchLength {
		search = string(runes[:maxSearchLength])
	}
	filter.Search = search
	return filter
}

func (s *Service) List(ctx context.Context, filter ListFilter) ([]Problem, error) {
	return s.repo.List(ctx, clampPaging(normalizeSearch(filter)))
}

// Count returns the total number of problems matching the filter,
// ignoring pagination, so clients can render page controls.
func (s *Service) Count(ctx context.Context, filter ListFilter) (int, error) {
	return s.repo.Count(ctx, normalizeSearch(filter))
}

// SolvedCountsByDifficulty turns a set of solved problem IDs into a
// per-difficulty tally for the profile page. Difficulties with no solves
// are still present with a zero count so the UI can render a stable set
// of bars.
func (s *Service) SolvedCountsByDifficulty(ctx context.Context, problemIDs []string) (map[Difficulty]int, error) {
	counts := map[Difficulty]int{DifficultyEasy: 0, DifficultyMedium: 0, DifficultyHard: 0}
	problems, err := s.GetByIDs(ctx, problemIDs)
	if err != nil {
		return nil, err
	}
	for _, p := range problems {
		counts[p.Difficulty]++
	}
	return counts, nil
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

// ReplaceTestCases swaps a problem's entire test case set for a new one.
//
// It is the only way to correct stored test data — a wrong expected
// output otherwise survives forever, since the seeder skips problems
// that already have cases. It is destructive by nature, so callers must
// ask for it explicitly; nothing invokes it implicitly.
//
// The whole replacement is validated before anything is deleted, and an
// empty set is refused: a problem with no test cases accepts every
// submission.
func (s *Service) ReplaceTestCases(ctx context.Context, problemID string, tcs []TestCase) error {
	// Steps to follow while replacing a problem's test cases
	// ========================================================

	// 1. Refuse to leave the problem with nothing to judge against
	if problemID == "" {
		return ValidationError{Field: "problemId", Message: "is required"}
	}
	if len(tcs) == 0 {
		return ValidationError{Field: "testCases", Message: "must not be empty"}
	}

	// 2. Validate every replacement first, so a typo in the new set never
	//    destroys the old one
	for i := range tcs {
		if strings.TrimSpace(tcs[i].ExpectedOutput) == "" {
			return ValidationError{
				Field:   fmt.Sprintf("testCases[%d].expectedOutput", i),
				Message: "must not be empty",
			}
		}
	}

	// 3. Drop the old set
	if _, err := s.repo.DeleteTestCases(ctx, problemID); err != nil {
		return err
	}

	// 4. Insert the new one, each case bound to this problem whatever the
	//    caller filled in
	for i := range tcs {
		tc := tcs[i]
		tc.ID = ""
		tc.ProblemID = problemID
		if err := s.repo.AddTestCase(ctx, &tc); err != nil {
			return err
		}
	}
	return nil
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
