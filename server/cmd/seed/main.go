// Package main provides a seed script that populates the database with
// 5 classic competitive programming problems and their test cases.
// It also promotes a specified user to admin role.
//
// Usage: go run cmd/seed/main.go [--admin-email user@example.com]
//
//	[--force-testcases]
//
// The seeder never overwrites stored test data unless --force-testcases
// is passed. Without it the run is read-only for existing problems: it
// reports which ones hold test cases that differ from the seed set and
// leaves them alone. That flag exists because test data was otherwise
// write-once — a wrong expected output could not be corrected in a
// running deployment through any code path.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/joho/godotenv"
	"github.com/toji339/online-judge/internal/config"
	"github.com/toji339/online-judge/internal/database"
	"github.com/toji339/online-judge/internal/problem"
	"github.com/toji339/online-judge/internal/problem/mongorepo"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func main() {
	adminEmail := flag.String("admin-email", "", "Email of the user to promote to admin")
	// Off by default and never implied by anything else: it deletes stored
	// test cases and writes the seed set in their place.
	forceTestCases := flag.Bool("force-testcases", false,
		"Replace the stored test cases of seeded problems whose data differs from the seed set (destructive)")
	flag.Parse()

	_ = godotenv.Load()
	cfg := config.Load()

	client, err := database.Connect(cfg.MongoURI)
	if err != nil {
		log.Fatalf("FATAL: %v", err)
	}
	defer database.Disconnect(client)

	db := client.Database(cfg.DBName)

	// Ensure indexes exist
	if err := database.EnsureIndexes(db); err != nil {
		log.Fatalf("FATAL: %v", err)
	}

	repo := mongorepo.New(db)
	svc := problem.NewService(repo)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Promote a user to admin if requested
	if *adminEmail != "" {
		promoteAdmin(ctx, db, *adminEmail)
	}

	// Seed problems
	problems := seedProblems()
	plans := make([]testCasePlan, 0, len(problems))
	for _, sp := range problems {
		existing, err := svc.GetBySlug(ctx, problem.Slugify(sp.Input.Title))
		if err == nil {
			log.Printf("SKIP: %q already exists (id=%s)", sp.Input.Title, existing.ID)
			plans = append(plans, seedTestCases(ctx, svc, existing.ID, sp, *forceTestCases))
			continue
		}

		p, err := svc.Create(ctx, sp.Input)
		if err != nil {
			log.Printf("ERROR creating %q: %v", sp.Input.Title, err)
			continue
		}
		log.Printf("CREATED: %q (slug=%s, id=%s)", p.Title, p.Slug, p.ID)

		plans = append(plans, seedTestCases(ctx, svc, p.ID, sp, *forceTestCases))
	}

	printSummary(plans, *forceTestCases)
	log.Println("Seed completed successfully!")
}

// printSummary reports what the run did to test data, and what it left
// untouched. A drifted problem is named explicitly, with the one command
// that would fix it — the previous silent "SKIP" is how the ambiguous
// Two Sum case survived every re-seed.
func printSummary(plans []testCasePlan, forced bool) {
	log.Println("——— test case summary ———")

	needsForce := []string{}
	for _, plan := range plans {
		switch plan.Action {
		case actionInsert:
			log.Printf("  %-28s inserted %d test case(s)", plan.Title, plan.Desired)
		case actionUpToDate:
			log.Printf("  %-28s unchanged (%d test case(s) already match)", plan.Title, plan.Existing)
		case actionReplace:
			log.Printf("  %-28s REPLACED %d stored test case(s) with %d from the seed set",
				plan.Title, plan.Existing, plan.Desired)
		case actionNeedsForce:
			log.Printf("  %-28s stored %d test case(s) DIFFER from the seed set's %d — left untouched",
				plan.Title, plan.Existing, plan.Desired)
			needsForce = append(needsForce, plan.Title)
		case actionFailed:
			log.Printf("  %-28s FAILED: %s", plan.Title, plan.Err)
		}
	}

	if len(needsForce) == 0 {
		return
	}
	sort.Strings(needsForce)
	log.Printf("%d problem(s) hold test data that differs from this seed set: %v",
		len(needsForce), needsForce)
	if !forced {
		log.Println("Nothing was overwritten. Re-run with --force-testcases to replace their test cases with the seed set.")
	}
}

func promoteAdmin(ctx context.Context, db *mongo.Database, email string) {
	result, err := db.Collection("users").UpdateOne(
		ctx,
		bson.M{"email": email},
		bson.M{"$set": bson.M{"role": "admin"}},
	)
	if err != nil {
		log.Printf("ERROR promoting admin: %v", err)
		return
	}
	if result.MatchedCount == 0 {
		log.Printf("WARNING: No user found with email %q", email)
		return
	}
	log.Printf("ADMIN: Promoted %q to admin role", email)
}

