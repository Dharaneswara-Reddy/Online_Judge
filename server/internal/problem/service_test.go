package problem_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/toji339/online-judge/internal/problem"
	"github.com/toji339/online-judge/internal/problem/problemtest"
)

func validInput() problem.CreateProblemInput {
	return problem.CreateProblemInput{
		Title:         "Two Sum",
		Statement:     "Given an array of integers...",
		Difficulty:    problem.DifficultyEasy,
		Tags:          []string{"arrays", "hash-table"},
		TimeLimitMS:   2000,
		MemoryLimitMB: 256,
	}
}

func TestService_Create_HappyPath(t *testing.T) {
	svc := problem.NewService(problemtest.NewFakeRepository())
	p, err := svc.Create(context.Background(), validInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.ID == "" {
		t.Fatal("expected ID to be set")
	}
	if p.Slug != "two-sum" {
		t.Errorf("slug = %q, want %q", p.Slug, "two-sum")
	}
	if p.Title != "Two Sum" {
		t.Errorf("title = %q, want %q", p.Title, "Two Sum")
	}
}

func TestService_Create_DuplicateTitle_IncrementsSuffix(t *testing.T) {
	svc := problem.NewService(problemtest.NewFakeRepository())
	_, err := svc.Create(context.Background(), validInput())
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	p2, err := svc.Create(context.Background(), validInput())
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if p2.Slug != "two-sum-2" {
		t.Errorf("slug = %q, want %q", p2.Slug, "two-sum-2")
	}
}

func TestService_Create_MissingTitle(t *testing.T) {
	svc := problem.NewService(problemtest.NewFakeRepository())
	input := validInput()
	input.Title = "   "
	_, err := svc.Create(context.Background(), input)

	var ve problem.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	if ve.Field != "title" {
		t.Errorf("field = %q, want %q", ve.Field, "title")
	}
}

func TestService_Create_InvalidDifficulty(t *testing.T) {
	svc := problem.NewService(problemtest.NewFakeRepository())
	input := validInput()
	input.Difficulty = "insane"
	_, err := svc.Create(context.Background(), input)

	var ve problem.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	if ve.Field != "difficulty" {
		t.Errorf("field = %q, want %q", ve.Field, "difficulty")
	}
}

func TestService_Create_ZeroTimeLimit(t *testing.T) {
	svc := problem.NewService(problemtest.NewFakeRepository())
	input := validInput()
	input.TimeLimitMS = 0
	_, err := svc.Create(context.Background(), input)

	var ve problem.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	if ve.Field != "timeLimitMs" {
		t.Errorf("field = %q, want %q", ve.Field, "timeLimitMs")
	}
}

func TestService_Update_HappyPath(t *testing.T) {
	repo := problemtest.NewFakeRepository()
	svc := problem.NewService(repo)

	p, err := svc.Create(context.Background(), validInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	input := validInput()
	input.Title = "Three Sum"
	updated, err := svc.Update(context.Background(), p.ID, input)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Title != "Three Sum" {
		t.Errorf("title = %q, want %q", updated.Title, "Three Sum")
	}
	// Slug should NOT change on update — it was set at creation.
	if updated.Slug != "two-sum" {
		t.Errorf("slug changed unexpectedly to %q", updated.Slug)
	}
}

// TestService_Update_NeverStoresNilSlices is the guard Create has and
// Update lacked. A nil slice is stored as BSON null: the problem then
// matches no tag query, so it silently disappears from every tag view,
// and $push cannot append a company tag to null either.
func TestService_Update_NeverStoresNilSlices(t *testing.T) {
	repo := problemtest.NewFakeRepository()
	svc := problem.NewService(repo)
	ctx := context.Background()

	p, err := svc.Create(ctx, validInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	input := validInput()
	input.Tags = nil
	input.StarterCode = nil
	updated, err := svc.Update(ctx, p.ID, input)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if updated.Tags == nil {
		t.Error("tags is nil — it would be stored as BSON null")
	}
	if len(updated.Tags) != 0 {
		t.Errorf("tags = %v, want empty", updated.Tags)
	}
	if updated.StarterCode == nil {
		t.Error("starterCode is nil — it would be stored as BSON null")
	}

	stored, err := svc.GetByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.Tags == nil || stored.StarterCode == nil {
		t.Errorf("stored problem has nil slices: tags=%v starterCode=%v", stored.Tags, stored.StarterCode)
	}
}

// TestService_Update_KeepsCompanyTags pins the other half of the same
// concern: the update writes a fixed field list, and the denormalised
// company tag summary must not be part of it.
func TestService_Update_KeepsCompanyTags(t *testing.T) {
	repo := problemtest.NewFakeRepository()
	svc := problem.NewService(repo)
	ctx := context.Background()

	p, err := svc.Create(ctx, validInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.CompanyTags == nil {
		t.Error("Create left companyTags nil — $push cannot append to BSON null")
	}
}

func TestService_AddTestCase_EmptyExpectedOutput(t *testing.T) {
	svc := problem.NewService(problemtest.NewFakeRepository())
	err := svc.AddTestCase(context.Background(), &problem.TestCase{
		ProblemID:      "fake-1",
		Input:          "1 2",
		ExpectedOutput: "   ",
	})

	var ve problem.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	if ve.Field != "expectedOutput" {
		t.Errorf("field = %q, want %q", ve.Field, "expectedOutput")
	}
}

func TestService_ListPublicTestCases_OnlySamples(t *testing.T) {
	repo := problemtest.NewFakeRepository()
	svc := problem.NewService(repo)
	ctx := context.Background()

	_ = svc.AddTestCase(ctx, &problem.TestCase{ProblemID: "p1", Input: "1", ExpectedOutput: "2", IsSample: true})
	_ = svc.AddTestCase(ctx, &problem.TestCase{ProblemID: "p1", Input: "3", ExpectedOutput: "4", IsSample: false})
	_ = svc.AddTestCase(ctx, &problem.TestCase{ProblemID: "p1", Input: "5", ExpectedOutput: "6", IsSample: true})

	tcs, err := svc.ListPublicTestCases(ctx, "p1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tcs) != 2 {
		t.Fatalf("got %d test cases, want 2", len(tcs))
	}
	for _, tc := range tcs {
		if !tc.IsSample {
			t.Errorf("got non-sample test case %q in public list", tc.ID)
		}
	}
}

func TestService_ListAllTestCases_IncludesHidden(t *testing.T) {
	repo := problemtest.NewFakeRepository()
	svc := problem.NewService(repo)
	ctx := context.Background()

	_ = svc.AddTestCase(ctx, &problem.TestCase{ProblemID: "p1", Input: "1", ExpectedOutput: "2", IsSample: true})
	_ = svc.AddTestCase(ctx, &problem.TestCase{ProblemID: "p1", Input: "3", ExpectedOutput: "4", IsSample: false})

	tcs, err := svc.ListAllTestCases(ctx, "p1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tcs) != 2 {
		t.Fatalf("got %d test cases, want 2", len(tcs))
	}
}

func TestService_GetBySlug_NotFound(t *testing.T) {
	svc := problem.NewService(problemtest.NewFakeRepository())
	_, err := svc.GetBySlug(context.Background(), "nonexistent")
	if !errors.Is(err, problem.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// searchCatalogue seeds a set of problems whose titles, difficulties and
// tags deliberately overlap, so a test can tell a search hit apart from a
// difficulty or tag hit. One title carries regex metacharacters.
func searchCatalogue(t *testing.T) (*problem.Service, *problemtest.FakeRepository) {
	t.Helper()
	repo := problemtest.NewFakeRepository()
	svc := problem.NewService(repo)

	seed := []struct {
		title      string
		difficulty problem.Difficulty
		tags       []string
	}{
		{"Two Sum", problem.DifficultyEasy, []string{"arrays", "hash-table"}},
		{"Three Sum", problem.DifficultyMedium, []string{"arrays", "two-pointers"}},
		{"Maximum Subarray Sum", problem.DifficultyHard, []string{"dp"}},
		{"Valid Parentheses", problem.DifficultyEasy, []string{"stack"}},
		{"Regex (a+b)* Matching", problem.DifficultyHard, []string{"dp"}},
	}
	for _, s := range seed {
		input := validInput()
		input.Title = s.title
		input.Difficulty = s.difficulty
		input.Tags = s.tags
		if _, err := svc.Create(context.Background(), input); err != nil {
			t.Fatalf("seed %q: %v", s.title, err)
		}
	}
	return svc, repo
}

// titles collects the titles of a listing so assertions can compare sets
// without depending on the order the repository happens to return.
func titles(problems []problem.Problem) map[string]bool {
	got := make(map[string]bool, len(problems))
	for _, p := range problems {
		got[p.Title] = true
	}
	return got
}

func assertTitles(t *testing.T, got []problem.Problem, want ...string) {
	t.Helper()
	have := titles(got)
	if len(have) != len(want) {
		t.Fatalf("got %d problems %v, want %d %v", len(have), have, len(want), want)
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("missing %q from results %v", w, have)
		}
	}
}

func TestService_List_SearchMatchesTitleCaseInsensitively(t *testing.T) {
	svc, _ := searchCatalogue(t)
	ctx := context.Background()

	for _, query := range []string{"sum", "SUM", "SuM"} {
		got, err := svc.List(ctx, problem.ListFilter{Search: query})
		if err != nil {
			t.Fatalf("list %q: %v", query, err)
		}
		assertTitles(t, got, "Two Sum", "Three Sum", "Maximum Subarray Sum")
	}

	got, err := svc.List(ctx, problem.ListFilter{Search: "tWo sUm"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	assertTitles(t, got, "Two Sum")
}

func TestService_List_SearchCombinesWithDifficultyAndTag(t *testing.T) {
	svc, _ := searchCatalogue(t)
	ctx := context.Background()

	// Search AND difficulty: "Three Sum" also matches "sum" but is medium.
	got, err := svc.List(ctx, problem.ListFilter{Search: "sum", Difficulty: "easy"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	assertTitles(t, got, "Two Sum")

	// Search AND tag: "Maximum Subarray Sum" matches "sum" but is not tagged arrays.
	got, err = svc.List(ctx, problem.ListFilter{Search: "sum", Tag: "arrays"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	assertTitles(t, got, "Two Sum", "Three Sum")

	// All three together narrow to a single problem.
	got, err = svc.List(ctx, problem.ListFilter{Search: "sum", Tag: "arrays", Difficulty: "medium"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	assertTitles(t, got, "Three Sum")

	// A search that matches nothing must not resurrect the filtered-out rest.
	got, err = svc.List(ctx, problem.ListFilter{Search: "graph", Difficulty: "easy"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	assertTitles(t, got)
}

func TestService_List_BlankSearchReturnsEverything(t *testing.T) {
	svc, _ := searchCatalogue(t)
	ctx := context.Background()

	for _, query := range []string{"", "   ", "\t\n "} {
		got, err := svc.List(ctx, problem.ListFilter{Search: query})
		if err != nil {
			t.Fatalf("list %q: %v", query, err)
		}
		assertTitles(t, got,
			"Two Sum", "Three Sum", "Maximum Subarray Sum",
			"Valid Parentheses", "Regex (a+b)* Matching")
	}
}

func TestService_List_SearchIsTrimmed(t *testing.T) {
	svc, _ := searchCatalogue(t)

	got, err := svc.List(context.Background(), problem.ListFilter{Search: "  two sum \n"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	assertTitles(t, got, "Two Sum")
}

// Count feeds the client's page controls, so it has to normalise the search
// exactly as List does — otherwise a padded query lists five problems while
// claiming a total of zero.
func TestService_Count_NormalisesSearchLikeList(t *testing.T) {
	svc, _ := searchCatalogue(t)
	ctx := context.Background()

	total, err := svc.Count(ctx, problem.ListFilter{Search: "   "})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 5 {
		t.Errorf("count with blank search = %d, want 5", total)
	}

	total, err = svc.Count(ctx, problem.ListFilter{Search: "  sum  "})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 3 {
		t.Errorf("count with padded search = %d, want 3", total)
	}
}

func TestService_List_SearchTreatsRegexMetacharactersLiterally(t *testing.T) {
	svc, _ := searchCatalogue(t)
	ctx := context.Background()

	got, err := svc.List(ctx, problem.ListFilter{Search: "(a+b)*"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	assertTitles(t, got, "Regex (a+b)* Matching")

	// ".*" matches every title when read as a pattern and no title when
	// read as text, which is the whole point.
	got, err = svc.List(ctx, problem.ListFilter{Search: ".*"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	assertTitles(t, got)
}

func TestService_List_SearchLengthIsBounded(t *testing.T) {
	svc, repo := searchCatalogue(t)

	long := strings.Repeat("a", 5000)
	if _, err := svc.List(context.Background(), problem.ListFilter{Search: long}); err != nil {
		t.Fatalf("list: %v", err)
	}

	seen := repo.LastListFilter().Search
	if len(seen) == 0 || len(seen) >= len(long) {
		t.Fatalf("repository saw a search of %d chars, want it bounded below %d", len(seen), len(long))
	}
}
