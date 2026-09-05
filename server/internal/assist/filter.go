package assist

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
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
// "Almost always" got a lot weaker when the deployment moved to
// open-weight models on a free tier. This file was written against a
// frontier model's compliance and is now the only thing standing
// between the assistant and a working solution, so it is tuned for the
// models that actually answer: ones that will emit a one-line solution,
// number the lines of a listing to look like an outline, or swap a
// Cyrillic 'е' into an identifier when told not to write code.
//
// The cost of a false positive is one withheld hint. The cost of a false
// negative is a judge whose scores mean nothing. The thresholds below
// are set accordingly, but not blindly: a rung-4 outline legitimately
// names variables and quotes identifiers, and a filter that rejects
// "compare `prices[i]` against it" makes the top of the ladder useless.
// Both directions are tested, and the false-positive corpus in
// filter_test.go is as much a part of the contract as the red-team one.

// ---------------------------------------------------------------------
// Normalisation
// ---------------------------------------------------------------------

// zeroWidth is stripped before anything is matched. A zero-width space
// inside `def` defeats every anchor and every keyword in this file while
// costing the student nothing: their editor pastes it back out as code.
// unicode.Cf covers most of these; the explicit list catches the few
// that other tables classify as spacing.
var zeroWidth = map[rune]bool{
	'\u200b': true, // zero width space
	'\u200c': true, // zero width non-joiner
	'\u200d': true, // zero width joiner
	'\u2060': true, // word joiner
	'\ufeff': true, // byte order mark
	'\u00ad': true, // soft hyphen
	'\u180e': true, // mongolian vowel separator
}

// homoglyphs folds the confusable letters that read as ASCII on screen
// but match no pattern here. It is deliberately partial: the Cyrillic
// and Greek letters that render identically to a Latin one in a
// monospace font are the ones a model actually reaches for, and folding
// a letter that merely looks similar would start corrupting prose.
//
// Fullwidth forms are handled arithmetically below rather than listed.
var homoglyphs = map[rune]rune{
	// Cyrillic lower
	'а': 'a', 'е': 'e', 'ё': 'e', 'о': 'o', 'р': 'p', 'с': 'c', 'х': 'x',
	'у': 'y', 'і': 'i', 'ј': 'j', 'ѕ': 's', 'ԁ': 'd', 'һ': 'h', 'м': 'm',
	'к': 'k', 'ѵ': 'v', 'ԛ': 'q', 'ԝ': 'w',
	// Cyrillic upper
	'А': 'A', 'В': 'B', 'Е': 'E', 'К': 'K', 'М': 'M', 'Н': 'H', 'О': 'O',
	'Р': 'P', 'С': 'C', 'Т': 'T', 'У': 'Y', 'Х': 'X', 'І': 'I', 'Ј': 'J',
	'Ѕ': 'S',
	// Greek
	'α': 'a', 'ε': 'e', 'ι': 'i', 'κ': 'k', 'ν': 'v', 'ο': 'o', 'ρ': 'p',
	'τ': 't', 'υ': 'u', 'χ': 'x',
	'Α': 'A', 'Β': 'B', 'Ε': 'E', 'Ζ': 'Z', 'Η': 'H', 'Ι': 'I', 'Κ': 'K',
	'Μ': 'M', 'Ν': 'N', 'Ο': 'O', 'Ρ': 'P', 'Τ': 'T', 'Υ': 'Y', 'Χ': 'X',
	// Punctuation that a model reaches for when told not to write code
	'‘': '\'', '’': '\'', '“': '"', '”': '"',
	'‐': '-', '‑': '-', '−': '-',
	'　': ' ',
}

// normaliseConfusables makes the text say what it looks like it says.
//
// It runs before every match in RejectCode and never leaves this file:
// the caller still sees, and still discards, the original string. What
// is normalised is only the copy the matcher reads, so a rejection can
// never be dodged by a change that a student's clipboard undoes.
func normaliseConfusables(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	for _, r := range s {
		switch {
		case zeroWidth[r]:
			// dropped
		case r != '\n' && r != '\r' && r != '\t' && unicode.Is(unicode.Cf, r):
			// other format characters, dropped
		case unicode.Is(unicode.Mn, r):
			// combining marks, dropped: they hide a keyword the same way
		case r >= 0xff01 && r <= 0xff5e:
			// fullwidth ASCII: （ ） ； ａ all fold to their ASCII twin
			b.WriteRune(r - 0xfee0)
		default:
			if folded, ok := homoglyphs[r]; ok {
				b.WriteRune(folded)
			} else {
				b.WriteRune(r)
			}
		}
	}

	return b.String()
}

