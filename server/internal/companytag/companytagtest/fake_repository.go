// Package companytagtest provides an in-memory companytag.Repository so
// the deduplication and aggregation rules can be tested without a
// database.
package companytagtest

import (
	"context"
	"sort"
	"strconv"
	"sync"

	"github.com/toji339/online-judge/internal/companytag"
)

// FakeRepository stores tags in a slice and enforces the same unique
// (problem, user, company) constraint the MongoDB index provides.
type FakeRepository struct {
	mu     sync.Mutex
	nextID int
	tags   []companytag.Tag

	// summaries mirrors the denormalised per-problem counts:
	// problemID -> company -> count
	summaries map[string]map[string]int

	// Failure injection. The two-collection write can only be tested for
	// recoverability if the second write can be made to fail.
	FailIncrementSummary error
	FailRemove           error
}

// New creates an empty FakeRepository.
func New() *FakeRepository {
	return &FakeRepository{summaries: make(map[string]map[string]int)}
}

func (r *FakeRepository) Add(_ context.Context, tag *companytag.Tag) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, existing := range r.tags {
		if existing.ProblemID == tag.ProblemID &&
			existing.UserID == tag.UserID &&
			existing.Company == tag.Company {
			return companytag.ErrAlreadyTagged
		}
	}

	r.nextID++
	tag.ID = strconv.Itoa(r.nextID)
	r.tags = append(r.tags, *tag)
	return nil
}

func (r *FakeRepository) IncrementSummary(_ context.Context, problemID, company string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.FailIncrementSummary != nil {
		return r.FailIncrementSummary
	}
	if r.summaries[problemID] == nil {
		r.summaries[problemID] = make(map[string]int)
	}
	r.summaries[problemID][company]++
	return nil
}

// Remove deletes one report by id, the compensating half of a tag.
func (r *FakeRepository) Remove(_ context.Context, tagID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.FailRemove != nil {
		return r.FailRemove
	}
	for i, tag := range r.tags {
		if tag.ID == tagID {
			r.tags = append(r.tags[:i], r.tags[i+1:]...)
			return nil
		}
	}
	return nil
}

// RecountSummary rebuilds one company's count from the stored reports,
// which are the authority.
func (r *FakeRepository) RecountSummary(_ context.Context, problemID, company string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	count := 0
	for _, tag := range r.tags {
		if tag.ProblemID == problemID && tag.Company == company {
			count++
		}
	}
	if r.summaries[problemID] == nil {
		r.summaries[problemID] = make(map[string]int)
	}
	if count == 0 {
		delete(r.summaries[problemID], company)
		return nil
	}
	r.summaries[problemID][company] = count
	return nil
}

// SummaryCount reports the denormalised count a test should be able to
// trust, and SetSummaryCount forces it out of step so the repair path
// can be exercised.
func (r *FakeRepository) SummaryCount(problemID, company string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.summaries[problemID][company]
}

// SetSummaryCount overwrites the denormalised count, standing in for a
// request that died between the two writes.
func (r *FakeRepository) SetSummaryCount(problemID, company string, count int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.summaries[problemID] == nil {
		r.summaries[problemID] = make(map[string]int)
	}
	r.summaries[problemID][company] = count
}

func (r *FakeRepository) ListForProblem(_ context.Context, problemID string) ([]companytag.CompanyCount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := []companytag.CompanyCount{}
	for company, count := range r.summaries[problemID] {
		out = append(out, companytag.CompanyCount{Company: company, TagCount: count, ProblemCount: 1})
	}
	sortByPopularity(out)
	return out, nil
}

func (r *FakeRepository) ListCompanies(_ context.Context, limit int) ([]companytag.CompanyCount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	tagCounts := make(map[string]int)
	problems := make(map[string]map[string]bool)
	for _, tag := range r.tags {
		tagCounts[tag.Company]++
		if problems[tag.Company] == nil {
			problems[tag.Company] = make(map[string]bool)
		}
		problems[tag.Company][tag.ProblemID] = true
	}

	out := []companytag.CompanyCount{}
	for company, count := range tagCounts {
		out = append(out, companytag.CompanyCount{
			Company:      company,
			TagCount:     count,
			ProblemCount: len(problems[company]),
		})
	}
	sortByPopularity(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *FakeRepository) ListUserTags(_ context.Context, problemID, userID string) ([]companytag.Tag, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := []companytag.Tag{}
	for _, tag := range r.tags {
		if tag.ProblemID == problemID && tag.UserID == userID {
			out = append(out, tag)
		}
	}
	return out, nil
}

// sortByPopularity orders most-tagged first, with the company name as a
// stable tie-break.
func sortByPopularity(counts []companytag.CompanyCount) {
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].TagCount == counts[j].TagCount {
			return counts[i].Company < counts[j].Company
		}
		return counts[i].TagCount > counts[j].TagCount
	})
}
