package problem

import "context"

// Repository is the storage boundary for problems and test cases. The
// production implementation is backed by MongoDB (see the mongorepo
// subpackage); tests use the in-memory FakeRepository in problemtest so
// Service logic can be verified without a database.
type Repository interface {
	Create(ctx context.Context, p *Problem) error
	GetBySlug(ctx context.Context, slug string) (*Problem, error)
	GetByID(ctx context.Context, id string) (*Problem, error)
	List(ctx context.Context, filter ListFilter) ([]Problem, error)
	Update(ctx context.Context, id string, p *Problem) error

	AddTestCase(ctx context.Context, tc *TestCase) error
	ListTestCases(ctx context.Context, problemID string, sampleOnly bool) ([]TestCase, error)
}

type ListFilter struct {
	Difficulty string
	Tag        string
	Company    string
	Page       int
	PageSize   int
}
