package judge_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/judge"
)

// countingSandbox records how many submissions are live at once, which is
// the number of real containers a Docker sandbox would have running.
type countingSandbox struct {
	live atomic.Int64
	peak atomic.Int64
	hold time.Duration
	err  error
}

func (s *countingSandbox) NewSubmission(ctx context.Context, _, _ string, _ judge.Limits) (judge.SubmissionSandbox, error) {
	if s.err != nil {
		return nil, s.err
	}
	live := s.live.Add(1)
	for {
		peak := s.peak.Load()
		if live <= peak || s.peak.CompareAndSwap(peak, live) {
			break
		}
	}
	if s.hold > 0 {
		select {
		case <-time.After(s.hold):
		case <-ctx.Done():
		}
	}
	return &countingSubmission{parent: s}, nil
}

type countingSubmission struct {
	parent *countingSandbox
	closed atomic.Bool
}

func (s *countingSubmission) Compile(context.Context) (judge.ExecuteResult, error) {
	return judge.ExecuteResult{}, nil
}

func (s *countingSubmission) Run(context.Context, string) (judge.ExecuteResult, error) {
	return judge.ExecuteResult{}, nil
}

func (s *countingSubmission) Close(context.Context) error {
	if s.closed.CompareAndSwap(false, true) {
		s.parent.live.Add(-1)
	}
	return nil
}

func TestLimitedSandbox_CapsConcurrentContainersAcrossEveryCaller(t *testing.T) {
	const cap = 2
	inner := &countingSandbox{hold: 20 * time.Millisecond}
	limited := judge.NewLimitedSandbox(inner, cap)

	// Three independent callers, standing in for the standard lane, the
	// War Room lane and the playground responder — the three consumers
	// that each used to get the whole WORKER_COUNT budget to themselves.
	var wg sync.WaitGroup
	for range 3 {
		for range 6 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sub, err := limited.NewSubmission(context.Background(), "python", "print(1)", judge.Limits{})
				if !assert.NoError(t, err) {
					return
				}
				_ = sub.Close(context.Background())
			}()
		}
	}
	wg.Wait()

	assert.LessOrEqual(t, int(inner.peak.Load()), cap,
		"the cap is global, not per caller")
	assert.Zero(t, inner.live.Load(), "every slot is handed back")
}

func TestLimitedSandbox_ReleasesTheSlotWhenTheInnerSandboxFails(t *testing.T) {
	inner := &countingSandbox{err: errors.New("docker is down")}
	limited := judge.NewLimitedSandbox(inner, 1)

	for range 5 {
		_, err := limited.NewSubmission(context.Background(), "python", "x", judge.Limits{})
		require.Error(t, err)
	}

	// A leaked slot would deadlock the next acquisition rather than fail.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := limited.NewSubmission(ctx, "python", "x", judge.Limits{})
	assert.NotErrorIs(t, err, context.DeadlineExceeded, "a failed create must not hold its slot")
}

func TestLimitedSandbox_ClosingTwiceReleasesOnlyOneSlot(t *testing.T) {
	inner := &countingSandbox{}
	limited := judge.NewLimitedSandbox(inner, 1)

	sub, err := limited.NewSubmission(context.Background(), "python", "x", judge.Limits{})
	require.NoError(t, err)
	require.NoError(t, sub.Close(context.Background()))
	require.NoError(t, sub.Close(context.Background()))

	// If the double close had released two slots, this would let a second
	// container start while the cap says one.
	second, err := limited.NewSubmission(context.Background(), "python", "x", judge.Limits{})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err = limited.NewSubmission(ctx, "python", "x", judge.Limits{})
	assert.ErrorIs(t, err, context.DeadlineExceeded, "the cap still holds after a double close")
	require.NoError(t, second.Close(context.Background()))
}

func TestLimitedSandbox_WaitingRespectsCancellation(t *testing.T) {
	inner := &countingSandbox{}
	limited := judge.NewLimitedSandbox(inner, 1)

	held, err := limited.NewSubmission(context.Background(), "python", "x", judge.Limits{})
	require.NoError(t, err)
	defer held.Close(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = limited.NewSubmission(ctx, "python", "x", judge.Limits{})

	assert.ErrorIs(t, err, context.DeadlineExceeded,
		"a shutting-down worker must not block forever waiting for a slot")
}

func TestNewLimitedSandbox_RefusesANonsensicalCap(t *testing.T) {
	inner := &countingSandbox{}
	for _, cap := range []int{0, -1} {
		limited := judge.NewLimitedSandbox(inner, cap)
		assert.Equal(t, 1, limited.Capacity(),
			"a cap of %d would either deadlock or mean unlimited; neither is safe", cap)
	}
}

func TestLimitedSandbox_CapacityIsReadable(t *testing.T) {
	assert.Equal(t, 3, judge.NewLimitedSandbox(&countingSandbox{}, 3).Capacity(),
		"startup logging needs to state the real ceiling")
}

// memoryStub reports a fixed peak so the wrapper can be checked for
// forwarding it.
type memoryStub struct {
	judge.SubmissionSandbox
	peak int64
}

func (m memoryStub) PeakMemoryKB(context.Context) (int64, bool) { return m.peak, true }

type memorySandbox struct{ peak int64 }

func (m memorySandbox) NewSubmission(context.Context, string, string, judge.Limits) (judge.SubmissionSandbox, error) {
	return memoryStub{SubmissionSandbox: noopSubmission{}, peak: m.peak}, nil
}

type noopSubmission struct{}

func (noopSubmission) Compile(context.Context) (judge.ExecuteResult, error) {
	return judge.ExecuteResult{}, nil
}
func (noopSubmission) Run(context.Context, string) (judge.ExecuteResult, error) {
	return judge.ExecuteResult{}, nil
}
func (noopSubmission) Close(context.Context) error { return nil }

// The wrapper must not swallow the capability. This is the production
// path: the worker wraps every sandbox in LimitedSandbox, so a capability
// the wrapper drops is a capability no submission ever sees — which is
// exactly how memoryKb stayed 0 in production while passing locally,
// where the tests drive DockerSandbox directly.
func TestLimitedSandbox_ForwardsMemoryReporting(t *testing.T) {
	limited := judge.NewLimitedSandbox(memorySandbox{peak: 4242}, 1)
	sub, err := limited.NewSubmission(context.Background(), "python", "", judge.Limits{})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	defer sub.Close(context.Background())

	reporter, ok := sub.(judge.MemoryReporter)
	if !ok {
		t.Fatal("the wrapped submission no longer implements MemoryReporter — " +
			"Judge's type assertion fails and every submission reports no memory")
	}
	peak, measured := reporter.PeakMemoryKB(context.Background())
	if !measured || peak != 4242 {
		t.Errorf("peak = %d measured=%v, want 4242 true", peak, measured)
	}
}
