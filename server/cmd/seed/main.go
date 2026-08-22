// Package main provides a seed script that populates the database with
// 5 classic competitive programming problems and their test cases.
// It also promotes a specified user to admin role.
//
// Usage: go run cmd/seed/main.go [--admin-email user@example.com]
package main

import (
	"context"
	"flag"
	"log"
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
	for _, sp := range problems {
		existing, err := svc.GetBySlug(ctx, problem.Slugify(sp.Input.Title))
		if err == nil {
			log.Printf("SKIP: %q already exists (id=%s)", sp.Input.Title, existing.ID)
			// Still add test cases if missing
			seedTestCases(ctx, svc, existing.ID, sp.TestCases)
			continue
		}

		p, err := svc.Create(ctx, sp.Input)
		if err != nil {
			log.Printf("ERROR creating %q: %v", sp.Input.Title, err)
			continue
		}
		log.Printf("CREATED: %q (slug=%s, id=%s)", p.Title, p.Slug, p.ID)

		seedTestCases(ctx, svc, p.ID, sp.TestCases)
	}

	log.Println("Seed completed successfully!")
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

func seedTestCases(ctx context.Context, svc *problem.Service, problemID string, tcs []problem.TestCase) {
	// Check if test cases already exist
	existing, _ := svc.ListAllTestCases(ctx, problemID)
	if len(existing) > 0 {
		log.Printf("  SKIP test cases: %d already exist", len(existing))
		return
	}
	for i, tc := range tcs {
		tc.ProblemID = problemID
		if err := svc.AddTestCase(ctx, &tc); err != nil {
			log.Printf("  ERROR adding test case %d: %v", i+1, err)
			continue
		}
		label := "hidden"
		if tc.IsSample {
			label = "sample"
		}
		log.Printf("  ADDED test case %d (%s)", i+1, label)
	}
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
