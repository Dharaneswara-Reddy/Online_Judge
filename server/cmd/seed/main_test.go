package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/toji339/online-judge/internal/judge"
	"github.com/toji339/online-judge/internal/problem"
)

// The seed data is the judge's ground truth: it decides whether a correct
// submission is accepted. Eyeballing it is what let an ambiguous Two Sum
// case ship, so every seeded case is checked mechanically here instead —
// each problem gets a reference solution, and the reference output must
// equal the stored expected output exactly under the judge's own
// comparison.

// referenceSolver produces the output a correct program would print for
// one test case input.
type referenceSolver func(input string) (string, error)

// solvers maps a seeded problem title to its reference solution. Every
// seeded problem must have one — a problem without a solver fails the
// suite rather than being skipped, so new seed data cannot slip in
// unverified.
func solvers() map[string]referenceSolver {
	return map[string]referenceSolver{
		"Two Sum":                         solveTwoSum,
		"Reverse String":                  solveReverseString,
		"Valid Parentheses":               solveValidParentheses,
		"Longest Common Subsequence":      solveLCS,
		"Merge Intervals":                 solveMergeIntervals,
		"Best Time to Buy and Sell Stock": solveBestTimeToBuySell,
		"Maximum Subarray":                solveMaximumSubarray,
		"Binary Search":                   solveBinarySearch,
		"Climbing Stairs":                 solveClimbingStairs,
		"Longest Substring Without Repeating Characters": solveLongestUniqueSubstring,
	}
}

func TestSeedTestCasesMatchAReferenceSolution(t *testing.T) {
	solve := solvers()

	for _, sp := range seedProblems() {
		title := sp.Input.Title
		solver, ok := solve[title]
		if !ok {
			t.Errorf("%s: no reference solver — seed data cannot be verified", title)
			continue
		}
		if len(sp.TestCases) == 0 {
			t.Errorf("%s: has no test cases", title)
		}
		for i, tc := range sp.TestCases {
			got, err := solver(tc.Input)
			if err != nil {
				t.Errorf("%s case %d: reference solver failed on input %q: %v", title, i+1, tc.Input, err)
				continue
			}
			if !judge.OutputsMatch(tc.ExpectedOutput, got) {
				t.Errorf("%s case %d: input %q\n  expected output: %q\n  reference says:  %q",
					title, i+1, tc.Input, tc.ExpectedOutput, got)
			}
		}
	}
}

// TestSeedTestCasesHaveReadableInput guards the scaffolding the starter
// code hands users: every starter reads a line from stdin, and an empty
// input makes Python's input() raise EOFError, so an empty-input case
// fails every solution written against the starter template.
func TestSeedTestCasesHaveReadableInput(t *testing.T) {
	for _, sp := range seedProblems() {
		for i, tc := range sp.TestCases {
			if strings.TrimSpace(tc.Input) == "" {
				t.Errorf("%s case %d: input is empty — the starter scaffolds cannot read it", sp.Input.Title, i+1)
			}
			if strings.TrimSpace(tc.ExpectedOutput) == "" {
				t.Errorf("%s case %d: expected output is empty (AddTestCase rejects it)", sp.Input.Title, i+1)
			}
		}
	}
}

// TestSeedProblemsHaveSampleAndHiddenCases keeps every problem showing
// the user something while still holding cases back.
func TestSeedProblemsHaveSampleAndHiddenCases(t *testing.T) {
	for _, sp := range seedProblems() {
		samples, hidden := 0, 0
		for _, tc := range sp.TestCases {
			if tc.IsSample {
				samples++
			} else {
				hidden++
			}
		}
		if samples == 0 {
			t.Errorf("%s: no sample test case", sp.Input.Title)
		}
		if hidden == 0 {
			t.Errorf("%s: no hidden test case", sp.Input.Title)
		}
	}
}

// TestTwoSumInputsHaveExactlyOneAnswer is the check that would have
// caught the original defect. The statement promises "exactly one
// solution" and judging is an exact string match, so an input with two
// valid index pairs rejects whichever correct solution the user wrote.
func TestTwoSumInputsHaveExactlyOneAnswer(t *testing.T) {
	sp := seedProblemByTitle(t, "Two Sum")

	for i, tc := range sp.TestCases {
		nums, target, err := parseTwoSum(tc.Input)
		if err != nil {
			t.Fatalf("case %d: %v", i+1, err)
		}
		answers := allTwoSumAnswers(nums, target)
		if len(answers) != 1 {
			t.Errorf("case %d: input %q has %d valid answers %v — the statement promises exactly one",
				i+1, tc.Input, len(answers), answers)
		}
	}
}

