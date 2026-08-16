// Package worker contains the judging pipeline that turns a queued job
// into a verdict. The same Processor is used by the standalone judge
// worker binary and by the API's inline fallback when the queue is
// unavailable, so a submission is judged identically either way.
package worker

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/toji339/online-judge/internal/judge"
	"github.com/toji339/online-judge/internal/problem"
	"github.com/toji339/online-judge/internal/queue"
	"github.com/toji339/online-judge/internal/submission"
)

// evaluationTimeout bounds one whole evaluation — compilation plus every
// test case — independently of the per-run limits inside the sandbox.
const evaluationTimeout = 60 * time.Second

// Notifier is told about every judged submission. The War Room uses it
// to broadcast live progress; the standalone worker uses a no-op.
type Notifier interface {
	SubmissionJudged(ctx context.Context, sub *submission.Submission)
}

// NopNotifier ignores every event.
type NopNotifier struct{}

func (NopNotifier) SubmissionJudged(context.Context, *submission.Submission) {}

// Processor judges one submission at a time.
type Processor struct {
	submissions *submission.Service
	problems    *problem.Service
	engine      *judge.Judge
	notifier    Notifier
}

// NewProcessor wires a Processor. A nil notifier is replaced with a no-op.
func NewProcessor(submissions *submission.Service, problems *problem.Service, sandbox judge.Sandbox, notifier Notifier) *Processor {
	if notifier == nil {
		notifier = NopNotifier{}
	}
	return &Processor{
		submissions: submissions,
		problems:    problems,
		engine:      judge.NewJudge(sandbox),
		notifier:    notifier,
	}
}

// Process judges the submission referenced by a queued job.
//
// It is safe to call more than once for the same job: a submission that
// already carries a terminal verdict is skipped, so a redelivered
// message cannot overwrite a result or double-count a solve.
func (p *Processor) Process(ctx context.Context, job queue.Job) error {
	// Steps to follow while judging a queued submission
	// ===================================================

	// 1. Load the stored submission — it, not the message, holds the code
	sub, err := p.submissions.GetByID(ctx, job.SubmissionID)
	if err != nil {
		return fmt.Errorf("load submission %s: %w", job.SubmissionID, err)
	}
	if sub.Status.IsTerminal() {
		log.Printf("worker: submission %s already judged (%s), skipping", sub.ID, sub.Status)
		return nil
	}

	// 2. Load the problem and its full test-case set
	prob, err := p.problems.GetByID(ctx, sub.ProblemID)
	if err != nil {
		p.fail(ctx, sub, "problem could not be loaded")
		return fmt.Errorf("load problem %s: %w", sub.ProblemID, err)
	}

	cases, err := p.problems.ListAllTestCases(ctx, prob.ID)
	if err != nil {
		p.fail(ctx, sub, "test cases could not be loaded")
		return fmt.Errorf("load test cases: %w", err)
	}
	if len(cases) == 0 {
		p.fail(ctx, sub, "problem has no test cases")
		return nil
	}

	// 3. Flag it as running so the client's poll shows progress
	if err := p.submissions.MarkRunning(ctx, sub.ID); err != nil {
		p.fail(ctx, sub, "could not start judging")
		return fmt.Errorf("mark running: %w", err)
	}

	// 4. Evaluate inside the sandbox
	result, err := p.evaluate(ctx, sub, prob, cases)
	if err != nil {
		// A cancelled context means the worker is shutting down, not that
		// the submission is bad. Leave the record alone so a redelivery
		// judges it properly rather than stamping a bogus runtime error on
		// someone's correct solution.
		if ctx.Err() != nil {
			return fmt.Errorf("judging interrupted for submission %s: %w", sub.ID, ctx.Err())
		}
		p.fail(ctx, sub, "execution engine error")
		return fmt.Errorf("evaluate submission %s: %w", sub.ID, err)
	}

	// 5. Record the verdict, then announce it
	if err := p.submissions.MarkJudged(ctx, sub.ID, result); err != nil {
		// Without this the record stays "running" forever, and because
		// admission control counts non-terminal submissions the user would
		// be refused every future submission from then on.
		p.fail(ctx, sub, "could not record the verdict")
		return fmt.Errorf("record verdict: %w", err)
	}

	judged, err := p.submissions.GetByID(ctx, sub.ID)
	if err != nil {
		return fmt.Errorf("reload judged submission: %w", err)
	}
	p.notifier.SubmissionJudged(ctx, judged)
	return nil
}

// evaluate runs the judge engine against every test case.
func (p *Processor) evaluate(ctx context.Context, sub *submission.Submission, prob *problem.Problem, cases []problem.TestCase) (submission.Result, error) {
	judgeCases := make([]judge.TestCase, len(cases))
	for i, tc := range cases {
		judgeCases[i] = judge.TestCase{Input: tc.Input, ExpectedOutput: tc.ExpectedOutput}
	}

	limits := judge.Limits{
		TimeLimit:     time.Duration(prob.TimeLimitMS) * time.Millisecond,
		MemoryLimitMB: int64(prob.MemoryLimitMB),
	}

	evalCtx, cancel := context.WithTimeout(ctx, evaluationTimeout)
	defer cancel()

	verdict, err := p.engine.Evaluate(evalCtx, sub.Language, sub.Code, judgeCases, limits)
	if err != nil {
		return submission.Result{}, err
	}

	return submission.Result{
		Status:       submission.StatusFromVerdict(verdict.Verdict),
		RuntimeMS:    verdict.RuntimeMS,
		MemoryKB:     verdict.MemoryKB,
		FailedCase:   verdict.FailedCase,
		TotalCases:   len(cases),
		CompileError: verdict.CompileError,
	}, nil
}

// fail records an infrastructure failure on the submission so it never
// stays pending, and still notifies listeners waiting on a result.
func (p *Processor) fail(ctx context.Context, sub *submission.Submission, reason string) {
	// The caller's context may already be cancelled, so use a fresh one.
	failCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	if err := p.submissions.MarkFailed(failCtx, sub.ID, reason); err != nil {
		log.Printf("WARNING: could not mark submission %s as failed: %v", sub.ID, err)
		return
	}
	if judged, err := p.submissions.GetByID(failCtx, sub.ID); err == nil {
		p.notifier.SubmissionJudged(failCtx, judged)
	}
}
