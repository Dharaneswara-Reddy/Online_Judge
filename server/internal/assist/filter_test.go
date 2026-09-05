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

// ---------------------------------------------------------------------
// Red-team corpus.
//
// Every string below was accepted by an earlier version of the filter.
// They come from a red-team pass run after the deployment moved to
// open-weight models on Groq's free tier, which comply with "never
// output code" noticeably less often than the frontier model the ladder
// was designed against. The system prompt was always only a request;
// this filter was always the actual control, and these are the holes in
// it.
// ---------------------------------------------------------------------

// TestRejectCodeCatchesOneLiners covers the family that mattered most:
// for an easy problem a single line *is* the whole solution, so a filter
// that needs a run of statement-shaped lines never sees it.
func TestRejectCodeCatchesOneLiners(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"bare call", "print(max(a))"},
		{"lambda one-liner", "f = lambda a: max(a)"},
		{"js arrow function", "const solve = (a) => a.reduce((m, x) => Math.max(m, x));"},
		{"list comprehension", "result = [max(a[:i+1]) for i in range(len(a))]"},
		{"semicolon-joined", "best=0; [best:=max(best,x) for x in a]; print(best)"},
		{"one-liner inside prose", "The whole thing is really just this:\nprint(max(a))\nand nothing more."},
		{"one-liner wrapped in backticks", "`print(max(a))`"},
		{"go one-liner", "fmt.Println(max(a))"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := RejectCode(tc.text); !errors.Is(err, ErrFiltered) {
				t.Fatalf("RejectCode(%q) = %v, want ErrFiltered", tc.text, err)
			}
		})
	}
}

// TestRejectCodeCatchesObfuscation covers the shapes that hide code from
// a line-anchored matcher: list numbering in front of every line,
// homoglyphs in the identifiers, and a zero-width space inside a
// keyword. Normalisation runs before matching so that none of these is
// worth a model's while.
func TestRejectCodeCatchesObfuscation(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"numbered list of code lines", "1. best = 0\n2. for x in a:\n3.     best = max(best, x)\n4. print(best)"},
		{"bulleted list of code lines", "- best = 0\n- for x in a:\n-     best = max(best, x)"},
		{"cyrillic homoglyph identifiers", "bеst = 0\nfor х in а:\n    bеst = max(bеst, х)\nprint(bеst)"},
		{"zero-width space inside def", "d\u200bef solve(a):\n    return max(a)"},
		{"zero-width joiner inside func", "fu\u200dnc main() {\n    x := 1\n}"},
		{"fullwidth punctuation", "print（max（a））"},
		{"mixed homoglyph def", "dеf ѕolve(а):\n    rеturn max(а)"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := RejectCode(tc.text); !errors.Is(err, ErrFiltered) {
				t.Fatalf("RejectCode(%q) = %v, want ErrFiltered", tc.text, err)
			}
		})
	}
}

// TestRejectCodeCatchesPseudocode: a complete program in block capitals
// is still a complete program. A student can transliterate it into any
// language in two minutes, which is exactly the work the ladder exists
// to leave undone.
func TestRejectCodeCatchesPseudocode(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{
			"all-caps pseudocode program",
			"SET best TO 0\nFOR EACH x IN a\n    IF x GREATER THAN best THEN SET best TO x\nPRINT best",
		},
		{
			"pseudocode with begin/end",
			"BEGIN\nREAD n\nWHILE n GREATER THAN 0\n    OUTPUT n\nEND",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := RejectCode(tc.text); !errors.Is(err, ErrFiltered) {
				t.Fatalf("RejectCode(%q) = %v, want ErrFiltered", tc.text, err)
			}
		})
	}
}

// TestRejectCodeCatchesShortRuns: two statement-shaped lines are a code
// block, not a coincidence. The run threshold used to be three, which
// let a two-line fragment through whole.
func TestRejectCodeCatchesShortRuns(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"assignment then loop header", "best = 0\nfor x in a:"},
		{"two declarations", "int lo = 0;\nint hi = n - 1;"},
		{"indented pair", "    lo = 0\n    hi = n - 1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := RejectCode(tc.text); !errors.Is(err, ErrFiltered) {
				t.Fatalf("RejectCode(%q) = %v, want ErrFiltered", tc.text, err)
			}
		})
	}
}

