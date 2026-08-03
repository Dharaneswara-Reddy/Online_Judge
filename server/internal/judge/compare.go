package judge

import "strings"

// NormalizeOutput trims trailing whitespace from each line and trailing
// newlines from the whole string, so that minor formatting differences
// between expected and actual output don't produce false negatives.
func NormalizeOutput(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t\r")
	}
	normalized := strings.Join(lines, "\n")
	return strings.TrimRight(normalized, "\n")
}

// OutputsMatch returns true if expected and actual outputs are
// semantically equivalent after normalization.
func OutputsMatch(expected, actual string) bool {
	return NormalizeOutput(expected) == NormalizeOutput(actual)
}
