// Package worker contains the judging pipeline that turns a queued job
// into a verdict. The same Processor is used by the standalone judge
// worker binary and by the API's inline fallback when the queue is
// unavailable, so a submission is judged identically either way.
package worker

import (
	"context"
	"errors"
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
// It is safe to call more than once for the same job, and safe for two
// workers to call concurrently. Both writes it makes to the submission
// are conditional on the state it expects to find: claiming the job
// requires it to still be pending (or abandoned by a dead worker), and
// recording the verdict requires it to still be running. Losing either
// race means abandoning the work rather than proceeding — otherwise a
// redelivery arriving mid-judge flips the verdict, restamps judged_at
// (which is what decides a War Room winner), and broadcasts twice.
func (p *Processor) Process(ctx context.Context, job queue.Job) error {
	// Steps to follow while judging a queued submission
	// ===================================================

	// 1. Load the stored submission — it, not the message, holds the code
	sub, err := p.submissions.GetByID(ctx, job.SubmissionID)
	if err != nil {
		// A submission that does not exist can never be judged, so the
		// message is dropped rather than retried. Any other failure is
		// reported: the consumer redelivers it once, and if that fails too
		// the record is left pending for the reaper, which is what stops a
		// read failure holding the user's admission slot forever.
		if errors.Is(err, submission.ErrNotFound) {
			log.Printf("worker: discarding job for unknown submission %s", job.SubmissionID)
			return nil
		}
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

	// 3. Claim it. The claim is what makes concurrent delivery safe: it
	//    succeeds only while the submission is still pending, or while it
	//    is running but was claimed so long ago that the worker holding
	//    it must be gone. Losing the claim means another worker is on it,
	//    so acknowledge the message and do nothing.
	if err := p.submissions.MarkRunning(ctx, sub.ID); err != nil {
		if errors.Is(err, submission.ErrAlreadyClaimed) {
			log.Printf("worker: submission %s is already being judged elsewhere, skipping", sub.ID)
			return nil
		}
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
		// The user-facing reason stays generic so Docker internals never
		// reach a client, but the real error is logged: without it a
		// sandbox failure is undiagnosable after the fact.
		log.Printf("ERROR: sandbox failure judging submission %s (problem %s, %s): %v",
			sub.ID, sub.ProblemID, sub.Language, err)
		p.fail(ctx, sub, "execution engine error")
		return fmt.Errorf("evaluate submission %s: %w", sub.ID, err)
	}

	// 5. Record the verdict, then announce it. The write is conditional on
	//    the submission still running, so if another worker judged it
	//    while we were in the sandbox, this verdict is thrown away rather
	//    than overwriting theirs — and nothing is broadcast a second time.
	if err := p.submissions.MarkJudged(ctx, sub.ID, result); err != nil {
		if errors.Is(err, submission.ErrAlreadyJudged) {
			log.Printf("worker: submission %s was judged elsewhere, discarding this verdict", sub.ID)
			return nil
		}
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
//
// The stored status is StatusJudgeError, not a runtime error: something
// in the judge broke, and there is no evidence the user's program did.
func (p *Processor) fail(ctx context.Context, sub *submission.Submission, reason string) {
	// The caller's context may already be cancelled, so use a fresh one.
	failCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	if err := p.submissions.MarkFailed(failCtx, sub.ID, reason); err != nil {
		// Losing to a real verdict is the good outcome, not a warning:
		// the submission is already terminal and someone else told the
		// listeners about it.
		if errors.Is(err, submission.ErrAlreadyJudged) {
			return
		}
		log.Printf("WARNING: could not mark submission %s as failed: %v", sub.ID, err)
		return
	}
	if judged, err := p.submissions.GetByID(failCtx, sub.ID); err == nil {
		p.notifier.SubmissionJudged(failCtx, judged)
	}
}