// ---------------------------------------------------------------------
// Whole-text signatures
// ---------------------------------------------------------------------

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

// ---------------------------------------------------------------------
// Line shapes
// ---------------------------------------------------------------------

// listMarker is stripped from the front of a line before the line is
// tested. Numbering a listing — "1. best = 0" — used to defeat every
// anchor in statementShapes for the price of four characters, and it
// arrives disguised as exactly the numbered outline rung 4 is asked
// for. Stripping the marker costs nothing in the other direction: a
// numbered sentence is still a sentence once the number is gone.
var listMarker = regexp.MustCompile(`^[ \t]*(?:\d{1,3}[.):]|[A-Za-z][.)]|[-*+•]|>|#{1,6})[ \t]+`)

// wordToken is a run of identifier characters. Used to tell prose from
// code by adjacency rather than by vocabulary; see hasProseSignal.
var wordToken = regexp.MustCompile(`[A-Za-z_][A-Za-z_0-9]*`)

// codeKeywords are the words that legitimately sit next to a bare
// identifier in source with nothing but a space between them — "for x",
// "const solve", "return best". Everywhere else, two words separated by
// a space is the defining shape of prose.
//
// The list is control flow, declarations and types only. Words that are
// primarily English — "and", "or", "not", "is", "to", "of", "then" —
// are deliberately absent even though several are operators in some
// language, because excusing them would excuse sentences.
var codeKeywords = map[string]bool{
	"for": true, "foreach": true, "while": true, "do": true, "loop": true,
	"if": true, "elif": true, "elseif": true, "else": true, "switch": true,
	"case": true, "default": true, "break": true, "continue": true,
	"in": true, "return": true, "yield": true, "goto": true, "pass": true,
	"def": true, "func": true, "fun": true, "fn": true, "function": true,
	"lambda": true, "const": true, "let": true, "var": true, "val": true,
	"new": true, "delete": true, "class": true, "struct": true,
	"interface": true, "enum": true, "typedef": true, "template": true,
	"int": true, "long": true, "short": true, "float": true, "double": true,
	"char": true, "bool": true, "boolean": true, "string": true, "str": true,
	"void": true, "auto": true, "unsigned": true, "signed": true,
	"static": true, "public": true, "private": true, "protected": true,
	"final": true, "import": true, "from": true, "package": true,
	"using": true, "namespace": true, "try": true, "catch": true,
	"except": true, "finally": true, "with": true, "async": true,
	"await": true, "vector": true, "map": true, "set": true, "list": true,
	"dict": true, "print": true,
}

// hasProseSignal reports whether a line contains two ordinary words
// separated by nothing but a space.
//
// This is the discriminator the rest of the file leans on, and it is
// structural rather than lexical: code separates its operands with
// operators and delimiters, English separates its words with spaces. So
// "best = max(best, x)" has no adjacent word pair and "Track the
// smallest price" has several. Keyword pairs are excused because "for
// x" and "const solve" are code that happens to look like two words.
//
// It is used two ways: to veto a line that matched a prose-ambiguous
// statement shape, and to require that a suspected one-liner contains
// no English at all.
func hasProseSignal(line string) bool {
	spans := wordToken.FindAllStringIndex(line, -1)
	for i := 1; i < len(spans); i++ {
		gap := line[spans[i-1][1]:spans[i][0]]
		if gap == "" || strings.Trim(gap, " \t") != "" {
			continue // an operator or a delimiter sits between them
		}
		first := strings.ToLower(line[spans[i-1][0]:spans[i-1][1]])
		second := strings.ToLower(line[spans[i][0]:spans[i][1]])
		if codeKeywords[first] || codeKeywords[second] {
			continue
		}
		return true
	}
	return false
}

