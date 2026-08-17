package playground

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/toji339/online-judge/internal/judge"
)

// fakeSandbox records what limits it was handed and returns scripted
// results, so these tests assert on behaviour rather than on Docker.
type fakeSandbox struct {
	gotLimits judge.Limits
	gotCode   string
	gotStdin  string

	newErr     error
	compile    judge.ExecuteResult
	compileErr error
	run        judge.ExecuteResult
	runErr     error

	closed bool
}

func (f *fakeSandbox) NewSubmission(_ context.Context, _, code string, limits judge.Limits) (judge.SubmissionSandbox, error) {
	if f.newErr != nil {
		return nil, f.newErr
	}
	f.gotLimits = limits
	f.gotCode = code
	return f, nil
}

func (f *fakeSandbox) Compile(context.Context) (judge.ExecuteResult, error) {
	return f.compile, f.compileErr
}

func (f *fakeSandbox) Run(_ context.Context, stdin string) (judge.ExecuteResult, error) {
	f.gotStdin = stdin
	return f.run, f.runErr
}

func (f *fakeSandbox) Close(context.Context) error {
	f.closed = true
	return nil
}

func TestRawModeReturnsProgramOutput(t *testing.T) {
	sb := &fakeSandbox{run: judge.ExecuteResult{Stdout: "olleh", ExitCode: 0, RuntimeMS: 12}}

	got, err := NewLocalRunner(sb).Run(context.Background(), Request{
		Mode: ModeRaw, Language: "python", Code: "print(input()[::-1])", Stdin: "hello",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got.Stdout != "olleh" {
		t.Errorf("stdout = %q, want %q", got.Stdout, "olleh")
	}
	if sb.gotStdin != "hello" {
		t.Errorf("stdin passed to sandbox = %q, want %q", sb.gotStdin, "hello")
	}
	if !sb.closed {
		t.Error("sandbox was not closed; containers would leak")
	}
}

func TestRawModeReportsCompileFailureAsAResultNotAnError(t *testing.T) {
	// A program that does not build is a normal answer for the user to
	// read, not a server-side failure.
	sb := &fakeSandbox{compile: judge.ExecuteResult{ExitCode: 1, Stderr: "syntax error"}}

	got, err := NewLocalRunner(sb).Run(context.Background(), Request{
		Mode: ModeRaw, Language: "cpp", Code: "int main(){",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !got.CompileFailed {
		t.Error("CompileFailed = false, want true")
	}
	if got.Stderr != "syntax error" {
		t.Errorf("stderr = %q, want the compiler message", got.Stderr)
	}
	if !sb.closed {
		t.Error("sandbox was not closed after a compile failure")
	}
}

func TestSandboxIsClosedEvenWhenTheRunFails(t *testing.T) {
	sb := &fakeSandbox{runErr: errors.New("docker exploded")}

	_, err := NewLocalRunner(sb).Run(context.Background(), Request{
		Mode: ModeRaw, Language: "python", Code: "x",
	})
	if err == nil {
		t.Fatal("expected an error when the run fails")
	}
	if !sb.closed {
		t.Error("sandbox leaked: Close was not called after a failed run")
	}
}

func TestCreateSandboxFailureIsReported(t *testing.T) {
	sb := &fakeSandbox{newErr: errors.New("no docker daemon")}

	_, err := NewLocalRunner(sb).Run(context.Background(), Request{
		Mode: ModeRaw, Language: "python", Code: "x",
	})
	if err == nil {
		t.Fatal("expected an error when the sandbox cannot be created")
	}
	if !strings.Contains(err.Error(), "create sandbox") {
		t.Errorf("error = %v, want it to name the failing step", err)
	}
}

// The worker must not trust limits that arrived over the broker: an
// unclamped memory ceiling above physical memory does not bind, and the
// host OOM killer fires instead of the container's.
func TestLimitsAreClampedByWhoeverCreatesTheContainer(t *testing.T) {
	sb := &fakeSandbox{}

	_, err := NewLocalRunner(sb).Run(context.Background(), Request{
		Mode: ModeRaw, Language: "python", Code: "x",
		TimeLimitMs: 999999, MemoryLimitMB: 999999,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if want := time.Duration(MaxTimeLimitMs) * time.Millisecond; sb.gotLimits.TimeLimit != want {
		t.Errorf("TimeLimit = %v, want it clamped to %v", sb.gotLimits.TimeLimit, want)
	}
	if sb.gotLimits.MemoryLimitMB != MaxMemoryLimitMB {
		t.Errorf("MemoryLimitMB = %d, want it clamped to %d", sb.gotLimits.MemoryLimitMB, MaxMemoryLimitMB)
	}
}

func TestAbsentLimitsFallBackToDefaults(t *testing.T) {
	sb := &fakeSandbox{}

	if _, err := NewLocalRunner(sb).Run(context.Background(), Request{
		Mode: ModeRaw, Language: "python", Code: "x",
	}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if want := time.Duration(DefaultTimeLimitMs) * time.Millisecond; sb.gotLimits.TimeLimit != want {
		t.Errorf("TimeLimit = %v, want default %v", sb.gotLimits.TimeLimit, want)
	}
	if sb.gotLimits.MemoryLimitMB != DefaultMemoryLimitMB {
		t.Errorf("MemoryLimitMB = %d, want default %d", sb.gotLimits.MemoryLimitMB, DefaultMemoryLimitMB)
	}
}

func TestOversizedCodeIsRejectedBeforeAnyContainerIsCreated(t *testing.T) {
	sb := &fakeSandbox{}

	_, err := NewLocalRunner(sb).Run(context.Background(), Request{
		Mode: ModeRaw, Language: "python", Code: strings.Repeat("x", MaxCodeBytes+1),
	})
	if err == nil {
		t.Fatal("expected oversized code to be rejected")
	}
	if sb.gotCode != "" {
		t.Error("a sandbox was created for oversized code")
	}
}

func TestTooManyTestCasesAreRejected(t *testing.T) {
	sb := &fakeSandbox{}
	cases := make([]judge.TestCase, MaxTestCases+1)

	if _, err := NewLocalRunner(sb).Run(context.Background(), Request{
		Mode: ModeEvaluate, Language: "python", Code: "x", TestCases: cases,
	}); err == nil {
		t.Fatal("expected too many test cases to be rejected")
	}
}

func TestUnknownModeIsRejected(t *testing.T) {
	if _, err := NewLocalRunner(&fakeSandbox{}).Run(context.Background(), Request{
		Mode: "definitely-not-a-mode", Language: "python", Code: "x",
	}); !errors.Is(err, ErrUnknownMode) {
		t.Fatalf("error = %v, want ErrUnknownMode", err)
	}
}

func TestEvaluateModeNeedsAtLeastOneTestCase(t *testing.T) {
	if _, err := NewLocalRunner(&fakeSandbox{}).Run(context.Background(), Request{
		Mode: ModeEvaluate, Language: "python", Code: "x",
	}); err == nil {
		t.Fatal("expected evaluate with no test cases to be rejected")
	}
}

func TestEvaluateModeReturnsAVerdict(t *testing.T) {
	sb := &fakeSandbox{run: judge.ExecuteResult{Stdout: "olleh", ExitCode: 0}}

	got, err := NewLocalRunner(sb).Run(context.Background(), Request{
		Mode: ModeEvaluate, Language: "python", Code: "x",
		TestCases: []judge.TestCase{{Input: "hello", ExpectedOutput: "olleh"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got.Verdict != judge.VerdictAccepted {
		t.Errorf("verdict = %q, want %q", got.Verdict, judge.VerdictAccepted)
	}
}
