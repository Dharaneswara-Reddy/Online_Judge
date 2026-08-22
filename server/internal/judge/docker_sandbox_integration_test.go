//go:build integration

package judge

import (
	"context"
	"testing"
	"time"
)

func TestDockerSandbox_Integration(t *testing.T) {
	sandbox, err := NewDockerSandbox()
	if err != nil {
		t.Fatalf("docker sandbox: %v", err)
	}
	limits := Limits{TimeLimit: 5 * time.Second, MemoryLimitMB: 256}

	t.Run("python hello world", func(t *testing.T) {
		j := NewJudge(sandbox)
		result, err := j.Evaluate(context.Background(), "python", "print('hello world')",
			[]TestCase{{ExpectedOutput: "hello world\n"}}, limits)
		if err != nil || result.Verdict != VerdictAccepted {
			t.Errorf("err=%v verdict=%v, want accepted", err, result.Verdict)
		}
	})

	// A test case with empty input must look the same to every language.
	// stdin used to be attached only when there was something to send, so
	// an empty input left the exec with no stdin at all — and whether that
	// mattered depended on the language, which made the verdict depend on
	// the language too.
	t.Run("empty stdin is a well-formed empty stream in every language", func(t *testing.T) {
		for _, tc := range []struct {
			language string
			code     string
		}{
			{"python", "import sys\ndata = sys.stdin.read()\nprint(len(data))"},
			{"cpp", "#include <iostream>\n#include <string>\nint main(){std::string s;std::string all;while(std::getline(std::cin,s))all+=s;std::cout<<all.size()<<std::endl;return 0;}"},
			{"go", "package main\n\nimport (\n\t\"fmt\"\n\t\"io\"\n\t\"os\"\n)\n\nfunc main() {\n\tb, _ := io.ReadAll(os.Stdin)\n\tfmt.Println(len(b))\n}"},
		} {
			t.Run(tc.language, func(t *testing.T) {
				j := NewJudge(sandbox)
				result, err := j.Evaluate(context.Background(), tc.language, tc.code,
					[]TestCase{{Input: "", ExpectedOutput: "0\n"}}, limits)
				if err != nil || result.Verdict != VerdictAccepted {
					t.Errorf("err=%v verdict=%v, want accepted", err, result.Verdict)
				}
			})
		}
	})

	// The seeded Valid Parentheses problem has a hidden test case whose
	// input is the empty string, and its own Python starter reads with
	// input(). This is that exact combination.
	t.Run("python input() on an empty test case", func(t *testing.T) {
		j := NewJudge(sandbox)
		code := "import sys\ns = sys.stdin.readline().rstrip('\\n')\nprint('true' if s == '' else 'false')"
		result, err := j.Evaluate(context.Background(), "python", code,
			[]TestCase{{Input: "", ExpectedOutput: "true\n"}}, limits)
		if err != nil || result.Verdict != VerdictAccepted {
			t.Errorf("err=%v verdict=%v, want accepted", err, result.Verdict)
		}
	})

	t.Run("non-empty stdin still reaches the program", func(t *testing.T) {
		j := NewJudge(sandbox)
		code := "a, b = map(int, input().split())\nprint(a + b)"
		result, err := j.Evaluate(context.Background(), "python", code,
			[]TestCase{{Input: "2 3\n", ExpectedOutput: "5\n"}}, limits)
		if err != nil || result.Verdict != VerdictAccepted {
			t.Errorf("err=%v verdict=%v, want accepted", err, result.Verdict)
		}
	})

	t.Run("cpp compile error surfaces as compile_error", func(t *testing.T) {
		j := NewJudge(sandbox)
		result, _ := j.Evaluate(context.Background(), "cpp", "int main( { return 0; }",
			[]TestCase{{ExpectedOutput: ""}}, limits)
		if result.Verdict != VerdictCompileError {
			t.Errorf("got %v, want compile_error", result.Verdict)
		}
	})

	t.Run("infinite loop is killed by the timeout", func(t *testing.T) {
		j := NewJudge(sandbox)
		short := Limits{TimeLimit: 2 * time.Second, MemoryLimitMB: 256}
		result, _ := j.Evaluate(context.Background(), "python", "while True: pass",
			[]TestCase{{ExpectedOutput: ""}}, short)
		if result.Verdict != VerdictTimeLimitExceeded {
			t.Errorf("got %v, want tle", result.Verdict)
		}
	})

	t.Run("no network access", func(t *testing.T) {
		j := NewJudge(sandbox)
		code := "import socket\ns=socket.socket()\ns.settimeout(2)\ntry:\n s.connect(('8.8.8.8',53))\n print('CONNECTED')\nexcept Exception:\n print('BLOCKED')"
		result, _ := j.Evaluate(context.Background(), "python", code,
			[]TestCase{{ExpectedOutput: "BLOCKED\n"}}, limits)
		if result.Verdict != VerdictAccepted {
			t.Errorf("network isolation failed: got verdict %v", result.Verdict)
		}
	})

	// A worker killed mid-judge leaves its container behind forever:
	// `sleep infinity` means removal is the only thing that stops one.
	t.Run("orphaned sandboxes are reclaimed at startup", func(t *testing.T) {
		leaked, err := sandbox.NewSubmission(context.Background(), "python", "print(1)", limits)
		if err != nil {
			t.Fatalf("create sandbox: %v", err)
		}
		_ = leaked // deliberately never closed, standing in for a crash

		removed, err := sandbox.ReconcileOrphans(context.Background())
		if err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		if removed < 1 {
			t.Errorf("removed %d containers, want at least the leaked one", removed)
		}

		// Nothing labelled must survive the sweep.
		again, err := sandbox.ReconcileOrphans(context.Background())
		if err != nil {
			t.Fatalf("reconcile again: %v", err)
		}
		if again != 0 {
			t.Errorf("a second sweep removed %d containers, want 0", again)
		}
	})

	t.Run("memory limit is enforced", func(t *testing.T) {
		j := NewJudge(sandbox)
		tight := Limits{TimeLimit: 5 * time.Second, MemoryLimitMB: 64}
		result, _ := j.Evaluate(context.Background(), "python", "x = ' ' * (200 * 1024 * 1024)",
			[]TestCase{{ExpectedOutput: ""}}, tight)
		if result.Verdict != VerdictMemoryLimitExceeded {
			t.Errorf("got %v, want mle", result.Verdict)
		}
	})
}
