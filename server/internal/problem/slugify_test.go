package problem

import "testing"

func TestSlugify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Two Sum", "two-sum"},
		{"  Longest   Common Subsequence!! ", "longest-common-subsequence"},
		{"123 Add Two Numbers", "123-add-two-numbers"},
		{"", "problem"},
		{"!!!", "problem"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := Slugify(tc.in); got != tc.want {
				t.Errorf("Slugify(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
