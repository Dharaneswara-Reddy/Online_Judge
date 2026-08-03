package judge

import "context"

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
	defer sub.Close(ctx)

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