// What a seeding run decided to do with one problem's test cases.
const (
	actionInsert     = "insert"     // the problem had none
	actionUpToDate   = "up-to-date" // stored data already matches the seed set
	actionNeedsForce = "needs-force"
	actionReplace    = "replace"
	actionFailed     = "failed"
)

// testCasePlan is what a run decided for one problem, and what it found.
type testCasePlan struct {
	Title    string
	Existing int
	Desired  int
	// Differs reports that the stored set is not the seed set — a
	// different count, a different input, a changed expected output, or a
	// case that flipped between sample and hidden.
	Differs bool
	Action  string
	Err     string
}

// planTestCases decides what to do with one problem's stored test cases.
//
// It is deliberately separate from the database work: the rule that
// matters is that force is the only path to a destructive action, and
// that rule is worth testing without a database in front of it.
func planTestCases(title string, existing, desired []problem.TestCase, force bool) testCasePlan {
	plan := testCasePlan{Title: title, Existing: len(existing), Desired: len(desired)}

	if len(existing) == 0 {
		plan.Action = actionInsert
		return plan
	}

	plan.Differs = !sameTestCases(existing, desired)
	switch {
	case !plan.Differs:
		plan.Action = actionUpToDate
	case force:
		plan.Action = actionReplace
	default:
		plan.Action = actionNeedsForce
	}
	return plan
}

// sameTestCases compares two sets by content, ignoring order and stored
// identifiers. ListTestCases makes no ordering promise, so comparing
// position by position would report spurious differences.
func sameTestCases(a, b []problem.TestCase) bool {
	if len(a) != len(b) {
		return false
	}
	key := func(tc problem.TestCase) string {
		return fmt.Sprintf("%q|%q|%t", tc.Input, tc.ExpectedOutput, tc.IsSample)
	}
	counts := make(map[string]int, len(a))
	for _, tc := range a {
		counts[key(tc)]++
	}
	for _, tc := range b {
		k := key(tc)
		counts[k]--
		if counts[k] < 0 {
			return false
		}
	}
	return true
}

// seedTestCases brings one problem's test cases in line with the seed
// set and reports what it did. It only ever deletes when force is true.
func seedTestCases(ctx context.Context, svc *problem.Service, problemID string, sp seedProblem, force bool) testCasePlan {
	existing, err := svc.ListAllTestCases(ctx, problemID)
	if err != nil {
		log.Printf("  ERROR reading existing test cases: %v", err)
		return testCasePlan{Title: sp.Input.Title, Action: actionFailed, Err: err.Error()}
	}

	plan := planTestCases(sp.Input.Title, existing, sp.TestCases, force)

	switch plan.Action {
	case actionInsert:
		for i, tc := range sp.TestCases {
			tc.ProblemID = problemID
			if err := svc.AddTestCase(ctx, &tc); err != nil {
				log.Printf("  ERROR adding test case %d: %v", i+1, err)
				plan.Action = actionFailed
				plan.Err = err.Error()
				return plan
			}
			label := "hidden"
			if tc.IsSample {
				label = "sample"
			}
			log.Printf("  ADDED test case %d (%s)", i+1, label)
		}

	case actionUpToDate:
		log.Printf("  SKIP test cases: %d already match the seed set", plan.Existing)

	case actionNeedsForce:
		log.Printf("  SKIP test cases: %d stored differ from the seed set's %d "+
			"(re-run with --force-testcases to replace them)", plan.Existing, plan.Desired)

	case actionReplace:
		log.Printf("  REPLACING %d stored test case(s) with %d from the seed set",
			plan.Existing, plan.Desired)
		if err := svc.ReplaceTestCases(ctx, problemID, sp.TestCases); err != nil {
			log.Printf("  ERROR replacing test cases: %v", err)
			plan.Action = actionFailed
			plan.Err = err.Error()
		}
	}
	return plan
}

type seedProblem struct {
	Input     problem.CreateProblemInput
	TestCases []problem.TestCase
}

