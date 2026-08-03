package judge

import "time"

// Verdict represents the outcome of evaluating a submission.
type Verdict string

const (
	VerdictAccepted            Verdict = "accepted"
	VerdictWrongAnswer         Verdict = "wrong_answer"
	VerdictTimeLimitExceeded   Verdict = "tle"
	VerdictMemoryLimitExceeded Verdict = "mle"
	VerdictRuntimeError        Verdict = "runtime_error"
	VerdictCompileError        Verdict = "compile_error"
)

// TestCase holds input and expected output for one test case.
type TestCase struct {
	Input          string
	ExpectedOutput string
}

// Limits defines resource constraints for a submission.
type Limits struct {
	TimeLimit     time.Duration
	MemoryLimitMB int64
}

// ExecuteResult captures the outcome of a single compile or run step.
type ExecuteResult struct {
	Stdout    string
	Stderr    string
	ExitCode  int
	TimedOut  bool
	OOMKilled bool
	RuntimeMS int64
}

// JudgeResult is the final verdict after evaluating all test cases.
type JudgeResult struct {
	Verdict      Verdict
	RuntimeMS    int64
	MemoryKB     int64
	FailedCase   int
	CompileError string
}
