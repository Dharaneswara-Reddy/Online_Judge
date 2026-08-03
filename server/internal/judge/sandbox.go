package judge

import "context"

// Sandbox provisions isolated, per-submission execution environments.
type Sandbox interface {
	NewSubmission(ctx context.Context, language, sourceCode string, limits Limits) (SubmissionSandbox, error)
}

// SubmissionSandbox is a handle to one isolated environment tied to a
// single submission. It's reused across Compile and every test-case Run
// so compiled languages are only compiled once, not once per test case.
type SubmissionSandbox interface {
	Compile(ctx context.Context) (ExecuteResult, error)
	Run(ctx context.Context, stdin string) (ExecuteResult, error)
	Close(ctx context.Context) error
}
