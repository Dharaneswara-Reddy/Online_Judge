package casegen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/assist"
	"github.com/toji339/online-judge/internal/playground"
)

// These tests are about one property above all others: the expected
// output of a generated case comes from executing the admin's reference
// solution, never from the model. Several of them therefore hand the
// model and the runner *different* answers and check which one survives.
//
// Nothing here opens a socket or starts a container. Both boundaries are
// one-method interfaces precisely so that a fake is three lines.

// fakeProvider answers with a canned string and records what it was
// asked, so the prompt tests can read the fences back.
type fakeProvider struct {
	reply   string
	err     error
	prompts []assist.Prompt
}

func (f *fakeProvider) Complete(_ context.Context, p assist.Prompt) (string, error) {
	f.prompts = append(f.prompts, p)
	return f.reply, f.err
}

// fakeRunner stands in for the sandbox. respond may be nil, in which
// case every run succeeds with the same stdout.
type fakeRunner struct {
	requests []playground.Request
	respond  func(playground.Request) (playground.Response, error)
}

func (f *fakeRunner) Run(_ context.Context, req playground.Request) (playground.Response, error) {
	f.requests = append(f.requests, req)
	if f.respond != nil {
		return f.respond(req)
	}
	return playground.Response{Stdout: "runner-output\n"}, nil
}

func newRequest(count int) Request {
	return Request{
		Problem: assist.ProblemContext{
			Title:         "Two Sum",
			Statement:     "Return the indices of the two numbers adding to the target.",
			Difficulty:    "easy",
			Tags:          []string{"array", "hash-map"},
			TimeLimitMS:   1000,
			MemoryLimitMB: 256,
		},
		ReferenceSolution: "print(0)",
		Language:          "python",
		Count:             count,
	}
}

// replyWith builds a well-formed model reply carrying n proposals.
func replyWith(n int, ambiguities ...string) string {
	cases := make([]map[string]string, 0, n)
	for i := 0; i < n; i++ {
		cases = append(cases, map[string]string{
			"input":     fmt.Sprintf("case-%d\n", i),
			"rationale": fmt.Sprintf("reason %d", i),
		})
	}
	if ambiguities == nil {
		ambiguities = []string{}
	}
	raw, err := json.Marshal(map[string]any{"cases": cases, "ambiguities": ambiguities})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func TestGeneratorWithoutAProviderIsDisabled(t *testing.T) {
	g := NewGenerator(nil, &fakeRunner{})

	assert.False(t, g.Enabled())

	_, err := g.Generate(context.Background(), newRequest(3))
	assert.ErrorIs(t, err, assist.ErrDisabled)
}

// A nil *Generator answers "disabled" rather than panicking, so the
// wiring never has to nil-check before asking.
func TestNilGeneratorIsDisabled(t *testing.T) {
	var g *Generator
	assert.False(t, g.Enabled())
}

func TestGeneratorWithAProviderIsEnabled(t *testing.T) {
	assert.True(t, NewGenerator(&fakeProvider{}, nil).Enabled())
}

func TestProviderFailureIsUnavailable(t *testing.T) {
	g := NewGenerator(&fakeProvider{err: errors.New("502 from upstream")}, &fakeRunner{})

	_, err := g.Generate(context.Background(), newRequest(3))
	assert.ErrorIs(t, err, assist.ErrUnavailable)
}

// Strict parsing. A reply that is not the shape we asked for is thrown
// away whole — no salvaging the fragments that happened to parse, since
// a half-understood reply is exactly how a hallucinated case gets in.
func TestMalformedRepliesAreRejectedWholesale(t *testing.T) {
	cases := map[string]string{
		"prose":            "Sure! Here are some good adversarial cases to try.",
		"truncated json":   `{"cases": [{"input": "1 2`,
		"wrong shape":      `{"cases": "three of them"}`,
		"wrong item shape": `{"cases": [{"input": 12}]}`,
		"top-level array":  `[{"input": "1 2"}]`,
		"trailing content": `{"cases":[{"input":"1\n"}]} {"cases":[]}`,
		"no cases":         `{"cases": [], "ambiguities": ["ties are possible"]}`,
		"blank input":      `{"cases": [{"input": "   ", "rationale": "empty"}]}`,
		"empty":            "",
	}

	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			runner := &fakeRunner{}
			g := NewGenerator(&fakeProvider{reply: reply}, runner)

			_, err := g.Generate(context.Background(), newRequest(3))

			assert.ErrorIs(t, err, ErrBadResponse)
			assert.Empty(t, runner.requests, "a rejected reply must never reach the sandbox")
		})
	}
}