// TestTwoSumAcceptsTheHashMapSolution pins the specific failure users
// hit: the problem is tagged hash-table and the C++ starter includes
// <unordered_map>, so the canonical single-pass hash map must pass every
// case, not just brute force.
func TestTwoSumAcceptsTheHashMapSolution(t *testing.T) {
	sp := seedProblemByTitle(t, "Two Sum")

	for i, tc := range sp.TestCases {
		nums, target, err := parseTwoSum(tc.Input)
		if err != nil {
			t.Fatalf("case %d: %v", i+1, err)
		}
		got, ok := twoSumHashMap(nums, target)
		if !ok {
			t.Errorf("case %d: hash-map solution found no answer for %q", i+1, tc.Input)
			continue
		}
		if !judge.OutputsMatch(tc.ExpectedOutput, got) {
			t.Errorf("case %d: input %q — hash-map solution prints %q, expected %q",
				i+1, tc.Input, got, tc.ExpectedOutput)
		}
	}
}

// TestTwoSumStatementDoesNotPromiseAnyOrder keeps the statement honest.
// Judging is an exact string match, so "return the answer in any order"
// is a promise the judge does not keep.
func TestTwoSumStatementDoesNotPromiseAnyOrder(t *testing.T) {
	for _, sp := range seedProblems() {
		if strings.Contains(strings.ToLower(sp.Input.Statement), "any order") {
			t.Errorf("%s: the statement promises 'any order' but judging is an exact string match",
				sp.Input.Title)
		}
	}
}

// TestMergeIntervalsRequiresSorting proves the data exercises the
// stated requirement. Every seeded input was pre-sorted by start, so a
// solution that never sorts was accepted; at least one case must reject
// it.
func TestMergeIntervalsRequiresSorting(t *testing.T) {
	sp := seedProblemByTitle(t, "Merge Intervals")

	unsortedInputs := 0
	caughtByData := false
	for i, tc := range sp.TestCases {
		intervals, err := parseIntervals(tc.Input)
		if err != nil {
			t.Fatalf("case %d: %v", i+1, err)
		}
		if !isSortedByStart(intervals) {
			unsortedInputs++
		}
		if !judge.OutputsMatch(tc.ExpectedOutput, mergeWithoutSorting(intervals)) {
			caughtByData = true
		}
	}

	if unsortedInputs == 0 {
		t.Error("every seeded input is pre-sorted by start, so the sorting step is never exercised")
	}
	if !caughtByData {
		t.Error("a solution that never sorts passes every seeded case")
	}
}

func seedProblemByTitle(t *testing.T, title string) seedProblem {
	t.Helper()
	for _, sp := range seedProblems() {
		if sp.Input.Title == title {
			return sp
		}
	}
	t.Fatalf("seed problem %q not found", title)
	return seedProblem{}
}

// ——— reference solutions ———

func parseTwoSum(input string) ([]int, int, error) {
	lines := strings.Split(strings.TrimRight(input, "\n"), "\n")
	if len(lines) != 2 {
		return nil, 0, fmt.Errorf("expected 2 lines, got %d", len(lines))
	}
	header := strings.Fields(lines[0])
	if len(header) != 2 {
		return nil, 0, fmt.Errorf("expected 'n target' header, got %q", lines[0])
	}
	n, err := strconv.Atoi(header[0])
	if err != nil {
		return nil, 0, err
	}
	target, err := strconv.Atoi(header[1])
	if err != nil {
		return nil, 0, err
	}
	nums, err := parseInts(strings.Fields(lines[1]))
	if err != nil {
		return nil, 0, err
	}
	if len(nums) != n {
		return nil, 0, fmt.Errorf("header says n=%d but %d numbers follow", n, len(nums))
	}
	return nums, target, nil
}

// allTwoSumAnswers returns every distinct index pair that sums to the
// target, as the judge would see them printed.
func allTwoSumAnswers(nums []int, target int) []string {
	answers := []string{}
	for i := 0; i < len(nums); i++ {
		for j := i + 1; j < len(nums); j++ {
			if nums[i]+nums[j] == target {
				answers = append(answers, fmt.Sprintf("%d %d", i, j))
			}
		}
	}
	return answers
}