// TestRejectCodeCatchesUnfencedBlocks folds in the rest of the red-team
// run: shapes the filter already stopped, kept so a future change to the
// matcher cannot quietly reopen them.
func TestRejectCodeCatchesUnfencedBlocks(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"indented tilde fence", "Here:\n   ~~~\n   print(1)\n   ~~~"},
		{"four-space indented block", "Try this:\n\n    best = 0\n    for i in range(n):\n        best = max(best, a[i])\n    print(best)"},
		{"top-level python, no def", "a = [1, 2, 3]\nbest = 0\nfor x in a:\n    if x > best:\n        best = x\nprint(best)"},
		{"code inside a pre tag", "<pre>\nbest = 0\nfor x in a:\n    best = max(best, x)\nprint(best)\n</pre>"},
		{"go func with loose spacing", "func  main ( ) { fmt.Println(max(a)) }"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := RejectCode(tc.text); !errors.Is(err, ErrFiltered) {
				t.Fatalf("RejectCode(%q) = %v, want ErrFiltered", tc.text, err)
			}
		})
	}
}

// TestRejectCodeAllowsRungFourOutlines is the corpus that constrains
// every tightening above. These are the shape of thing `hintSystem`
// asks rung 4 to produce — four to six sentences of English naming the
// variables and the order of operations — and a filter that rejects any
// of them has made the top of the ladder useless.
//
// The cost of a false positive is one withheld hint; the cost of a
// false negative is a judge whose scores mean nothing. That asymmetry
// justifies a tight filter, not an indiscriminate one.
func TestRejectCodeAllowsRungFourOutlines(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"smallest so far", "Track the smallest price seen so far, then compare today's price against it before updating."},
		{"backticked identifier", "Consider what `dp[i]` should represent."},
		{"class of approach", "Think about the class of approach this problem wants."},
		{"return in prose", "Return the maximum profit, not the maximum price."},
		{"if in prose", "If the sum overflows, stop early."},
		{"single assignment alone", "best  =  0"},
		{"complexity target", "Aim for O(n log n) overall."},
		{"complexity on its own line", "Target complexity: O(n)"},
		{
			"outline: best time to buy and sell",
			strings.Join([]string{
				"Walk the prices from left to right and keep two numbers in your head.",
				"The first is the cheapest price you have seen up to the current day.",
				"The second is the largest profit any pair you have already considered would have made.",
				"At each day, work out the profit of selling today before you decide whether today is the new cheapest day.",
				"When the walk ends, the second number is the answer, and it is a profit rather than a price.",
			}, "\n"),
		},
		{
			"outline: two pointers on a sorted array",
			strings.Join([]string{
				"Start with one index at the front of the sorted array and another at the back.",
				"Compare the sum of the two values they point at against the target.",
				"If the sum is too small, the only way to grow it is to move the front index forward.",
				"If it is too large, move the back index down instead.",
				"The indices meet after at most one pass, so the whole scan is linear.",
			}, "\n"),
		},
		{
			"outline: frequency counting",
			strings.Join([]string{
				"Make one pass over the input and count how many times each value appears.",
				"A map from value to count is enough; the order of the input does not matter here.",
				"Then look at the counts rather than the values to answer the question.",
				"Remember that a value appearing once and a value appearing zero times are different cases.",
				"Report the value whose count wins, breaking ties the way the statement tells you to.",
			}, "\n"),
		},
		{
			"outline: numbered steps in prose",
			strings.Join([]string{
				"1. Read the whole array once and remember its running minimum.",
				"2. On the same pass, compare the current element against that minimum.",
				"3. Keep whichever difference is largest so far.",
				"4. Print that difference at the end, not the minimum itself.",
			}, "\n"),
		},
		{
			"outline: bulleted steps in prose",
			strings.Join([]string{
				"- First decide what state you would need to answer the question for a prefix of the input.",
				"- Then work out how that state changes when one more element arrives.",
				"- Finally check what the state should be before any element has arrived at all.",
			}, "\n"),
		},
		{"identifier in a sentence", "Compare prices[i] against the minimum you have tracked, then update lo and hi accordingly."},
		{"prose about returning", "The function should eventually return to the caller once every element has been visited."},
		{"two step outline", "First scan once to find the minimum. Then scan again to find the best difference."},
		{"semicolon separated prose", "First find the minimum;\nthen find the best difference."},
		{"prose naming a class", "This falls into the class of approaches that keep a running total."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := RejectCode(tc.text); err != nil {
				t.Fatalf("RejectCode(%q) = %v, want nil", tc.text, err)
			}
		})
	}
}