// statementShapes recognise a single line that reads as an executable
// statement rather than as a sentence. None of them is damning alone —
// prose contains "return the maximum" and "count = 0" — which is why the
// caller requires a run of them, and why every one of them is vetoed by
// hasProseSignal.
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

// pseudocodeShape is a line of block-capital pseudocode. A complete
// program in SET/FOR EACH/PRINT is still a complete program — it
// transliterates into any language in about two minutes, which is
// precisely the work the ladder exists to leave undone — and it matches
// none of the shapes above because it has no punctuation.
//
// It is deliberately not a whole-text signature: one shouted line is
// emphasis, several in a row is a listing, so it feeds the same run
// counter as everything else.
var pseudocodeShape = regexp.MustCompile(`^[ \t]*(?:SET|LET|PUT|FOR EACH|FOR|FOREACH|WHILE|REPEAT|UNTIL|IF|ELSE IF|ELSE|ENDIF|END IF|END|BEGIN|DO|THEN|PRINT|OUTPUT|DISPLAY|WRITE|INPUT|READ|RETURN|SWAP|ADD|SUBTRACT|INCREMENT|DECREMENT|INITIALISE|INITIALIZE|DECLARE|COMPUTE|CALCULATE|SORT|CALL|WHILE NOT)\b[ \t]+\S`)

// isPseudocodeLine additionally requires the line to be mostly capitals,
// so that a hint which opens a sentence with "IF the array is empty" is
// not mistaken for a program.
func isPseudocodeLine(line string) bool {
	if !pseudocodeShape.MatchString(line) {
		return false
	}
	var upper, lower int
	for _, r := range line {
		switch {
		case unicode.IsUpper(r):
			upper++
		case unicode.IsLower(r):
			lower++
		}
	}
	return upper*2 >= upper+lower
}

// callOperator is an application: an identifier of at least two
// characters immediately followed by a call or an index, an arrow
// function, a lambda, or a walrus.
//
// The two-character minimum is not arbitrary. It exempts `O(n log n)`,
// `T(n)` and `f(x)` — a hint is allowed to state a complexity target or
// name a recurrence, and those are the only single-letter applications
// that turn up in legitimate prose.
var callOperator = regexp.MustCompile(`[A-Za-z_]\w+\s*[(\[]|=>|->|:=|\blambda\b`)

// inlineBlock is a control statement with its body on the same line —
// "if x > best: best = x", "for x in a: total += x". It is the other
// half of the one-liner family: a whole loop or branch collapsed onto
// one line carries no application, so callOperator misses it.
//
// Lower-case and requiring a body after the colon, so that "If the sum
// overflows, stop early" and a bare "while lo < hi:" are both untouched
// — the latter is a fragment, and fragments are what the run counter is
// for.
var inlineBlock = regexp.MustCompile(`^\s*(?:if|elif|else|for|foreach|while|switch|match|do|try|except|catch)\b[^:{]*[:{]\s*\S`)

// sentenceEnd is a line that finishes like an English sentence.
var sentenceEnd = regexp.MustCompile(`[.!?][")'` + "`" + `]?\s*$`)

// isSolutionOneLiner reports whether a single line is, on its own, a
// runnable fragment: `print(max(a))`, `f = lambda a: max(a)`,
// `const solve = (a) => a.reduce(...)`.
//
// This is the hole that mattered most. For an easy problem one line is
// the entire solution, and a filter that only looks for runs of
// statements never sees it. It is also the most delicate check in the
// file, because "compare `prices[i]` against it" must survive, so the
// bar is that the line contains *no English at all*:
//
//   - no two ordinary words separated by only a space,
//   - no sentence-ending punctuation,
//   - balanced brackets, so a wrapped prose line cannot qualify,
//   - and at least one application or an inline block body, so a bare
//     "count = 0" — which is genuinely ambiguous, and which prose does
//     produce — still needs a neighbour before it is rejected.
func isSolutionOneLiner(line string) bool {
	s := strings.TrimSpace(stripListMarker(line))
	s = strings.Trim(s, "`")
	s = strings.TrimSpace(s)

	if utf8.RuneCountInString(s) < 5 {
		return false
	}
	if hasProseSignal(s) || sentenceEnd.MatchString(s) {
		return false
	}
	if !balancedDelimiters(s) {
		return false
	}
	return callOperator.MatchString(s) || inlineBlock.MatchString(s)
}

