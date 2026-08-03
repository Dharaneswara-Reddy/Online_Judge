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
