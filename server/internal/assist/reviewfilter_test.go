package assist

import (
	"strings"
	"testing"
)

// A review is judged by different rules than a hint, and the difference
// is the whole reason this file exists.
//
// RejectCode exists to stop the assistant handing a student the answer
// to a problem they have not solved. A review only ever runs after the
// judge has accepted the submission, so there is no answer left to give
// away — quoting three lines of the student's own code back at them
// while explaining a naming choice gives them nothing they did not
// write themselves.
//
// What a review must still never do is hand back a rewritten program.
// "Here is how I would have written it" is an editorial, it is the
// thing the student would copy next time, and on a judge it is how a
// review becomes a solution generator by the back door.
//
// So the line is length and shape, not the presence of code at all.

func TestRejectReviewAllowsAShortIllustrativeSnippet(t *testing.T) {
	allowed := []string{
		"Your loop reads clearly. One small thing: `total += x` inside the branch could sit outside it.",
		"Consider renaming `a` to `prices`; the rest of the function reads well.\n\n```python\nlo = min(lo, price)\n```\n\nThat single line carries the invariant.",
		"The complexity is linear in the number of days and constant in space, which is the intended bound.",
		"Naming: `mp` is doing a lot of work for two characters. `seen` or `index_of` would say what it holds.",
		"You handle the empty input implicitly by starting the accumulator at zero. Worth a comment.",
	}
	for _, text := range allowed {
		if err := RejectReviewDump(text); err != nil {
			t.Errorf("RejectReviewDump refused legitimate review prose:\n%q\n-> %v", text, err)
		}
	}
}

func TestRejectReviewBlocksAReplacementSolution(t *testing.T) {
	blocked := map[string]string{
		"whole function rewritten": "Cleaner:\n\n```python\ndef max_profit(prices):\n    lo = prices[0]\n    best = 0\n    for p in prices:\n        best = max(best, p - lo)\n        lo = min(lo, p)\n    return best\n```",
		"long fenced block":        "```python\n" + strings.Repeat("x = 1\n", 12) + "```",
		"go function definition":   "You could write:\n\n```go\nfunc maxProfit(prices []int) int {\n\tbest := 0\n\treturn best\n}\n```",
		"java entry point":         "```java\npublic static void main(String[] args) {\n    System.out.println(1);\n}\n```",
		"class definition":         "```python\nclass Solution:\n    def solve(self):\n        return 1\n```",
		"unfenced full program":    "import sys\ndata = sys.stdin.read().split()\nn = int(data[0])\nvals = list(map(int, data[1:]))\nprint(max(vals))",
		"c entry point":            "```c\nint main(void) {\n    return 0;\n}\n```",
	}
	for name, text := range blocked {
		t.Run(name, func(t *testing.T) {
			if err := RejectReviewDump(text); err == nil {
				t.Fatalf("RejectReviewDump allowed a replacement solution:\n%s", text)
			}
		})
	}
}

// The snippet allowance is a budget, not a loophole: several small
// blocks add up to the same handed-over program.
func TestRejectReviewBlocksManySmallBlocks(t *testing.T) {
	text := "First:\n```python\na = 1\n```\nThen:\n```python\nb = 2\n```\nThen:\n```python\nc = 3\n```\nThen:\n```python\nd = 4\n```"
	if err := RejectReviewDump(text); err == nil {
		t.Fatal("four snippets in one review were allowed; the budget is per review, not per block")
	}
}

func TestRejectReviewBlocksJudgeInternals(t *testing.T) {
	blocked := []string{
		"The hidden test case is `6\\n3 8 1 9 2 4` and it expects 8.",
		"Your submission failed on hidden test 3 which uses n = 1.",
	}
	for _, text := range blocked {
		if err := RejectReviewDump(text); err == nil {
			t.Errorf("RejectReviewDump allowed judge-internal disclosure: %q", text)
		}
	}
}

func TestRejectReviewIsSeparateFromTheHintFilter(t *testing.T) {
	// The snippet a review may keep is one the hint filter must still
	// refuse, because a hint runs on an unsolved problem.
	const snippet = "Consider:\n\n```python\nlo = min(lo, price)\n```\n"

	if err := RejectReviewDump(snippet); err != nil {
		t.Fatalf("review filter refused a short snippet: %v", err)
	}
	if err := RejectCode(snippet); err == nil {
		t.Fatal("the hint filter now allows a fenced block — the hint guarantee has been weakened")
	}
}

func TestRejectReviewHandlesEmptyInput(t *testing.T) {
	if err := RejectReviewDump(""); err != nil {
		t.Errorf("empty text should not be an error: %v", err)
	}
}