// twoSumHashMap is the canonical single-pass solution: scan left to
// right, remembering values already seen.
func twoSumHashMap(nums []int, target int) (string, bool) {
	seen := make(map[int]int, len(nums))
	for i, v := range nums {
		if j, ok := seen[target-v]; ok {
			return fmt.Sprintf("%d %d", j, i), true
		}
		seen[v] = i
	}
	return "", false
}

func solveTwoSum(input string) (string, error) {
	nums, target, err := parseTwoSum(input)
	if err != nil {
		return "", err
	}
	answers := allTwoSumAnswers(nums, target)
	if len(answers) == 0 {
		return "", fmt.Errorf("no pair sums to %d", target)
	}
	return answers[0], nil
}

func solveReverseString(input string) (string, error) {
	runes := []rune(strings.TrimRight(input, "\n"))
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes), nil
}

func solveValidParentheses(input string) (string, error) {
	s := strings.TrimSpace(input)
	pairs := map[rune]rune{')': '(', ']': '[', '}': '{'}
	stack := []rune{}
	for _, ch := range s {
		switch ch {
		case '(', '[', '{':
			stack = append(stack, ch)
		case ')', ']', '}':
			if len(stack) == 0 || stack[len(stack)-1] != pairs[ch] {
				return "false", nil
			}
			stack = stack[:len(stack)-1]
		default:
			return "", fmt.Errorf("unexpected character %q", ch)
		}
	}
	if len(stack) == 0 {
		return "true", nil
	}
	return "false", nil
}

func solveLCS(input string) (string, error) {
	lines := strings.Split(strings.TrimRight(input, "\n"), "\n")
	if len(lines) != 2 {
		return "", fmt.Errorf("expected 2 lines, got %d", len(lines))
	}
	a, b := []rune(lines[0]), []rune(lines[1])
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	return strconv.Itoa(dp[len(a)][len(b)]), nil
}

type interval struct{ start, end int }

func parseIntervals(input string) ([]interval, error) {
	lines := strings.Split(strings.TrimRight(input, "\n"), "\n")
	if len(lines) == 0 {
		return nil, fmt.Errorf("empty input")
	}
	n, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil {
		return nil, err
	}
	if len(lines)-1 != n {
		return nil, fmt.Errorf("header says n=%d but %d interval lines follow", n, len(lines)-1)
	}
	intervals := make([]interval, 0, n)
	for _, line := range lines[1:] {
		values, err := parseInts(strings.Fields(line))
		if err != nil {
			return nil, err
		}
		if len(values) != 2 {
			return nil, fmt.Errorf("expected 'start end', got %q", line)
		}
		intervals = append(intervals, interval{values[0], values[1]})
	}
	return intervals, nil
}

func solveMergeIntervals(input string) (string, error) {
	intervals, err := parseIntervals(input)
	if err != nil {
		return "", err
	}
	sorted := append([]interval(nil), intervals...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].start == sorted[j].start {
			return sorted[i].end < sorted[j].end
		}
		return sorted[i].start < sorted[j].start
	})
	return renderMerged(mergeInOrder(sorted)), nil
}

// mergeWithoutSorting is the buggy solution the data has to reject: it
// walks the intervals in input order and never sorts them.
func mergeWithoutSorting(intervals []interval) string {
	return renderMerged(mergeInOrder(intervals))
}

func mergeInOrder(intervals []interval) []interval {
	merged := []interval{}
	for _, iv := range intervals {
		if len(merged) > 0 && iv.start <= merged[len(merged)-1].end {
			if iv.end > merged[len(merged)-1].end {
				merged[len(merged)-1].end = iv.end
			}
			continue
		}
		merged = append(merged, iv)
	}
	return merged
}

func renderMerged(merged []interval) string {
	lines := make([]string, 0, len(merged))
	for _, iv := range merged {
		lines = append(lines, fmt.Sprintf("%d %d", iv.start, iv.end))
	}
	return strings.Join(lines, "\n")
}

func isSortedByStart(intervals []interval) bool {
	for i := 1; i < len(intervals); i++ {
		if intervals[i].start < intervals[i-1].start {
			return false
		}
	}
	return true
}