func seedProblems() []seedProblem {
	return []seedProblem{
		// ——— 1. Two Sum ———
		{
			Input: problem.CreateProblemInput{
				Title: "Two Sum",
				Statement: `Given an array of integers nums and an integer target, return the indices of the two numbers such that they add up to target.

You may assume that each input would have exactly one solution, and you may not use the same element twice.

Print the two indices in increasing order, smaller index first. Judging is an exact match on your output, so the order is part of the answer.

Input Format:
First line: n (size of array) and target separated by space.
Second line: n space-separated integers.

Output Format:
Two space-separated indices (0-indexed), smaller index first.`,
				Difficulty:    problem.DifficultyEasy,
				Tags:          []string{"arrays", "hash-table"},
				TimeLimitMS:   2000,
				MemoryLimitMB: 256,
				StarterCode: map[string]string{
					"python": "import sys\n\ndef two_sum(nums, target):\n    # Your code here\n    pass\n\nline1 = input().split()\nn, target = int(line1[0]), int(line1[1])\nnums = list(map(int, input().split()))\nresult = two_sum(nums, target)\nprint(result[0], result[1])\n",
					"cpp":    "#include <iostream>\n#include <vector>\n#include <unordered_map>\nusing namespace std;\n\nint main() {\n    int n, target;\n    cin >> n >> target;\n    vector<int> nums(n);\n    for (int i = 0; i < n; i++) cin >> nums[i];\n    // Your code here\n    return 0;\n}\n",
					"go":     "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tvar n, target int\n\tfmt.Scan(&n, &target)\n\tnums := make([]int, n)\n\tfor i := range nums {\n\t\tfmt.Scan(&nums[i])\n\t}\n\t// Your code here\n}\n",
					"java":   "import java.util.*;\n\npublic class Main {\n    public static void main(String[] args) {\n        Scanner sc = new Scanner(System.in);\n        int n = sc.nextInt(), target = sc.nextInt();\n        int[] nums = new int[n];\n        for (int i = 0; i < n; i++) nums[i] = sc.nextInt();\n        // Your code here\n    }\n}\n",
				},
			},
			TestCases: []problem.TestCase{
				{Input: "4 9\n2 7 11 15", ExpectedOutput: "0 1", IsSample: true},
				{Input: "3 6\n3 2 4", ExpectedOutput: "1 2", IsSample: true},
				{Input: "2 6\n3 3", ExpectedOutput: "0 1", IsSample: false},
				{Input: "5 10\n1 2 3 4 6", ExpectedOutput: "3 4", IsSample: false},
				{Input: "4 8\n1 5 2 7", ExpectedOutput: "0 3", IsSample: false},
			},
		},

		// ——— 2. Reverse String ———
		{
			Input: problem.CreateProblemInput{
				Title: "Reverse String",
				Statement: `Write a function that reverses a string. The input string is given as a single line.

Input Format:
A single line containing the string to reverse.

Output Format:
The reversed string.`,
				Difficulty:    problem.DifficultyEasy,
				Tags:          []string{"strings", "two-pointers"},
				TimeLimitMS:   1000,
				MemoryLimitMB: 256,
				StarterCode: map[string]string{
					"python": "s = input()\n# Your code here\nprint(s[::-1])\n",
					"cpp":    "#include <iostream>\n#include <algorithm>\nusing namespace std;\n\nint main() {\n    string s;\n    getline(cin, s);\n    // Your code here\n    return 0;\n}\n",
					"go":     "package main\n\nimport (\n\t\"bufio\"\n\t\"fmt\"\n\t\"os\"\n)\n\nfunc main() {\n\tscanner := bufio.NewScanner(os.Stdin)\n\tscanner.Scan()\n\ts := scanner.Text()\n\t// Your code here\n\t_ = s\n\tfmt.Println()\n}\n",
				},
			},
			TestCases: []problem.TestCase{
				{Input: "hello", ExpectedOutput: "olleh", IsSample: true},
				{Input: "Hannah", ExpectedOutput: "hannaH", IsSample: true},
				{Input: "a", ExpectedOutput: "a", IsSample: false},
				{Input: "abcdef", ExpectedOutput: "fedcba", IsSample: false},
				{Input: "racecar", ExpectedOutput: "racecar", IsSample: false},
			},
		},

		// ——— 3. Valid Parentheses ———
		{
			Input: problem.CreateProblemInput{
				Title: "Valid Parentheses",
				Statement: `Given a string s containing just the characters '(', ')', '{', '}', '[' and ']', determine if the input string is valid.

An input string is valid if:
1. Open brackets must be closed by the same type of brackets.
2. Open brackets must be closed in the correct order.
3. Every close bracket has a corresponding open bracket of the same type.

Input Format:
A single line containing the string s.

Output Format:
Print "true" if valid, "false" otherwise.`,
				Difficulty:    problem.DifficultyEasy,
				Tags:          []string{"stack", "strings"},
				TimeLimitMS:   1000,
				MemoryLimitMB: 256,
				StarterCode: map[string]string{
					"python": "s = input()\n\ndef is_valid(s):\n    # Your code here\n    pass\n\nprint(\"true\" if is_valid(s) else \"false\")\n",
					"cpp":    "#include <iostream>\n#include <stack>\nusing namespace std;\n\nint main() {\n    string s;\n    cin >> s;\n    // Your code here\n    return 0;\n}\n",
					"go":     "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tvar s string\n\tfmt.Scan(&s)\n\t// Your code here\n}\n",
				},
			},
			TestCases: []problem.TestCase{
				{Input: "()", ExpectedOutput: "true", IsSample: true},
				{Input: "()[]{}", ExpectedOutput: "true", IsSample: true},
				{Input: "(]", ExpectedOutput: "false", IsSample: true},
				{Input: "([)]", ExpectedOutput: "false", IsSample: false},
				{Input: "{[]}", ExpectedOutput: "true", IsSample: false},
				{Input: "]", ExpectedOutput: "false", IsSample: false},
				{Input: "((((", ExpectedOutput: "false", IsSample: false},
			},
		},

		// ——— 4. Longest Common Subsequence ———
		{
			Input: problem.CreateProblemInput{
				Title: "Longest Common Subsequence",
				Statement: `Given two strings text1 and text2, return the length of their longest common subsequence. If there is no common subsequence, return 0.

A subsequence of a string is a new string generated from the original string with some characters (can be none) deleted without changing the relative order of the remaining characters.

Input Format:
Two lines, each containing a string.

Output Format:
A single integer — the length of the longest common subsequence.`,
				Difficulty:    problem.DifficultyMedium,
				Tags:          []string{"dynamic-programming", "strings"},
				TimeLimitMS:   3000,
				MemoryLimitMB: 256,
				StarterCode: map[string]string{
					"python": "text1 = input()\ntext2 = input()\n\ndef lcs(t1, t2):\n    # Your code here\n    pass\n\nprint(lcs(text1, text2))\n",
					"cpp":    "#include <iostream>\n#include <vector>\n#include <algorithm>\nusing namespace std;\n\nint main() {\n    string text1, text2;\n    cin >> text1 >> text2;\n    // Your code here\n    return 0;\n}\n",
					"go":     "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tvar text1, text2 string\n\tfmt.Scan(&text1)\n\tfmt.Scan(&text2)\n\t// Your code here\n}\n",
				},
			},
			TestCases: []problem.TestCase{
				{Input: "abcde\nace", ExpectedOutput: "3", IsSample: true},
				{Input: "abc\nabc", ExpectedOutput: "3", IsSample: true},
				{Input: "abc\ndef", ExpectedOutput: "0", IsSample: false},
				{Input: "bsbininm\njmjkbkjkv", ExpectedOutput: "1", IsSample: false},
				{Input: "abcba\nabcbcba", ExpectedOutput: "5", IsSample: false},
			},
		},

		// ——— 5. Merge Intervals ———
		{
			Input: problem.CreateProblemInput{
				Title: "Merge Intervals",
				Statement: `Given an array of intervals where intervals[i] = [start_i, end_i], merge all overlapping intervals, and return an array of the non-overlapping intervals that cover all the intervals in the input.

The input intervals are NOT sorted.

Input Format:
First line: n (number of intervals).
Next n lines: two space-separated integers representing start and end of each interval.

Output Format:
Print the merged intervals sorted by start value, one per line, as "start end".`,
				Difficulty:    problem.DifficultyMedium,
				Tags:          []string{"arrays", "sorting"},
				TimeLimitMS:   2000,
				MemoryLimitMB: 256,
				StarterCode: map[string]string{
					"python": "n = int(input())\nintervals = []\nfor _ in range(n):\n    a, b = map(int, input().split())\n    intervals.append([a, b])\n\n# Your code here\n",
					"cpp":    "#include <iostream>\n#include <vector>\n#include <algorithm>\nusing namespace std;\n\nint main() {\n    int n;\n    cin >> n;\n    vector<pair<int,int>> intervals(n);\n    for (int i = 0; i < n; i++) cin >> intervals[i].first >> intervals[i].second;\n    // Your code here\n    return 0;\n}\n",
					"go":     "package main\n\nimport (\n\t\"fmt\"\n\t\"sort\"\n)\n\nfunc main() {\n\tvar n int\n\tfmt.Scan(&n)\n\tintervals := make([][2]int, n)\n\tfor i := range intervals {\n\t\tfmt.Scan(&intervals[i][0], &intervals[i][1])\n\t}\n\t// Your code here\n\t_ = sort.Ints\n}\n",
				},
			},
			TestCases: []problem.TestCase{
				{Input: "4\n1 3\n2 6\n8 10\n15 18", ExpectedOutput: "1 6\n8 10\n15 18", IsSample: true},
				{Input: "2\n4 5\n1 4", ExpectedOutput: "1 5", IsSample: true},
				{Input: "1\n1 1", ExpectedOutput: "1 1", IsSample: false},
				{Input: "3\n4 5\n1 10\n2 3", ExpectedOutput: "1 10", IsSample: false},
				{Input: "5\n9 10\n1 2\n5 6\n3 4\n7 8", ExpectedOutput: "1 2\n3 4\n5 6\n7 8\n9 10", IsSample: false},
			},
		},
	}
}