// Models fence their JSON however they like. Stripping the wrapper is
// the one liberty taken with the reply; everything inside it is still
// parsed strictly.
func TestFencedJSONIsAccepted(t *testing.T) {
	for name, reply := range map[string]string{
		"json fence":  "```json\n" + replyWith(2) + "\n```",
		"bare fence":  "```\n" + replyWith(2) + "\n```",
		"tilde fence": "~~~json\n" + replyWith(2) + "\n~~~",
		"chatty":      "Here you go:\n\n```json\n" + replyWith(2) + "\n```\n",
	} {
		t.Run(name, func(t *testing.T) {
			g := NewGenerator(&fakeProvider{reply: reply}, &fakeRunner{})

			result, err := g.Generate(context.Background(), newRequest(5))

			require.NoError(t, err)
			assert.Len(t, result.Proposals, 2)
		})
	}
}

// The cap is what stops a runaway reply from spawning a container per
// hallucinated case.
func TestProposalsAreCappedAtTheRequestedCount(t *testing.T) {
	runner := &fakeRunner{}
	g := NewGenerator(&fakeProvider{reply: replyWith(9)}, runner)

	result, err := g.Generate(context.Background(), newRequest(2))

	require.NoError(t, err)
	assert.Len(t, result.Proposals, 2)
	assert.Len(t, runner.requests, 2, "only the kept proposals may be executed")
}

func TestProposalsAreCappedAtThePackageMaximum(t *testing.T) {
	runner := &fakeRunner{}
	g := NewGenerator(&fakeProvider{reply: replyWith(MaxProposals + 7)}, runner)

	result, err := g.Generate(context.Background(), newRequest(500))

	require.NoError(t, err)
	assert.Len(t, result.Proposals, MaxProposals)
	assert.Len(t, runner.requests, MaxProposals)
}

func TestZeroCountFallsBackToTheDefault(t *testing.T) {
	g := NewGenerator(&fakeProvider{reply: replyWith(MaxProposals)}, &fakeRunner{})

	result, err := g.Generate(context.Background(), newRequest(0))

	require.NoError(t, err)
	assert.Len(t, result.Proposals, DefaultCount)
}

// The point of the whole package. The model is given every chance to
// supply an expected output and it is discarded unread.
func TestExpectedOutputComesFromTheRunnerNotTheModel(t *testing.T) {
	reply := `{"cases":[{"input":"2 2\n","expectedOutput":"MODEL-HALLUCINATION","rationale":"ties"}],
	           "ambiguities":[]}`
	runner := &fakeRunner{respond: func(playground.Request) (playground.Response, error) {
		return playground.Response{Stdout: "EXECUTED-TRUTH\n"}, nil
	}}
	g := NewGenerator(&fakeProvider{reply: reply}, runner)

	result, err := g.Generate(context.Background(), newRequest(1))

	require.NoError(t, err)
	require.Len(t, result.Proposals, 1)
	assert.Equal(t, "EXECUTED-TRUTH", result.Proposals[0].ExpectedOutput)
	assert.True(t, result.Proposals[0].Verified)
	assert.Empty(t, result.Proposals[0].Error)
	assert.NotContains(t, result.Proposals[0].ExpectedOutput, "MODEL")
}

