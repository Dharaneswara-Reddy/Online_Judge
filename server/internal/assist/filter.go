package assist

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// This file is the part of the package that has to hold when everything
// else fails.
//
// The system prompts ask the model for prose. That is a request, and a
// request is not a control. Models comply with "no code" almost always,
// and "almost always" across a term's worth of submissions is a
// guarantee that the assistant will eventually hand somebody a working
// solution to a problem they are being graded on. So every generated
// string passes through here before any caller sees it, and a string
// that trips a check is thrown away rather than trimmed — a filter that
// edits its input is a filter that can be talked into editing badly.
//
// The cost of a false positive is one withheld hint. The cost of a false
// negative is a judge whose scores mean nothing. The thresholds below
// are set accordingly, but not blindly: a rung-4 outline legitimately
// names variables and quotes identifiers, and a filter that rejects
// "compare `prices[i]` against it" makes the top of the ladder useless.
// Both directions are tested.

// codeSignature is a shape that only appears in source. Each carries a
// name so a rejection can be explained without quoting the offending
// text back into a log.
type codeSignature struct {
	name string
	re   *regexp.Regexp
}

var codeSignatures = []codeSignature{
	// A fenced block is the model's own admission that what follows is
	// code, in any language, and it is by far the most common shape.
	{"markdown fence", regexp.MustCompile("(?m)^[ \t]*(?:```|~~~)")},

	{"python function definition", regexp.MustCompile(`\bdef\s+[A-Za-z_]\w*\s*\(`)},
	{"go function definition", regexp.MustCompile(`\bfunc\s+[A-Za-z_]\w*\s*\(`)},

	// Two separate class patterns rather than one loose `class \w+`,
	// because rung 2's entire job is to "name the class of approach" and
	// that phrase must survive. A declaration either capitalises its type
	// name or opens a block immediately; "class of approach" does neither.
	{"class declaration", regexp.MustCompile(`\bclass\s+[A-Z]\w*`)},
	{"class block", regexp.MustCompile(`\bclass\s+\w+\s*[:({]`)},

	{"java entry point", regexp.MustCompile(`\bpublic\s+static\s+void\s+main\b`)},
	{"c preprocessor", regexp.MustCompile(`#include\b`)},
	{"c entry point", regexp.MustCompile(`\bint\s+main\s*\(`)},
}

// statementShapes recognise a single line that reads as an executable
// statement rather than as a sentence. None of them is damning alone —
// prose contains "return the maximum" and "count = 0" — which is why the
// caller requires a run of them.
var statementShapes = []*regexp.Regexp{
	// An assignment, optionally preceded by a type or declaration
	// keyword ("int lo = 0", "let x = 1", "x += 1", "ch <- v").
	//
	// Comparisons are excluded by requiring the character after "=" not
	// to be another "=". RE2 has no lookahead, so it is spelled out.
	regexp.MustCompile(`^\s*(?:[A-Za-z_][\w\[\].*&<>]*\s+)?[A-Za-z_][\w\[\].]*\s*(?:[-+*/%|&^]|:|<<|>>)?=(?:[^=]|$)`),
	regexp.MustCompile(`^\s*[A-Za-z_][\w\[\].]*\s*<-\s*\S`),

	// A control keyword that opens a block — the brace or the colon at
	// the end of the line is what separates "if the sum overflows, stop"
	// from "if (sum > cap) {".
	regexp.MustCompile(`^\s*(?:for|foreach|while|if|elif|else|switch|match)\b.*[:{]\s*$`),

	// A bare return. Case-sensitive on purpose: prose starts sentences
	// with "Return", code does not.
	regexp.MustCompile(`^\s*return\b`),

	// Statement punctuation: a line that ends in a semicolon or an
	// opening brace, or that is nothing but closing delimiters.
	regexp.MustCompile(`[;{]\s*$`),
	regexp.MustCompile(`^\s*[}\])]+[;,]?\s*$`),
}

// maxStatementRun is how many consecutive statement-shaped lines are
// tolerated. Three in a row is a code block with the fence taken off.
const maxStatementRun = 2

// RejectCode reports ErrFiltered when text contains something that reads
// as source in any language the judge supports.
//
// Prose is allowed to mention identifiers, quote them in backticks, and
// describe an algorithm step by step; that is what rung 4 is for. What
// is not allowed is a fenced block, a function or class definition, a
// recognisable entry point, or a run of statement-shaped lines.
func RejectCode(text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}

	// 1. Unambiguous signatures first — one match is enough.
	for _, sig := range codeSignatures {
		if sig.re.MatchString(text) {
			return fmt.Errorf("%w: %s", ErrFiltered, sig.name)
		}
	}

	// 2. Then the statistical shape: a run of lines that each read as a
	//    statement. Blank lines neither extend nor break a run, so
	//    double-spacing a listing does not sneak it past.
	run := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !isStatementLine(line) {
			run = 0
			continue
		}
		run++
		if run > maxStatementRun {
			return fmt.Errorf("%w: %d consecutive statement-shaped lines", ErrFiltered, run)
		}
	}

	return nil
}

// isStatementLine reports whether one line reads as executable code.
func isStatementLine(line string) bool {
	// Strip a trailing carriage return so CRLF input behaves like LF.
	line = strings.TrimSuffix(line, "\r")
	for _, re := range statementShapes {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

// minLeakRunes is the shortest fragment of a hidden case worth treating
// as a disclosure.
//
// Below this the fragments are single numbers and words — "5", "YES",
// "abc" — which appear in ordinary prose about the problem and would
// make rung 3 impossible to use. Eight characters of exact test data is
// no longer a coincidence.
const minLeakRunes = 8

// RejectLeak reports ErrLeak when text echoes the hidden case instead of
// describing it.
//
// Two comparisons, because a model that has been told not to print the
// case will happily reformat it. Every trimmed line of the input and the
// expected output is checked, and so is each whole section with runs of
// whitespace collapsed, so a case spread across lines is caught after it
// has been flowed into a sentence.
//
// Both sides of every comparison are whitespace-normalised, which means
// "3  1   4" no longer hides "3 1 4".
func RejectLeak(text string, c HiddenCase) error {
	normalisedText := normaliseWhitespace(text)
	if normalisedText == "" {
		return nil
	}

	sections := []struct {
		name string
		body string
	}{
		{"input", c.Input},
		{"expected output", c.ExpectedOutput},
	}

	for _, section := range sections {
		if strings.TrimSpace(section.body) == "" {
			continue
		}

		// The whole section, flowed onto one line.
		if whole := normaliseWhitespace(section.body); leaks(normalisedText, whole) {
			return fmt.Errorf("%w: the %s appears verbatim", ErrLeak, section.name)
		}

		// Individual lines, which is how a multi-line case usually
		// escapes: the model quotes one row of a grid.
		for _, line := range strings.Split(section.body, "\n") {
			if leaks(normalisedText, normaliseWhitespace(line)) {
				return fmt.Errorf("%w: a line of the %s appears verbatim", ErrLeak, section.name)
			}
		}
	}

	return nil
}

// leaks reports whether fragment is long enough to matter and is present
// in the already-normalised haystack.
func leaks(normalisedText, fragment string) bool {
	if utf8.RuneCountInString(fragment) < minLeakRunes {
		return false
	}
	return strings.Contains(normalisedText, fragment)
}

// normaliseWhitespace collapses every run of whitespace — newlines
// included — to a single space and trims the ends, so that comparisons
// ignore the formatting a model applies when it reflows a test case into
// a sentence.
func normaliseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