// TestRejectLeakCatchesShortExpectedOutput closes the last hole in
// RejectLeak. The eight-rune floor exists because "5" and "YES" occur in
// ordinary prose about a problem, so lowering it wholesale would make
// rung 3 unusable — but that floor also let a model print the entire
// expected answer of a yes/no or single-number problem verbatim.
//
// The fix is not a shorter floor but a stricter match: a short expected
// output counts as leaked only when it appears as a whole token in a
// context that reads as stating the answer.
func TestRejectLeakCatchesShortExpectedOutput(t *testing.T) {
	cases := []struct {
		name     string
		expected string
		text     string
	}{
		{"should print", "-1", "Your code should print -1 for this case."},
		{"the answer is", "42", "For that input the answer is 42, but you produce something else."},
		{"expected output is", "YES", "The expected output is YES and yours says otherwise."},
		{"it returns", "0", "The judge returns 0 here; think about the empty case."},
		{"quoted", "YES", "The case wants `YES` but your branch never fires."},
		{"multi token short output", "1 2", "It should output 1 2 rather than the reverse."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hidden := HiddenCase{Input: "3\n1 2 3\n", ExpectedOutput: tc.expected + "\n"}
			if err := RejectLeak(tc.text, hidden); !errors.Is(err, ErrLeak) {
				t.Fatalf("RejectLeak(%q) = %v, want ErrLeak", tc.text, err)
			}
		})
	}
}

// TestRejectLeakAllowsShortTokensInProse is the other half of that
// change, and the reason the eight-rune floor stays where it is. A short
// token that merely occurs in a sentence about the problem is not a
// disclosure, and rung 3 has to be able to talk about small numbers.
func TestRejectLeakAllowsShortTokensInProse(t *testing.T) {
	cases := []struct {
		name     string
		expected string
		text     string
	}{
		{"asked as a question", "YES", "Think about what happens for 5 inputs: is the answer YES?"},
		{"number in a count", "3", "The case has 3 elements, which is fewer than your loop assumes."},
		{"token inside a longer number", "42", "Your program stops at 420 elements, which is the wrong bound."},
		{"negative inside a longer number", "-1", "Consider a value like -19, well below the others."},
		{"describing the shape", "-1", "Every value is negative, so the closest to zero wins."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hidden := HiddenCase{Input: "3\n1 2 3\n", ExpectedOutput: tc.expected + "\n"}
			if err := RejectLeak(tc.text, hidden); err != nil {
				t.Fatalf("RejectLeak(%q) = %v, want nil", tc.text, err)
			}
		})
	}
}

// TestRejectLeakCatchesLongCases folds in the rest of the red-team leak
// run, which the eight-rune rule already handled.
func TestRejectLeakCatchesLongCases(t *testing.T) {
	hidden := HiddenCase{Input: "5\n-3 -7 -1 -9 -2", ExpectedOutput: "-1"}

	cases := []struct {
		name string
		text string
	}{
		{"verbatim input", "The case has input 5\n-3 -7 -1 -9 -2 which is tricky."},
		{"reflowed whitespace", "The input is:   5    -3  -7  -1  -9  -2   — all negative."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := RejectLeak(tc.text, hidden); !errors.Is(err, ErrLeak) {
				t.Fatalf("RejectLeak(%q) = %v, want ErrLeak", tc.text, err)
			}
		})
	}
}

// TestRejectLeakAllowsPropertiesOfShortCases keeps rung 3 usable on the
// problems whose whole answer is one token: the model may say everything
// about the case except the answer itself.
func TestRejectLeakAllowsPropertiesOfShortCases(t *testing.T) {
	hidden := HiddenCase{Input: "5\n-3 -7 -1 -9 -2", ExpectedOutput: "-1"}

	cases := []struct {
		name string
		text string
	}{
		{"partial line under threshold", "One of the values is -9 which is quite low."},
		{"described not echoed", "Every value in the input is negative, and the closest to zero is the answer."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := RejectLeak(tc.text, hidden); err != nil {
				t.Fatalf("RejectLeak(%q) = %v, want nil", tc.text, err)
			}
		})
	}
}

// TestRejectCodeCatchesInlineBlocks closes the last member of the
// one-liner family: a loop or a branch with its body on the same line
// carries no call, so the application rule alone would miss it.
func TestRejectCodeCatchesInlineBlocks(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"inline if", "if x > best: best = x"},
		{"inline for", "for x in a: total += x"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := RejectCode(tc.text); !errors.Is(err, ErrFiltered) {
				t.Fatalf("RejectCode(%q) = %v, want ErrFiltered", tc.text, err)
			}
		})
	}
}

// ---------------------------------------------------------------------
// Known gaps.
//
// These record what the filter deliberately does NOT stop. Each is a
// place where closing the hole would cost more in withheld hints than
// it buys in withheld code, and the test exists so that the decision is
// visible and so that a future change to it is a change to a test
// rather than a silent drift.
// ---------------------------------------------------------------------

