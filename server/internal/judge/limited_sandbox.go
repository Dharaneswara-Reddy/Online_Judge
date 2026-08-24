package judge

import (
	"context"
	"sync"
)

// LimitedSandbox wraps a Sandbox with a hard ceiling on how many
// environments may exist at the same time.
//
// It exists because the thing that has to be bounded is the number of
// *containers on the host*, and nothing upstream of here can bound that.
// A judge worker runs three independent consumers — the standard lane,
// the War Room lane, and the playground responder — and each was given
// the same WORKER_COUNT budget, so the real ceiling was three times the
// number anyone configured. Each container asks for a full vCPU and up to
// 512MB; on a two-vCPU, 916MB instance that arithmetic ends with the OOM
// killer, and it did, twice.
//
// Putting the semaphore around sandbox creation rather than around each
// consumer means the ceiling holds no matter how many consumers exist or
// how they are configured, including any added later. A caller blocks
// until a slot frees, or until its context ends — a worker draining for
// shutdown must not be stuck waiting for a container it will never get.
//
// The slot is held for the whole life of the environment, from creation
// until Close, because that is exactly how long the container exists.
type LimitedSandbox struct {
	inner Sandbox
	slots chan struct{}
}

// NewLimitedSandbox caps inner at maxConcurrent live environments.
//
// A cap below one is raised to one rather than treated as "unlimited":
// zero would deadlock every caller, and silently removing the ceiling is
// the failure this type exists to prevent.
func NewLimitedSandbox(inner Sandbox, maxConcurrent int) *LimitedSandbox {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &LimitedSandbox{
		inner: inner,
		slots: make(chan struct{}, maxConcurrent),
	}
}

// Capacity reports the ceiling, so a process can state it at startup
// instead of leaving operators to infer it from several settings.
func (l *LimitedSandbox) Capacity() int { return cap(l.slots) }

// NewSubmission waits for a free slot, then creates the environment.
func (l *LimitedSandbox) NewSubmission(ctx context.Context, language, sourceCode string, limits Limits) (SubmissionSandbox, error) {
	select {
	case l.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	sub, err := l.inner.NewSubmission(ctx, language, sourceCode, limits)
	if err != nil {
		// No container was created, so the slot goes straight back. Losing
		// it here would shrink the ceiling by one for the rest of the
		// process's life, and a daemon having a bad minute would starve
		// judging permanently.
		<-l.slots
		return nil, err
	}
	return &limitedSubmission{inner: sub, release: l.release}, nil
}

func (l *LimitedSandbox) release() { <-l.slots }

// limitedSubmission returns its slot when the environment is closed.
type limitedSubmission struct {
	inner   SubmissionSandbox
	once    sync.Once
	release func()
}

func (s *limitedSubmission) Compile(ctx context.Context) (ExecuteResult, error) {
	return s.inner.Compile(ctx)
}

func (s *limitedSubmission) Run(ctx context.Context, stdin string) (ExecuteResult, error) {
	return s.inner.Run(ctx, stdin)
}

// PeakMemoryKB forwards to the wrapped environment.
//
// Without this the wrapper silently swallows the capability: Judge asks
// for MemoryReporter with a type assertion, limitedSubmission does not
// implement it, and every submission reports no memory at all. It passed
// every local test because those drive DockerSandbox directly — the
// wrapper is only added in the worker, so the gap appeared solely in
// production.
//
// A wrapper that can be dropped from an optional interface is a wrapper
// that hides features, so anything added to SubmissionSandbox has to be
// forwarded here too.
func (s *limitedSubmission) PeakMemoryKB(ctx context.Context) (int64, bool) {
	reporter, ok := s.inner.(MemoryReporter)
	if !ok {
		return 0, false
	}
	return reporter.PeakMemoryKB(ctx)
}

// Close tears the environment down and hands the slot back exactly once,
// however many times it is called. Releasing twice would quietly raise
// the ceiling, which is the same bug in a different shape.
func (s *limitedSubmission) Close(ctx context.Context) error {
	err := s.inner.Close(ctx)
	s.once.Do(s.release)
	return err
}

// Compile-time proof that the wrapper is still a Sandbox.
var _ Sandbox = (*LimitedSandbox)(nil)
