package mongorepo

import (
	"testing"

	"github.com/toji339/online-judge/internal/problem"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestListQuery_OmitsUnsetFilters(t *testing.T) {
	query := listQuery(problem.ListFilter{})
	if len(query) != 0 {
		t.Fatalf("empty filter produced %v, want an empty query", query)
	}
}

// The four filters have to intersect. A search that replaced the difficulty
// or tag clause would silently widen every filtered listing.
func TestListQuery_CombinesSearchWithTheOtherFilters(t *testing.T) {
	query := listQuery(problem.ListFilter{
		Difficulty: "easy",
		Tag:        "arrays",
		Company:    "Google",
		Search:     "two sum",
	})

	for _, key := range []string{"difficulty", "tags", "company_tags.company", "title"} {
		if _, ok := query[key]; !ok {
			t.Errorf("query is missing the %q clause: %v", key, query)
		}
	}
}

// A user's search string reaches Mongo as a regex, so metacharacters must be
// escaped: unescaped, "(" is a syntax error the driver rejects and "(a+)+$"
// is a catastrophic-backtracking pattern.
func TestListQuery_EscapesRegexMetacharacters(t *testing.T) {
	cases := []struct {
		search string
		want   string
	}{
		{"c++", `c\+\+`},
		{".*", `\.\*`},
		{"(a+)+$", `\(a\+\)\+\$`},
	}

	for _, tc := range cases {
		clause, ok := listQuery(problem.ListFilter{Search: tc.search})["title"].(bson.M)
		if !ok {
			t.Fatalf("search %q did not produce a title clause", tc.search)
		}
		if got := clause["$regex"]; got != tc.want {
			t.Errorf("search %q compiled to %q, want %q", tc.search, got, tc.want)
		}
	}
}

func TestListQuery_SearchIsCaseInsensitive(t *testing.T) {
	clause, ok := listQuery(problem.ListFilter{Search: "two sum"})["title"].(bson.M)
	if !ok {
		t.Fatal("expected a title clause")
	}
	if got := clause["$options"]; got != "i" {
		t.Errorf("regex options = %q, want %q", got, "i")
	}
}