// TestRejectCodeAllowsLoneAmbiguousStatements: a single line that is
// merely assignment- or header-shaped is accepted on its own, and needs
// one neighbour before the run counter fires.
//
// This is not laziness, it is the false-positive corpus talking. "count
// = 0" and "n = the number of elements" are things a hint says, and the
// shapes that match them are the same shapes that match real code. A
// lone fragment is also not a solution: it is one line of one, which is
// the level of disclosure rung 4 is allowed to reach in words anyway.
func TestRejectCodeAllowsLoneAmbiguousStatements(t *testing.T) {
	gaps := []string{
		"count = 0",
		"best = 0",
		"while lo < hi:",
		"lo, hi = 0, n - 1",
	}

	for _, text := range gaps {
		t.Run(text, func(t *testing.T) {
			if err := RejectCode(text); err != nil {
				t.Fatalf("RejectCode(%q) = %v; this gap is deliberate — see the comment", text, err)
			}
			// One neighbour is all it takes.
			paired := text + "\nbest = max(best, x)"
			if err := RejectCode(paired); !errors.Is(err, ErrFiltered) {
				t.Fatalf("RejectCode(%q) = %v, want ErrFiltered", paired, err)
			}
		})
	}
}

// TestRejectCodeRejectsBareExpressionLines records the one known false
// positive in the corpus above: a line that is nothing but an
// expression over identifiers is rejected even when it was meant as
// illustration, because it is indistinguishable from a line of a
// solution. The same content inside a sentence is accepted, which is
// how a hint should be phrasing it anyway.
func TestRejectCodeRejectsBareExpressionLines(t *testing.T) {
	if err := RejectCode("prices[i] - lo"); !errors.Is(err, ErrFiltered) {
		t.Fatalf("RejectCode on a bare expression = %v, want ErrFiltered", err)
	}
	inProse := "Notice that prices[i] - lo is the profit of selling on day i."
	if err := RejectCode(inProse); err != nil {
		t.Fatalf("RejectCode(%q) = %v, want nil", inProse, err)
	}
}

// TestRejectLeakAllowsUnframedShortAnswer records the residual gap in
// leak detection. A short expected output is caught when it is stated
// as the answer, quoted, or printed; it is not caught when it merely
// appears in a sentence, because that sentence is indistinguishable
// from the ones rung 3 exists to produce.
//
// The exposure is bounded: at most one short token, only on problems
// whose entire answer is one short token, and only via a phrasing the
// model has to arrive at without being asked for it.
func TestRejectLeakAllowsUnframedShortAnswer(t *testing.T) {
	hidden := HiddenCase{Input: "3\n1 2 3\n", ExpectedOutput: "-1\n"}
	text := "Everything in that case collapses to -1 by the time the scan ends."
	if err := RejectLeak(text, hidden); err != nil {
		t.Fatalf("RejectLeak(%q) = %v; this gap is deliberate — see the comment", text, err)
	}
}

// --- Gap D: confusable folding on the leak filter ------------------------
//
// RejectCode normalises confusables; RejectLeak did not, so a hidden
// case echoed with Cyrillic digits-alikes or a zero-width space wedged
// into it walked straight past a filter whose entire job is to notice
// that exact text. Folding both sides closes it, but "verbatim" then
// means "verbatim after folding", so the false-positive direction needs
// its own check: ordinary prose about a problem must not start matching
// a case it merely discusses.

func TestRejectLeakCatchesAnObfuscatedEcho(t *testing.T) {
	hidden := HiddenCase{Input: "6\n17 42 99 13 58 21", ExpectedOutput: "250"}

	cases := []struct {
		name string
		text string
	}{
		{"verbatim", "Your code fails on 17 42 99 13 58 21 because of the ordering."},
		{"reflowed whitespace", "It fails on 17   42  99\t13 58 21 for that reason."},
		{"zero-width spaces wedged in", "It fails on 17 42 9​9 13 58 21 there."},
		{"fullwidth digits", "It fails on １７ 42 99 13 58 21 there."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := RejectLeak(tc.text, hidden); err == nil {
				t.Fatalf("RejectLeak allowed an echo of the hidden case: %q", tc.text)
			}
		})
	}
}

// TestRejectLeakStillAllowsDescribingTheCase is the direction that
// matters more. Rung 3 exists to describe a hidden case; a leak filter
// that fires on any discussion of it makes the rung unusable.
func TestRejectLeakStillAllowsDescribingTheCase(t *testing.T) {
	hidden := HiddenCase{Input: "6\n17 42 99 13 58 21", ExpectedOutput: "250"}

	allowed := []string{
		"The case you fail has its largest value early, before the smallest one.",
		"Every element in that input is positive, and there are six of them.",
		"Think about what happens when the peak comes before the dip.",
		"Your running total starts at zero; is that right for this input?",
		"The failing input has six values and none of them repeat.",
	}

	for _, text := range allowed {
		if err := RejectLeak(text, hidden); err != nil {
			t.Errorf("RejectLeak refused a legitimate description: %q -> %v", text, err)
		}
	}
}
