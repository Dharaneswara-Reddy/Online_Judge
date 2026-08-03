package judge

import (
	"context"
	"testing"
	"time"
)

// --- Fake sandbox (in-memory, zero Docker dependency) ---

type fakeSandbox struct{ submission *fakeSubmission }

func (f *fakeSandbox) NewSubmission(ctx context.Context, language, sourceCode string, limits Limits) (SubmissionSandbox, error) {
	return f.submission, nil
}

type fakeSubmission struct {
	compileResult ExecuteResult
	runResults    []ExecuteResult
	runIndex      int
	closed        bool
}

func (f *fakeSubmission) Compile(ctx context.Context) (ExecuteResult, error) {
	return f.compileResult, nil
}
func (f *fakeSubmission) Run(ctx context.Context, stdin string) (ExecuteResult, error) {
	r := f.runResults[f.runIndex]
	f.runIndex++
	return r, nil
}
func (f *fakeSubmission) Close(ctx context.Context) error {
	f.closed = true
	return nil
}

// --- Tests ---

func TestJudge_Evaluate(t *testing.T) {
	limits := Limits{TimeLimit: time.Second, MemoryLimitMB: 256}
	testCases := []TestCase{{Input: "3\n", ExpectedOutput: "6\n"}}

	t.Run("accepted", func(t *testing.T) {
		sub := &fakeSubmission{runResults: []ExecuteResult{{Stdout: "6\n", ExitCode: 0, RuntimeMS: 12}}}
		j := NewJudge(&fakeSandbox{submission: sub})
		result, err := j.Evaluate(context.Background(), "python", "print(int(input())*2)", testCases, limits)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Verdict != VerdictAccepted {
			t.Errorf("got verdict %v, want accepted", result.Verdict)
		}
		if !sub.closed {
			t.Errorf("expected sandbox to be closed after evaluation")
		}
	})

	t.Run("wrong answer", func(t *testing.T) {
		sub := &fakeSubmission{runResults: []ExecuteResult{{Stdout: "7\n", ExitCode: 0}}}
		j := NewJudge(&fakeSandbox{submission: sub})
		result, _ := j.Evaluate(context.Background(), "python", "...", testCases, limits)
		if result.Verdict != VerdictWrongAnswer || result.FailedCase != 0 {
			t.Errorf("got verdict %v failedCase %d, want wrong_answer/0", result.Verdict, result.FailedCase)
		}
	})

	t.Run("time limit exceeded", func(t *testing.T) {
		sub := &fakeSubmission{runResults: []ExecuteResult{{TimedOut: true}}}
		j := NewJudge(&fakeSandbox{submission: sub})
		result, _ := j.Evaluate(context.Background(), "cpp", "...", testCases, limits)
		if result.Verdict != VerdictTimeLimitExceeded {
			t.Errorf("got verdict %v, want tle", result.Verdict)
		}
	})

	t.Run("memory limit exceeded", func(t *testing.T) {
		sub := &fakeSubmission{runResults: []ExecuteResult{{OOMKilled: true}}}
		j := NewJudge(&fakeSandbox{submission: sub})
		result, _ := j.Evaluate(context.Background(), "cpp", "...", testCases, limits)
		if result.Verdict != VerdictMemoryLimitExceeded {
			t.Errorf("got verdict %v, want mle", result.Verdict)
		}
	})

	t.Run("runtime error", func(t *testing.T) {
		sub := &fakeSubmission{runResults: []ExecuteResult{{ExitCode: 1, Stderr: "panic"}}}
		j := NewJudge(&fakeSandbox{submission: sub})
		result, _ := j.Evaluate(context.Background(), "go", "...", testCases, limits)
		if result.Verdict != VerdictRuntimeError {
			t.Errorf("got verdict %v, want runtime_error", result.Verdict)
		}
	})

	t.Run("compile error closes sandbox and skips run", func(t *testing.T) {
		sub := &fakeSubmission{compileResult: ExecuteResult{ExitCode: 1, Stderr: "syntax error"}}
		j := NewJudge(&fakeSandbox{submission: sub})
		result, _ := j.Evaluate(context.Background(), "cpp", "int main( {", testCases, limits)
		if result.Verdict != VerdictCompileError || result.CompileError == "" {
			t.Errorf("got verdict %v compileError %q, want compile_error with a message", result.Verdict, result.CompileError)
		}
		if !sub.closed {
			t.Errorf("expected sandbox to be closed even after a compile error")
		}
	})

	t.Run("stops at first failing test case", func(t *testing.T) {
		multiCases := []TestCase{{Input: "1", ExpectedOutput: "2"}, {Input: "2", ExpectedOutput: "4"}}
		sub := &fakeSubmission{runResults: []ExecuteResult{{Stdout: "2", ExitCode: 0}, {Stdout: "5", ExitCode: 0}}}
		j := NewJudge(&fakeSandbox{submission: sub})
		result, _ := j.Evaluate(context.Background(), "python", "...", multiCases, limits)
		if result.Verdict != VerdictWrongAnswer || result.FailedCase != 1 {
			t.Errorf("got verdict %v failedCase %d, want wrong_answer at case 1", result.Verdict, result.FailedCase)
		}
	})
}