// balancedDelimiters reports whether every bracket in the line is
// closed within it. An unbalanced line is a fragment of something
// larger — usually a wrapped sentence — and the run counter is the
// right tool for those.
func balancedDelimiters(s string) bool {
	var round, square, curly int
	for _, r := range s {
		switch r {
		case '(':
			round++
		case ')':
			round--
		case '[':
			square++
		case ']':
			square--
		case '{':
			curly++
		case '}':
			curly--
		}
		if round < 0 || square < 0 || curly < 0 {
			return false
		}
	}
	return round == 0 && square == 0 && curly == 0
}

func stripListMarker(line string) string {
	return listMarker.ReplaceAllString(line, "")
}

// maxStatementRun is how many consecutive statement-shaped lines are
// tolerated.
//
// It was three, on the reasoning that three in a row is a code block
// with the fence taken off. Two is a code block too — "best = 0" then
// "for x in a:" is the opening of a solution and nothing else — and the
// shapes are now vetoed by hasProseSignal, which is what makes the
// tighter threshold affordable: the prose corpus in filter_test.go
// contains several two-line passages that no longer match a shape at
// all.
const maxStatementRun = 1

// RejectCode reports ErrFiltered when text contains something that reads
// as source in any language the judge supports.
//
// Prose is allowed to mention identifiers, quote them in backticks, and
// describe an algorithm step by step; that is what rung 4 is for. What
// is not allowed is a fenced block, a function or class definition, a
// recognisable entry point, a line that is a complete runnable
// expression, or a run of statement-shaped lines.
func RejectCode(text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}

	// 0. Say what it looks like it says. Zero-width characters and
	//    homoglyphs are stripped first so that every check below sees
	//    the code the student's clipboard would see.
	text = normaliseConfusables(text)

	// 1. Unambiguous signatures first — one match is enough.
	for _, sig := range codeSignatures {
		if sig.re.MatchString(text) {
			return fmt.Errorf("%w: %s", ErrFiltered, sig.name)
		}
	}

	// 2. Then the line shapes. A single line that is a complete runnable
	//    expression is enough on its own; anything weaker needs a run.
	//    Blank lines neither extend nor break a run, so double-spacing a
	//    listing does not sneak it past.
	run := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if isSolutionOneLiner(line) {
			return fmt.Errorf("%w: a line is a complete expression", ErrFiltered)
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
	// Strip a trailing carriage return so CRLF input behaves like LF,
	// and a list marker so that numbering a listing does not hide it.
	line = strings.TrimSuffix(line, "\r")
	line = stripListMarker(line)

	// Block capitals are their own evidence and are not vetoed by the
	// prose test, which would otherwise read "SET best TO 0" as four
	// English words.
	if isPseudocodeLine(line) {
		return true
	}

	for _, re := range statementShapes {
		if re.MatchString(line) {
			// Every shape above is prose-ambiguous on its own. A line
			// carrying two ordinary words in a row is a sentence that
			// happens to contain a semicolon or an equals sign, and
			// counting it would start rejecting outlines.
			return !hasProseSignal(line)
		}
	}
	return false
}

// ---------------------------------------------------------------------
// Leak detection
// ---------------------------------------------------------------------

// minLeakRunes is the shortest fragment of a hidden case worth treating
// as a bare substring match.
//
// Below this the fragments are single numbers and words — "5", "YES",
// "abc" — which appear in ordinary prose about the problem and would
// make rung 3 impossible to use. Eight characters of exact test data is
// no longer a coincidence.
//
// The floor stays where it is. What changed is that a *whole expected
// output* shorter than this is no longer ignored outright: see
// leaksShortAnswer.
const minLeakRunes = 8

