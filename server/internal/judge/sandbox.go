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

// MemoryReporter is implemented by sandboxes that can report how much
// memory a submission actually used.
//
// It is separate from SubmissionSandbox rather than part of it because
// not every implementation can answer: the in-memory fakes used by the
// unit tests have no cgroup to read. A sandbox that cannot report simply
// does not implement this, and the verdict carries no memory figure —
// which is the honest outcome, and better than every submission claiming
// it used zero.
type MemoryReporter interface {
	PeakMemoryKB(ctx context.Context) (int64, bool)
}
