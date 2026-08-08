// Package problemtest provides test doubles for the problem domain.
package problemtest

import (
	"context"
	"fmt"
	"sync"

	"github.com/toji339/online-judge/internal/problem"
)

// FakeRepository is an in-memory implementation of problem.Repository
// used in unit tests so Service logic can be verified without MongoDB.
type FakeRepository struct {
	mu        sync.Mutex
	problems  []problem.Problem
	testCases []problem.TestCase
	nextID    int
}

func NewFakeRepository() *FakeRepository {
	return &FakeRepository{nextID: 1}
}

func (r *FakeRepository) Create(_ context.Context, p *problem.Problem) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p.ID = fmt.Sprintf("fake-%d", r.nextID)
	r.nextID++
	r.problems = append(r.problems, *p)
	return nil
}

func (r *FakeRepository) GetBySlug(_ context.Context, slug string) (*problem.Problem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.problems {
		if p.Slug == slug {
			cp := p
			return &cp, nil
		}
	}
	return nil, problem.ErrNotFound
}

func (r *FakeRepository) GetByID(_ context.Context, id string) (*problem.Problem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.problems {
		if p.ID == id {
			cp := p
			return &cp, nil
		}
	}
	return nil, problem.ErrNotFound
}

func (r *FakeRepository) List(_ context.Context, filter problem.ListFilter) ([]problem.Problem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var result []problem.Problem
	for _, p := range r.problems {
		if filter.Difficulty != "" && string(p.Difficulty) != filter.Difficulty {
			continue
		}
		if filter.Tag != "" && !containsTag(p.Tags, filter.Tag) {
			continue
		}
		result = append(result, p)
	}

	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	page := filter.Page
	if page <= 0 {
		page = 1
	}

	start := (page - 1) * pageSize
	if start >= len(result) {
		return []problem.Problem{}, nil
	}
	end := start + pageSize
	if end > len(result) {
		end = len(result)
	}
	return result[start:end], nil
}

func (r *FakeRepository) Update(_ context.Context, id string, p *problem.Problem) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, existing := range r.problems {
		if existing.ID == id {
			p.ID = id
			p.Slug = existing.Slug
			p.CreatedAt = existing.CreatedAt
			r.problems[i] = *p
			return nil
		}
	}
	return problem.ErrNotFound
}

func (r *FakeRepository) AddTestCase(_ context.Context, tc *problem.TestCase) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	tc.ID = fmt.Sprintf("tc-%d", r.nextID)
	r.nextID++
	r.testCases = append(r.testCases, *tc)
	return nil
}

func (r *FakeRepository) ListTestCases(_ context.Context, problemID string, sampleOnly bool) ([]problem.TestCase, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []problem.TestCase
	for _, tc := range r.testCases {
		if tc.ProblemID != problemID {
			continue
		}
		if sampleOnly && !tc.IsSample {
			continue
		}
		result = append(result, tc)
	}
	return result, nil
}

func containsTag(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}