func parseInts(fields []string) ([]int, error) {
	values := make([]int, 0, len(fields))
	for _, f := range fields {
		v, err := strconv.Atoi(f)
		if err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, nil
}

// compile-time guard: the seed data must keep the shape the service
// validates, so a typo in a difficulty constant fails here rather than
// at run time against the database.
var _ = problem.DifficultyEasy

// =====================================================================
// Reference solutions for the problems added in the V2 expansion.
//
// These are deliberately written straight, not cleverly: the point is to
// be obviously correct so that when a stored expected output disagrees
// with one, the seed data is what gets doubted.
// =====================================================================

// solveBestTimeToBuySell walks the prices once, tracking the cheapest day
// seen so far. Buying must precede selling, which is what makes pairing
// the global minimum with the global maximum wrong.
func solveBestTimeToBuySell(input string) (string, error) {
	lines := strings.Split(strings.TrimSpace(input), "\n")
	if len(lines) < 2 {
		return "", fmt.Errorf("expected two lines, got %d", len(lines))
	}
	prices, err := parseInts(strings.Fields(lines[1]))
	if err != nil {
		return "", err
	}
	if len(prices) == 0 {
		return "0", nil
	}
	lowest, best := prices[0], 0
	for _, price := range prices[1:] {
		if profit := price - lowest; profit > best {
			best = profit
		}
		if price < lowest {
			lowest = price
		}
	}
	return strconv.Itoa(best), nil
}

// solveMaximumSubarray is Kadane's. The running sum starts at the first
// element rather than zero, so an all-negative array returns its least
// negative element instead of an empty-subarray zero.
func solveMaximumSubarray(input string) (string, error) {
	lines := strings.Split(strings.TrimSpace(input), "\n")
	if len(lines) < 2 {
		return "", fmt.Errorf("expected two lines, got %d", len(lines))
	}
	nums, err := parseInts(strings.Fields(lines[1]))
	if err != nil {
		return "", err
	}
	if len(nums) == 0 {
		return "", fmt.Errorf("empty array")
	}
	best, running := nums[0], nums[0]
	for _, n := range nums[1:] {
		if running < 0 {
			running = n
		} else {
			running += n
		}
		if running > best {
			best = running
		}
	}
	return strconv.Itoa(best), nil
}

// solveBinarySearch returns the index of target, or -1.
func solveBinarySearch(input string) (string, error) {
	lines := strings.Split(strings.TrimSpace(input), "\n")
	if len(lines) < 2 {
		return "", fmt.Errorf("expected two lines, got %d", len(lines))
	}
	header, err := parseInts(strings.Fields(lines[0]))
	if err != nil || len(header) < 2 {
		return "", fmt.Errorf("bad header %q", lines[0])
	}
	target := header[1]
	nums, err := parseInts(strings.Fields(lines[1]))
	if err != nil {
		return "", err
	}
	lo, hi := 0, len(nums)-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		switch {
		case nums[mid] == target:
			return strconv.Itoa(mid), nil
		case nums[mid] < target:
			lo = mid + 1
		default:
			hi = mid - 1
		}
	}
	return "-1", nil
}

// solveClimbingStairs is the Fibonacci recurrence, iterated. n is bounded
// at 40 by the statement, which fits an int comfortably.
func solveClimbingStairs(input string) (string, error) {
	n, err := strconv.Atoi(strings.TrimSpace(input))
	if err != nil {
		return "", err
	}
	if n <= 2 {
		return strconv.Itoa(n), nil
	}
	prev, curr := 1, 2
	for i := 3; i <= n; i++ {
		prev, curr = curr, prev+curr
	}
	return strconv.Itoa(curr), nil
}

// solveLongestUniqueSubstring is the sliding window. The left edge jumps
// past a repeat rather than stepping one place, which is what "dvdf"
// distinguishes.
func solveLongestUniqueSubstring(input string) (string, error) {
	s := strings.TrimRight(input, "\r\n")
	lastSeen := make(map[rune]int)
	best, left := 0, 0
	for i, ch := range []rune(s) {
		if prev, seen := lastSeen[ch]; seen && prev >= left {
			left = prev + 1
		}
		lastSeen[ch] = i
		if width := i - left + 1; width > best {
			best = width
		}
	}
	return strconv.Itoa(best), nil
}
