// Package playground runs untrusted code on behalf of the "Run" button.
//
// It exists because the API process is not always able to create
// sandboxes itself. In production the API runs in a container with no
// access to the Docker daemon — deliberately, since it is the only
// process reachable from the internet and handing it the daemon socket
// would make an HTTP-layer bug equivalent to host root. Judge workers
// already own a sandbox, so the API asks a worker to run the code and
// waits for the answer.
//
// The unit of work is one *complete* execution: compile, run, and tear
// down. An earlier design proxied the stateful SubmissionSandbox handle
// (Compile, then N Runs, then Close) across the broker, which would have
// meant distributed session state and orphaned containers whenever an
// API instance died mid-request. Keeping the whole lifecycle on the
// worker side means a lost caller costs at most one already-bounded
// execution.
package playground

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/toji339/online-judge/internal/judge"
)

// Mode selects which of the two playground behaviours a request wants.
type Mode string

const (
	// ModeRaw compiles and runs once against supplied stdin and returns
	// the raw streams, without comparing them to anything.
	ModeRaw Mode = "raw"
	// ModeEvaluate runs the code against test cases and returns a verdict.
	ModeEvaluate Mode = "evaluate"
)

// Execution ceilings for the playground. These are ceilings, not
// suggestions: a request may ask for less, never more. Real submissions
// take their limits from the problem definition and never consult these.
const (
	DefaultTimeLimitMs   int64 = 3000
	MaxTimeLimitMs       int64 = 10000
	DefaultMemoryLimitMB int64 = 256
	MaxMemoryLimitMB     int64 = 512

	// MaxTestCases bounds how many container executions one request can
	// trigger; each test case is a separate exec round trip.
	MaxTestCases = 20
	// MaxCodeBytes mirrors the cap the submission service applies, which
	// this path does not go through.
	MaxCodeBytes = 64 * 1024
)

// MaxTotalRuntime bounds one whole playground request — compilation and
// every test case together — independently of the per-case limits.
//
// The per-case limits do not compose into a budget: MaxTestCases at
// MaxTimeLimitMs each is over three minutes of CPU for one click of
// "Run", and on a two-vCPU host a couple of those hold every judging
// slot and drain the instance's CPU credits while they do it. This is
// the ceiling on the whole request, so a caller cannot buy more compute
// by asking for more cases.
//
// It sits below RemoteTimeout on purpose: past that point the API has
// stopped waiting and every further second of compute is spent on an
// answer nobody will read.
const MaxTotalRuntime = 40 * time.Second

// ErrUnknownMode is returned for a request whose Mode is not recognised.
var ErrUnknownMode = errors.New("playground: unknown run mode")

// Request is one playground execution. It is serialised onto the broker,
// so every field must be JSON-safe and self-contained: the worker does
// not look anything up in the database for a playground run.
type Request struct {
	Mode          Mode             `json:"mode"`
	Language      string           `json:"language"`
	Code          string           `json:"code"`
	Stdin         string           `json:"stdin,omitempty"`
	TestCases     []judge.TestCase `json:"testCases,omitempty"`
	TimeLimitMs   int64            `json:"timeLimitMs,omitempty"`
	MemoryLimitMB int64            `json:"memoryLimitMb,omitempty"`
}

// Response carries the outcome back. One struct covers both modes so the
// transport stays a single request/response shape; the fields a mode
// does not use are simply left at their zero values.
type Response struct {
	// Raw-mode fields.
	Stdout    string `json:"stdout,omitempty"`
	Stderr    string `json:"stderr,omitempty"`
	ExitCode  int    `json:"exitCode,omitempty"`
	TimedOut  bool   `json:"timedOut,omitempty"`
	OOMKilled bool   `json:"oomKilled,omitempty"`
	RuntimeMS int64  `json:"runtimeMs,omitempty"`

	// Evaluate-mode fields.
	Verdict      judge.Verdict `json:"verdict,omitempty"`
	MemoryKB     int64         `json:"memoryKb,omitempty"`
	FailedCase   int           `json:"failedCase,omitempty"`
	CompileError string        `json:"compileError,omitempty"`

	// CompileFailed distinguishes "the program did not compile" from "the
	// program compiled and exited non-zero". Raw mode reports a compile
	// failure as a normal response carrying stderr, not as an error.
	CompileFailed bool `json:"compileFailed,omitempty"`
}

// Runner executes a playground request somewhere — in this process, or
// on a judge worker across the broker.
type Runner interface {
	Run(ctx context.Context, req Request) (Response, error)
}

