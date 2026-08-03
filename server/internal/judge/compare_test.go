package judge

import "testing"

func TestOutputsMatch(t *testing.T) {
	cases := []struct {
		name             string
		expected, actual string
		want             bool
	}{
		{"exact match", "hello\n", "hello\n", true},
		{"trailing newline diff", "hello", "hello\n\n\n", true},
		{"trailing spaces per line", "hello \n5", "hello\n5", true},
		{"different content", "hello", "world", false},
		{"empty vs empty", "", "", true},
		{"whitespace only vs empty", "   \n", "", true},
		{"multiline match", "1\n2\n3", "1\n2\n3\n", true},
		{"multiline mismatch order", "1\n2\n3", "3\n2\n1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := OutputsMatch(tc.expected, tc.actual); got != tc.want {
				t.Errorf("OutputsMatch(%q, %q) = %v, want %v", tc.expected, tc.actual, got, tc.want)
			}
		})
	}
}