// answerContexts match a short expected output that is being stated as
// the answer rather than merely mentioned.
//
// The problem this solves: for a yes/no or single-number problem the
// entire answer is "YES" or "-1", so the eight-rune floor let a model
// print the whole thing verbatim. Lowering the floor is the wrong fix —
// it would reject any sentence containing a small number, and rung 3's
// job is to talk about the case. So the fragment has to appear as a
// whole token *and* in a context that reads as disclosure: after a verb
// of output, after "the answer is", or in quotes.
//
// The line this draws is deliberate and slightly narrow. "Is the answer
// YES?" is a question put to the student and stays allowed; "the answer
// is YES" is a disclosure and does not. A model determined to phrase a
// disclosure some other way can still get one short token out, which is
// recorded as a known gap in filter_test.go.
var answerContexts = []string{
	`(?i)\b(?:prints?|printed|printing|outputs?|outputted|returns?|returned|emits?|emitted|produces?|produced|echoe?s?|shows?)\b[ \t]+(?:the[ \t]+)?(?:value[ \t]+|answer[ \t]+|output[ \t]+)?%s(?:$|[^\w-])`,
	`(?i)\b(?:answer|output|result|expectation)s?\b[ \t]+(?:is|was|are|were|should[ \t]+be|would[ \t]+be|must[ \t]+be|will[ \t]+be|has[ \t]+to[ \t]+be)[ \t]+%s(?:$|[^\w-])`,
	`(?i)["'` + "`" + `]%s["'` + "`" + `]`,
}

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
// "3  1   4" no longer hides "3 1 4", and confusable-normalised, which
// means a Cyrillic lookalike or a zero-width space wedged between two
// digits no longer hides the case either. That second pass was missing
// for a while: RejectCode had it and this did not, so the filter whose
// entire job is to notice one specific string could be walked past with
// a character nobody can see.
//
// Folding changes what "verbatim" means, so the false-positive
// direction is tested alongside it — rung 3 exists to describe a hidden
// case, and a leak filter that fires on any discussion of one makes the
// rung unusable.
func RejectLeak(text string, c HiddenCase) error {
	normalisedText := normaliseForLeak(text)
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
		if whole := normaliseForLeak(section.body); leaks(normalisedText, whole) {
			return fmt.Errorf("%w: the %s appears verbatim", ErrLeak, section.name)
		}

		// Individual lines, which is how a multi-line case usually
		// escapes: the model quotes one row of a grid.
		for _, line := range strings.Split(section.body, "\n") {
			if leaks(normalisedText, normaliseForLeak(line)) {
				return fmt.Errorf("%w: a line of the %s appears verbatim", ErrLeak, section.name)
			}
		}
	}

	// The whole answer, when the whole answer is too short for the
	// substring rule to touch it.
	if leaksShortAnswer(normalisedText, normaliseForLeak(c.ExpectedOutput)) {
		return fmt.Errorf("%w: the expected output is stated as the answer", ErrLeak)
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

// leaksShortAnswer reports whether a short expected output is being
// disclosed rather than mentioned. It only ever runs on the complete
// expected output — never on a line of the input, and never on a
// fragment — because "the answer is 3" is a disclosure only when 3 is
// the whole answer.
func leaksShortAnswer(normalisedText, answer string) bool {
	if answer == "" || utf8.RuneCountInString(answer) >= minLeakRunes {
		return false // the substring rule already covers it
	}

	quoted := regexp.QuoteMeta(answer)
	for _, pattern := range answerContexts {
		re, err := regexp.Compile(fmt.Sprintf(pattern, quoted))
		if err != nil {
			continue
		}
		if re.MatchString(normalisedText) {
			return true
		}
	}
	return false
}

// normaliseWhitespace collapses every run of whitespace — newlines
// included — to a single space and trims the ends, so that comparisons
// ignore the formatting a model applies when it reflows a test case into
// a sentence.
func normaliseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// normaliseForLeak is the comparison form both sides of a leak check are
// put into: confusables folded to ASCII and zero-width characters
// dropped, then whitespace collapsed.
//
// The order matters. Folding first turns a zero-width space into
// nothing rather than into a word boundary, so "9<ZWSP>9" compares equal
// to "99" instead of to "9 9".
func normaliseForLeak(s string) string {
	return normaliseWhitespace(normaliseConfusables(s))
}
