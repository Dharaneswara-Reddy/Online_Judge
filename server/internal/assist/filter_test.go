package assist

import (
	"errors"
	"strings"
	"testing"
)

// TestRejectCodeCatchesSource walks the shapes the filter exists to stop.
// Every one of these is a real thing a model has been observed to emit
// when asked for a "hint": the whole point of the ladder is that none of
// them reaches the student.
func TestRejectCodeCatchesSource(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"markdown fence", "Here is the idea:\n```python\nprint(1)\n```"},
		{"tilde fence", "Try this:\n~~~\nprint(1)\n~~~"},
		{"fence with no language", "Consider:\n```\nx\n```"},
		{"python def", "You want def solve(prices) to walk the list once."},
		{"go func", "Write func main() and read the input there."},
		{"java class", "Start from class Solution and fill in the method."},
		{"java entry point", "Add public static void main to the file."},
		{"c include", "You will need #include <stdio.h> at the top."},
		{"c main", "Declare int main(void) and read n from stdin."},
		{"three statements", "best = 0\nfor i in range(n):\n    best = max(best, a[i])"},
		{"three braced statements", "int lo = 0;\nint hi = n - 1;\nwhile (lo < hi) {"},
		{"return run", "return 0\nreturn 1\nreturn 2"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := RejectCode(tc.text)
			if !errors.Is(err, ErrFiltered) {
				t.Fatalf("RejectCode(%q) = %v, want ErrFiltered", tc.text, err)
			}
		})
	}
}

// TestRejectCodeAllowsProse is the other half of the contract, and the
// half that is easy to get wrong: a rung-4 outline names variables and
// quotes identifiers inline, and a filter that trips on those makes the
// top of the ladder useless.
func TestRejectCodeAllowsProse(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"identifier in prose", "Track the smallest price seen so far."},
		{"inline code span", "Walk the array once and compare `prices[i]` against it."},
		{"class of approach", "This is the class of approach that scans the input a single time."},
		{
			"full rung 4 outline",
			strings.Join([]string{
				"Keep two running values as you scan left to right.",
				"The first is the smallest price you have seen so far.",
				"The second is the best profit you could have banked so far.",
				"At each position, update the profit before you update the minimum.",
				"Answer with the best profit once the scan ends.",
			}, "\n"),
		},
		{"two statement-ish lines", "count = 0\nfor each element in the array, add one to it."},
		{"sentence starting with return", "Return the running maximum once the loop ends."},
		{"colon list", "There are two values to keep:\nthe running minimum, and the running best."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := RejectCode(tc.text); err != nil {
				t.Fatalf("RejectCode(%q) = %v, want nil", tc.text, err)
			}
		})
	}
}

func TestRejectCodeAllowsEmpty(t *testing.T) {
	if err := RejectCode(""); err != nil {
		t.Fatalf("RejectCode(\"\") = %v, want nil", err)
	}
}

func TestRejectLeakCatchesEchoedLines(t *testing.T) {
	hidden := HiddenCase{
		Input:          "7\n3 1 4 1 5 9 2 6\n",
		ExpectedOutput: "the answer is 41235\n",
	}

	cases := []struct {
		name string
		text string
	}{
		{"input line echoed", "The case that fails looks like 3 1 4 1 5 9 2 6 which is not sorted."},
		{"expected output echoed", "It should have printed the answer is 41235."},
		{"whole input normalised", "Your program is fed 7 3 1 4 1 5 9 2 6 and stops early."},
		{"reflowed whitespace", "Consider   3  1   4 1 5 9 2 6   as the sequence."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := RejectLeak(tc.text, hidden)
			if !errors.Is(err, ErrLeak) {
				t.Fatalf("RejectLeak(%q) = %v, want ErrLeak", tc.text, err)
			}
		})
	}
}

// TestRejectLeakAllowsProperties is the behaviour rung 3 depends on:
// the model may describe the case, it may not print it.
func TestRejectLeakAllowsProperties(t *testing.T) {
	hidden := HiddenCase{
		Input:          "7\n3 1 4 1 5 9 2 6\n",
		ExpectedOutput: "the answer is 41235\n",
	}

	cases := []struct {
		name string
		text string
	}{
		{"property only", "The failing case has a repeated value and its largest element is not last."},
		{"count mentioned", "It has 7 elements, which is an odd count."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := RejectLeak(tc.text, hidden); err != nil {
				t.Fatalf("RejectLeak(%q) = %v, want nil", tc.text, err)
			}
		})
	}
}

// TestRejectLeakIgnoresShortLines keeps the filter from firing on the
// single digits and one-word tokens that fill most test cases; "5" or
// "YES" appear in ordinary prose and are not a disclosure.
func TestRejectLeakIgnoresShortLines(t *testing.T) {
	hidden := HiddenCase{Input: "5\n", ExpectedOutput: "YES\n"}
	if err := RejectLeak("Think about what happens for 5 inputs: is the answer YES?", hidden); err != nil {
		t.Fatalf("RejectLeak on short tokens = %v, want nil", err)
	}
}

func TestRejectLeakEmptyCaseIsNoOp(t *testing.T) {
	if err := RejectLeak("anything at all", HiddenCase{}); err != nil {
		t.Fatalf("RejectLeak with empty case = %v, want nil", err)
	}
}