// The verification run is what the sandbox actually sees: the admin's
// code, the model's input on stdin, and bounded limits.
func TestVerificationRunsTheReferenceSolutionOnTheGeneratedInput(t *testing.T) {
	runner := &fakeRunner{}
	req := newRequest(1)
	req.ReferenceSolution = "import sys; print(sum(map(int, sys.stdin.read().split())))"
	req.Language = "python"
	g := NewGenerator(&fakeProvider{reply: `{"cases":[{"input":"1 2 3","rationale":"small"}]}`}, runner)

	_, err := g.Generate(context.Background(), req)
	require.NoError(t, err)

	require.Len(t, runner.requests, 1)
	run := runner.requests[0]
	assert.Equal(t, playground.ModeRaw, run.Mode)
	assert.Equal(t, req.ReferenceSolution, run.Code)
	assert.Equal(t, "python", run.Language)
	assert.Equal(t, "1 2 3\n", run.Stdin, "stdin is newline-terminated so line readers do not block")
	assert.Positive(t, run.TimeLimitMs)
	assert.LessOrEqual(t, run.TimeLimitMs, playground.MaxTimeLimitMs)
	assert.Positive(t, run.MemoryLimitMB)
	assert.LessOrEqual(t, run.MemoryLimitMB, playground.MaxMemoryLimitMB)
}

// One bad input must not cost the admin the other nine.
func TestOneFailedVerificationDoesNotKillTheBatch(t *testing.T) {
	runner := &fakeRunner{respond: func(req playground.Request) (playground.Response, error) {
		if strings.HasPrefix(req.Stdin, "case-1") {
			return playground.Response{}, errors.New("sandbox exploded")
		}
		return playground.Response{Stdout: "fine\n"}, nil
	}}
	g := NewGenerator(&fakeProvider{reply: replyWith(3)}, runner)

	result, err := g.Generate(context.Background(), newRequest(3))

	require.NoError(t, err)
	require.Len(t, result.Proposals, 3)
	assert.Len(t, runner.requests, 3, "the batch continues past the failure")

	assert.True(t, result.Proposals[0].Verified)
	assert.False(t, result.Proposals[1].Verified)
	assert.NotEmpty(t, result.Proposals[1].Error)
	assert.Empty(t, result.Proposals[1].ExpectedOutput, "a failed run yields no usable expected output")
	assert.True(t, result.Proposals[2].Verified)
}

// Every way a run can fail to produce a trustworthy answer.
func TestUnusableRunsAreMarkedRatherThanAccepted(t *testing.T) {
	for name, response := range map[string]playground.Response{
		"empty output":    {Stdout: "   \n"},
		"timed out":       {Stdout: "partial", TimedOut: true},
		"oom killed":      {Stdout: "partial", OOMKilled: true},
		"non-zero exit":   {Stdout: "partial", ExitCode: 1, Stderr: "Traceback"},
		"compile failure": {CompileFailed: true, Stderr: "SyntaxError"},
	} {
		t.Run(name, func(t *testing.T) {
			runner := &fakeRunner{respond: func(playground.Request) (playground.Response, error) {
				return response, nil
			}}
			g := NewGenerator(&fakeProvider{reply: replyWith(1)}, runner)

			result, err := g.Generate(context.Background(), newRequest(1))

			require.NoError(t, err)
			require.Len(t, result.Proposals, 1)
			assert.False(t, result.Proposals[0].Verified)
			assert.NotEmpty(t, result.Proposals[0].Error)
			assert.Empty(t, result.Proposals[0].ExpectedOutput)
		})
	}
}

// A reference solution that does not compile fails identically for every
// input, so the remaining containers are never started.
func TestACompileFailureStopsFurtherRuns(t *testing.T) {
	runner := &fakeRunner{respond: func(playground.Request) (playground.Response, error) {
		return playground.Response{CompileFailed: true, Stderr: "SyntaxError: bad"}, nil
	}}
	g := NewGenerator(&fakeProvider{reply: replyWith(4)}, runner)

	result, err := g.Generate(context.Background(), newRequest(4))

	require.NoError(t, err)
	require.Len(t, result.Proposals, 4)
	assert.Len(t, runner.requests, 1, "the reference solution is compiled once, not four times")
	for _, p := range result.Proposals {
		assert.False(t, p.Verified)
		assert.Contains(t, p.Error, "compile")
	}
}