// ClampLimit keeps a client-proposed limit inside the allowed range,
// falling back to the default when it is absent or nonsensical.
func ClampLimit(requested, fallback, max int64) int64 {
	if requested <= 0 {
		return fallback
	}
	if requested > max {
		return max
	}
	return requested
}

// limitsFor turns a request's proposed limits into enforced ones.
//
// This is applied by whichever process actually creates the container,
// not only by the HTTP handler. A playground request arrives over the
// broker, and a worker must not trust a message to have been validated
// by whoever published it: an unclamped memory ceiling larger than
// physical memory does not bind at all, and the program then allocates
// until the host OOM killer fires.
func limitsFor(req Request) judge.Limits {
	return judge.Limits{
		TimeLimit:     time.Duration(ClampLimit(req.TimeLimitMs, DefaultTimeLimitMs, MaxTimeLimitMs)) * time.Millisecond,
		MemoryLimitMB: ClampLimit(req.MemoryLimitMB, DefaultMemoryLimitMB, MaxMemoryLimitMB),
	}
}

// LocalRunner executes requests in this process using a real sandbox.
// Judge workers use it directly; the API uses it only in development,
// where the process can reach the Docker daemon itself.
type LocalRunner struct {
	sandbox judge.Sandbox
	engine  *judge.Judge
	// budget is the wall-clock ceiling on one request. It is a field
	// rather than a bare constant only so tests can shrink it.
	budget time.Duration
}

// NewLocalRunner builds a runner over a sandbox implementation.
func NewLocalRunner(sandbox judge.Sandbox) *LocalRunner {
	return &LocalRunner{
		sandbox: sandbox,
		engine:  judge.NewJudge(sandbox),
		budget:  MaxTotalRuntime,
	}
}

// Run executes one playground request to completion, or until the
// overall budget runs out.
func (r *LocalRunner) Run(ctx context.Context, req Request) (Response, error) {
	if len(req.Code) > MaxCodeBytes {
		return Response{}, fmt.Errorf("playground: code exceeds %d bytes", MaxCodeBytes)
	}
	if len(req.TestCases) > MaxTestCases {
		return Response{}, fmt.Errorf("playground: at most %d test cases may be run at once", MaxTestCases)
	}

	// The whole request runs under one deadline. Enforced here rather
	// than by the caller for the same reason limitsFor is applied here:
	// the process that creates containers must not depend on whoever
	// published the message having bounded anything. If the caller's own
	// context is shorter, it still wins — this only ever shortens.
	ctx, cancel := context.WithTimeout(ctx, r.budget)
	defer cancel()

	switch req.Mode {
	case ModeEvaluate:
		return r.evaluate(ctx, req)
	case ModeRaw:
		return r.raw(ctx, req)
	default:
		return Response{}, ErrUnknownMode
	}
}

func (r *LocalRunner) evaluate(ctx context.Context, req Request) (Response, error) {
	testCases := req.TestCases
	if len(testCases) == 0 {
		return Response{}, errors.New("playground: evaluate needs at least one test case")
	}

	result, err := r.engine.Evaluate(ctx, req.Language, req.Code, testCases, limitsFor(req))
	if err != nil {
		return Response{}, err
	}
	return Response{
		Verdict:      result.Verdict,
		RuntimeMS:    result.RuntimeMS,
		MemoryKB:     result.MemoryKB,
		FailedCase:   result.FailedCase,
		CompileError: result.CompileError,
	}, nil
}

func (r *LocalRunner) raw(ctx context.Context, req Request) (Response, error) {
	sub, err := r.sandbox.NewSubmission(ctx, req.Language, req.Code, limitsFor(req))
	if err != nil {
		return Response{}, fmt.Errorf("playground: create sandbox: %w", err)
	}
	// Close takes its own context: by the time a run times out the caller's
	// context is already cancelled, and cleanup still has to happen or the
	// container leaks.
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		_ = sub.Close(closeCtx)
	}()

	compiled, err := sub.Compile(ctx)
	if err != nil {
		return Response{}, fmt.Errorf("playground: compile: %w", err)
	}
	if compiled.ExitCode != 0 {
		// Not an error: the user wants to see why their code did not build.
		return Response{Stderr: compiled.Stderr, ExitCode: compiled.ExitCode, CompileFailed: true}, nil
	}

	run, err := sub.Run(ctx, req.Stdin)
	if err != nil {
		return Response{}, fmt.Errorf("playground: run: %w", err)
	}
	return Response{
		Stdout:    run.Stdout,
		Stderr:    run.Stderr,
		ExitCode:  run.ExitCode,
		TimedOut:  run.TimedOut,
		OOMKilled: run.OOMKilled,
		RuntimeMS: run.RuntimeMS,
	}, nil
}
