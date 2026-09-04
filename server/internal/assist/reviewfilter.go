package assist

import (
	"fmt"
	"regexp"
	"strings"
)

// The output filter for post-acceptance reviews.
//
// It is deliberately a different filter from RejectCode rather than a
// relaxed setting on it, because the two guard different things and
// sharing one control would eventually weaken the stricter of them.
//
// RejectCode protects an unsolved problem: any code at all is the answer
// the student was meant to find, so none is allowed. A review runs only
// after the judge has accepted the submission — the edge refuses to
// reach it otherwise — so there is no answer left to hand over, and
// quoting three lines of the student's own program back at them while
// explaining a naming choice discloses nothing they did not write.
//
// What a review must never return is a rewritten program. "Here is how
// I would have written it" is an editorial: it is the thing a student
// copies into the next problem, and it turns a review into a solution
// generator by the back door. So the boundary here is size and shape
// rather than the presence of code, and the budget is per review rather
// than per block — four small snippets are one handed-over program with
// prose in between.

// Review snippet budget. Small enough that nothing complete fits;
// generous enough to quote a loop body or an invariant.
const (
	// maxReviewSnippetLines is the longest fenced block a review may
	// contain. Three lines holds an invariant or a condition, and holds
	// no function.
	maxReviewSnippetLines = 3
	// maxReviewSnippetBlocks bounds how many such blocks may appear, so
	// the allowance cannot be spent repeatedly in one response.
	maxReviewSnippetBlocks = 2
)

// reviewDefinitionShapes are structures that only appear in a complete
// implementation. One is enough to refuse, regardless of length: a
// function definition is a replacement solution however short it is.
var reviewDefinitionShapes = []codeSignature{
	{"python function definition", regexp.MustCompile(`\bdef\s+[A-Za-z_]\w*\s*\(`)},
	{"go function definition", regexp.MustCompile(`\bfunc\s+[A-Za-z_]\w*\s*\(`)},
	{"javascript function definition", regexp.MustCompile(`\bfunction\s+[A-Za-z_]\w*\s*\(`)},
	{"class declaration", regexp.MustCompile(`\bclass\s+[A-Z]\w*`)},
	{"class block", regexp.MustCompile(`\bclass\s+\w+\s*[:({]`)},
	{"java entry point", regexp.MustCompile(`\bpublic\s+static\s+void\s+main\b`)},
	{"c entry point", regexp.MustCompile(`\bint\s+main\s*\(`)},
	{"c preprocessor", regexp.MustCompile(`#include\b`)},
}

// judgeInternalShapes catch a review talking about the judge's private
// data. A review is never given a hidden case, so anything matching here
// is either a hallucination presented as fact or an injection that
// worked; both are worth withholding.
var judgeInternalShapes = []codeSignature{
	{"hidden test disclosure", regexp.MustCompile(`(?i)\bhidden\s+(?:test|case|input|output)s?\b`)},
	{"test case index disclosure", regexp.MustCompile(`(?i)\btest\s+case\s+#?\d+\s+(?:is|was|contains|expects)`)},
}

// fencedBlock matches a markdown fence and its contents.
var fencedBlock = regexp.MustCompile("(?s)```[^\n]*\n(.*?)```|~~~[^\n]*\n(.*?)~~~")

// RejectReviewDump reports an error when a post-acceptance review has
// stopped reviewing and started rewriting.
//
// Text is normalised first with the same pass RejectCode uses, so an
// invisible character or a homoglyph cannot hide a definition.
func RejectReviewDump(text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	normalised := normaliseConfusables(text)

	// 1. A complete definition is a replacement solution at any length.
	for _, sig := range reviewDefinitionShapes {
		if sig.re.MatchString(normalised) {
			return fmt.Errorf("%w: review contained a %s", ErrFiltered, sig.name)
		}
	}

	// 2. The judge's private data is never the reviewer's to discuss.
	for _, sig := range judgeInternalShapes {
		if sig.re.MatchString(normalised) {
			return fmt.Errorf("%w: review referred to %s", ErrLeak, sig.name)
		}
	}

	// 3. Fenced snippets: bounded in size and in number.
	blocks := fencedBlock.FindAllStringSubmatch(normalised, -1)
	if len(blocks) > maxReviewSnippetBlocks {
		return fmt.Errorf("%w: review carried %d code blocks (limit %d)",
			ErrFiltered, len(blocks), maxReviewSnippetBlocks)
	}
	for _, b := range blocks {
		body := b[1]
		if body == "" {
			body = b[2]
		}
		if n := countNonBlankLines(body); n > maxReviewSnippetLines {
			return fmt.Errorf("%w: review carried a %d-line code block (limit %d)",
				ErrFiltered, n, maxReviewSnippetLines)
		}
	}

	// 4. Outside a fence, a review is prose and is held to exactly the
	//    standard a hint is. The fence is the sanctioned channel for a
	//    snippet; code smuggled in without one is a program pasted with
	//    the fences left off, and RejectCode already knows every shape
	//    that takes. Reusing it here means the two filters cannot drift
	//    apart about what counts as code.
	if err := RejectCode(fencedBlock.ReplaceAllString(normalised, "\n")); err != nil {
		return fmt.Errorf("%w (outside a code block)", err)
	}

	return nil
}

// countNonBlankLines counts the lines in a snippet that carry anything.
func countNonBlankLines(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}