// A nil runner is a legal deployment — no Docker, no broker. The
// endpoint still answers; the proposals simply arrive unverified.
func TestNilRunnerDegradesToUnverifiedProposals(t *testing.T) {
	g := NewGenerator(&fakeProvider{reply: replyWith(2, "the answer may be printed in any order")}, nil)

	result, err := g.Generate(context.Background(), newRequest(2))

	require.NoError(t, err)
	require.Len(t, result.Proposals, 2)
	for _, p := range result.Proposals {
		assert.False(t, p.Verified)
		assert.Empty(t, p.ExpectedOutput)
		assert.NotEmpty(t, p.Error)
	}
	assert.Equal(t, []string{"the answer may be printed in any order"}, result.Ambiguities)
}

func TestAmbiguitiesAreCarriedThrough(t *testing.T) {
	reply := `{"cases":[{"input":"2\n1 1\n","rationale":"tie"}],
	           "ambiguities":["Two pairs sum to the target, so either index pair is correct.",
	                          "   ",
	                          "The output order is unspecified."]}`
	g := NewGenerator(&fakeProvider{reply: reply}, &fakeRunner{})

	result, err := g.Generate(context.Background(), newRequest(1))

	require.NoError(t, err)
	assert.Equal(t, []string{
		"Two pairs sum to the target, so either index pair is correct.",
		"The output order is unspecified.",
	}, result.Ambiguities, "blank sentences are dropped")
}

// A result is always JSON-shaped as two arrays, never two nulls: the
// admin UI iterates both without a guard.
func TestEmptyCollectionsMarshalAsArrays(t *testing.T) {
	g := NewGenerator(&fakeProvider{reply: `{"cases":[{"input":"1\n","rationale":"r"}]}`}, &fakeRunner{})

	result, err := g.Generate(context.Background(), newRequest(1))
	require.NoError(t, err)

	raw, err := json.Marshal(result)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"ambiguities":[]`)
}

// The expected output is stored in the form the judge will compare
// against, so an accepted proposal cannot fail on trailing whitespace.
func TestExpectedOutputIsNormalisedTheWayTheJudgeCompares(t *testing.T) {
	runner := &fakeRunner{respond: func(playground.Request) (playground.Response, error) {
		return playground.Response{Stdout: "1 2   \n3 4\n\n\n"}, nil
	}}
	g := NewGenerator(&fakeProvider{reply: replyWith(1)}, runner)

	result, err := g.Generate(context.Background(), newRequest(1))

	require.NoError(t, err)
	assert.Equal(t, "1 2\n3 4", result.Proposals[0].ExpectedOutput)
}

// An absurdly large input is refused before it reaches a container,
// without costing the rest of the batch.
func TestAnOversizedInputIsRejectedWithoutRunning(t *testing.T) {
	huge := strings.Repeat("9", MaxInputBytes+1)
	reply := fmt.Sprintf(`{"cases":[{"input":%q,"rationale":"huge"},{"input":"2\n","rationale":"fine"}]}`, huge)
	runner := &fakeRunner{}
	g := NewGenerator(&fakeProvider{reply: reply}, runner)

	result, err := g.Generate(context.Background(), newRequest(2))

	require.NoError(t, err)
	require.Len(t, result.Proposals, 2)
	assert.False(t, result.Proposals[0].Verified)
	assert.NotEmpty(t, result.Proposals[0].Error)
	assert.True(t, result.Proposals[1].Verified)
	assert.Len(t, runner.requests, 1)
}

// Nothing in this package writes anywhere. The only collaborators are
// the two interfaces, and the runner is only ever asked to run — never
// to evaluate against stored cases.
func TestGenerateNeverAsksForEvaluateMode(t *testing.T) {
	runner := &fakeRunner{}
	g := NewGenerator(&fakeProvider{reply: replyWith(3)}, runner)

	_, err := g.Generate(context.Background(), newRequest(3))
	require.NoError(t, err)

	for _, run := range runner.requests {
		assert.Equal(t, playground.ModeRaw, run.Mode)
		assert.Empty(t, run.TestCases)
	}
}
