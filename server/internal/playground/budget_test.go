package playground

import (
	"context"
	"testing"
	"time"

	"github.com/toji339/online-judge/internal/judge"
)

// slowSandbox spends real time in every run, the way a container does,
// and honours cancellation the way the Docker sandbox does.
type slowSandbox struct {
	delay time.Duration
	runs  int
}

func (s *slowSandbox) NewSubmission(context.Context, string, string, judge.Limits) (judge.SubmissionSandbox, error) {
	return s, nil
}

func (s *slowSandbox) Compile(context.Context) (judge.ExecuteResult, error) {
	return judge.ExecuteResult{}, nil
}

func (s *slowSandbox) Run(ctx context.Context, _ string) (judge.ExecuteResult, error) {
	s.runs++
	select {
	case <-time.After(s.delay):
		return judge.ExecuteResult{Stdout: "ok"}, nil
	case <-ctx.Done():
		return judge.ExecuteResult{}, ctx.Err()
	}
}

func (s *slowSandbox) Close(context.Context) error { return nil }

// TestEvaluateStopsAtTheOverallBudget is the defect. Per-case limits
// multiply: twenty cases at the maximum ten seconds each is over three
// minutes of a two-vCPU host's CPU for one click of "Run", and two such
// requests hold every judging slot on the box while they burn it.
func TestEvaluateStopsAtTheOverallBudget(t *testing.T) {
	sb := &slowSandbox{delay: 30 * time.Millisecond}
	runner := NewLocalRunner(sb)
	runner.budget = 90 * time.Millisecond

	cases := make([]judge.TestCase, MaxTestCases)
	for i := range cases {
		cases[i] = judge.TestCase{Input: "1", ExpectedOutput: "ok"}
	}

	start := time.Now()
	_, err := runner.Run(context.Background(), Request{
		Mode: ModeEvaluate, Language: "python", Code: "x",
		TestCases: cases, TimeLimitMs: MaxTimeLimitMs,
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("want an error once the overall budget is spent")
	}
	if elapsed > time.Second {
		t.Errorf("evaluation ran for %s, well past its budget", elapsed)
	}
	if sb.runs >= MaxTestCases {
		t.Errorf("ran %d of %d cases; the budget did not bound the work", sb.runs, MaxTestCases)
	}
}

// TestRawModeIsBoundedToo: one raw run is smaller, but it is still a
// container the worker must not be stuck inside indefinitely.
func TestRawModeIsBoundedToo(t *testing.T) {
	sb := &slowSandbox{delay: time.Minute}
	runner := NewLocalRunner(sb)
	runner.budget = 50 * time.Millisecond

	start := time.Now()
	_, err := runner.Run(context.Background(), Request{Mode: ModeRaw, Language: "python", Code: "x"})

	if err == nil {
		t.Fatal("want an error once the overall budget is spent")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("raw run took %s despite its budget", elapsed)
	}
}

// TestDefaultBudgetIsShorterThanTheCallerWait: computing past the point
// where the caller has given up is pure waste, so the worker must stop
// first.
func TestDefaultBudgetIsShorterThanTheCallerWait(t *testing.T) {
	if MaxTotalRuntime >= RemoteTimeout {
		t.Errorf("MaxTotalRuntime (%s) must be under RemoteTimeout (%s)", MaxTotalRuntime, RemoteTimeout)
	}
	if worst := time.Duration(MaxTestCases) * time.Duration(MaxTimeLimitMs) * time.Millisecond; MaxTotalRuntime >= worst {
		t.Errorf("MaxTotalRuntime (%s) must bound the per-case limits (%s), not restate them",
			MaxTotalRuntime, worst)
	}
}
