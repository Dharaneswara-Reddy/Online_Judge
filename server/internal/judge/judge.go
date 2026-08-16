package judge

import (
	"context"
	"log"
	"time"
)

// Judge orchestrates compilation and execution of submissions against
// test cases, delegating the actual compute to the injected Sandbox.
type Judge struct {
	sandbox Sandbox
}

// NewJudge creates a Judge backed by the given Sandbox implementation.
func NewJudge(sandbox Sandbox) *Judge {
	return &Judge{sandbox: sandbox}
}

// Evaluate compiles the source code, runs it against every test case in
// order, and returns a JudgeResult with the appropriate verdict.
// It stops at the first failing test case and always closes the sandbox.
func (j *Judge) Evaluate(ctx context.Context, language, sourceCode string, testCases []TestCase, limits Limits) (JudgeResult, error) {
	sub, err := j.sandbox.NewSubmission(ctx, language, sourceCode, limits)
	if err != nil {
		return JudgeResult{}, err
	}

	// Clean up on a context that outlives ctx. The container is started
	// with `sleep infinity`, so it never exits by itself — and the cases
	// where cleanup matters most (a timeout, a disconnected client) are
	// exactly the cases where ctx is already cancelled and a removal call
	// made with it would never reach the Docker daemon, leaking the
	// container and its memory reservation forever.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := sub.Close(cleanupCtx); err != nil {
			log.Printf("WARNING: could not remove sandbox container: %v", err)
		}
	}()

	// --- Compile step ---
	compileResult, err := sub.Compile(ctx)
	if err != nil {
		return JudgeResult{}, err
	}
	if compileResult.ExitCode != 0 {
		return JudgeResult{
			Verdict:      VerdictCompileError,
			CompileError: compileResult.Stderr,
			FailedCase:   -1,
		}, nil
	}

	// --- Run against each test case ---
	var maxRuntime int64
	for i, tc := range testCases {
		result, err := sub.Run(ctx, tc.Input)
		if err != nil {
			return JudgeResult{}, err
		}

		switch {
		case result.TimedOut:
			return JudgeResult{Verdict: VerdictTimeLimitExceeded, FailedCase: i}, nil
		case result.OOMKilled:
			return JudgeResult{Verdict: VerdictMemoryLimitExceeded, FailedCase: i}, nil
		case result.OutputTruncated:
			// The program printed more than the judge will hold. Its output
			// is incomplete, so it cannot be compared — reject rather than
			// risk a truncated prefix matching by accident.
			return JudgeResult{Verdict: VerdictOutputLimitExceeded, FailedCase: i}, nil
		case result.ExitCode != 0:
			return JudgeResult{Verdict: VerdictRuntimeError, FailedCase: i}, nil
		case !OutputsMatch(tc.ExpectedOutput, result.Stdout):
			return JudgeResult{Verdict: VerdictWrongAnswer, FailedCase: i}, nil
		}

		if result.RuntimeMS > maxRuntime {
			maxRuntime = result.RuntimeMS
		}
	}

	return JudgeResult{
		Verdict:    VerdictAccepted,
		RuntimeMS:  maxRuntime,
		FailedCase: -1,
	}, nil
}
