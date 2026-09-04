package casegen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/toji339/online-judge/internal/assist"
	"github.com/toji339/online-judge/internal/judge"
	"github.com/toji339/online-judge/internal/playground"
)

// Generator turns a problem plus a reference solution into reviewed
// proposals. It holds two collaborators and no state: a Provider to ask
// for inputs, and a Runner to find out what those inputs actually
// produce.
//
// Neither is required. A nil Provider disables the feature outright; a
// nil Runner leaves it working but unable to verify, which is a
// deployment without Docker and without a broker — the same
// configuration the playground already degrades to.
type Generator struct {
	provider assist.Provider
	runner   playground.Runner

	// Limits are fields rather than bare constants so a test can shrink
	// them without a real sandbox anywhere in sight.
	maxProposals int
	maxCodeBytes int
	timeLimitMS  int64
	memoryMB     int64
}

// NewGenerator wires a generator over a model provider and a sandbox
// runner. Both may be nil; see the type comment for what each absence
// costs.
func NewGenerator(p assist.Provider, r playground.Runner) *Generator {
	return &Generator{
		provider:     p,
		runner:       r,
		maxProposals: MaxProposals,
		maxCodeBytes: DefaultMaxCodeBytes,
		timeLimitMS:  verifyTimeLimitMS,
		memoryMB:     verifyMemoryLimitMB,
	}
}

// Enabled reports whether a provider is wired.
//
// The nil-receiver case is deliberate, as in assist.Service: the wiring
// may hold a *Generator that was never constructed, and answering
// "disabled" rather than panicking keeps the nil checks out of every
// call site.
func (g *Generator) Enabled() bool {
	return g != nil && g.provider != nil
}

// Generate proposes test cases and verifies each one.
//
// The order is the design. The model is asked for inputs and rationales
// only; the reply is parsed strictly and capped; and then, for each
// surviving input, the admin's reference solution is executed on it and
// *its* stdout becomes the expected output. Nothing the model said about
// an output is read. Nothing is written anywhere — the caller gets
// proposals, and a human decides which of them become test cases.
//
// An error is returned only when nothing useful could be produced at
// all: no provider, no reply, or a reply that was not the requested
// shape. A verification failure is not an error, it is a property of one
// proposal, because the batch is worth returning without it.
func (g *Generator) Generate(ctx context.Context, req Request) (Result, error) {
	// Steps to follow while generating adversarial cases
	// ==================================================

	// 1. Refuse early when the feature is not configured.
	if !g.Enabled() {
		return Result{}, assist.ErrDisabled
	}

	// 2. Decide how many cases will actually be kept, and ask for that
	//    number rather than for whatever the client typed.
	want := g.clampCount(req.Count)

	// 3. Ask the model. Its answer is a proposal, not a result.
	raw, err := g.provider.Complete(ctx, buildPrompt(req, want, g.maxCodeBytes))
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", assist.ErrUnavailable, err)
	}

	// 4. Parse strictly and cap, so a runaway reply cannot spawn
	//    hundreds of sandbox runs.
	reply, err := parseReply(raw)
	if err != nil {
		return Result{}, err
	}
	if len(reply.Cases) > want {
		reply.Cases = reply.Cases[:want]
	}

	// 5. Execute the reference solution once per input. This is where
	//    every expected output comes from.
	result := Result{
		Proposals:   make([]Proposal, 0, len(reply.Cases)),
		Ambiguities: cleanAmbiguities(reply.Ambiguities),
	}

	// A reference solution that does not compile fails identically for
	// every input, so the first compile failure ends the runs and its
	// message is reported against the rest. Continuing would cost one
	// container per proposal to learn the same fact again.
	var compileErr string

	for _, mc := range reply.Cases {
		p := Proposal{
			Input:     normaliseInput(mc.Input),
			Rationale: strings.TrimSpace(mc.Rationale),
		}
		if compileErr != "" {
			p.Error = compileErr
			result.Proposals = append(result.Proposals, p)
			continue
		}

		expected, verr := g.verify(ctx, req, p.Input)
		switch {
		case verr == nil:
			p.Verified = true
			p.ExpectedOutput = expected
		default:
			// ExpectedOutput is left empty on purpose: a proposal nobody
			// stands behind must not carry something that looks like an
			// answer.
			p.Error = verr.Error()
			if errors.Is(verr, errReferenceCompile) {
				compileErr = p.Error
			}
		}
		result.Proposals = append(result.Proposals, p)
	}

	return result, nil
}

// clampCount keeps the requested case count inside the package bounds.
func (g *Generator) clampCount(requested int) int {
	max := g.maxProposals
	if max <= 0 {
		max = MaxProposals
	}
	switch {
	case requested <= 0:
		return min(DefaultCount, max)
	case requested > max:
		return max
	default:
		return requested
	}
}

// errReferenceCompile marks the one verification failure that says
// something about the reference solution rather than about the input.
var errReferenceCompile = errors.New("the reference solution did not compile")

// verify runs the reference solution on one generated input and returns
// the expected output it printed.
//
// Every way this can fail produces an error rather than a partial
// answer. An expected output taken from a run that timed out, was
// OOM-killed, crashed, or printed nothing is not an expected output; it
// is a guess with a container's worth of ceremony around it.
func (g *Generator) verify(ctx context.Context, req Request, input string) (string, error) {
	if g.runner == nil {
		return "", errors.New("no sandbox is available on this deployment, so this input was not executed")
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("verification was cancelled: %w", err)
	}
	if len(input) > MaxInputBytes {
		return "", fmt.Errorf("the generated input is %d bytes, over the %d byte limit, so it was not executed",
			len(input), MaxInputBytes)
	}

	resp, err := g.runner.Run(ctx, playground.Request{
		Mode:     playground.ModeRaw,
		Language: req.Language,
		Code:     req.ReferenceSolution,
		Stdin:    input,
		// Clamped here as well as inside the runner: the ceilings are
		// this package's promise about how much compute one admin click
		// can buy, and that promise should not depend on which runner
		// implementation happens to be wired.
		TimeLimitMs:   playground.ClampLimit(g.timeLimitMS, playground.DefaultTimeLimitMs, playground.MaxTimeLimitMs),
		MemoryLimitMB: playground.ClampLimit(g.memoryMB, playground.DefaultMemoryLimitMB, playground.MaxMemoryLimitMB),
	})
	if err != nil {
		return "", fmt.Errorf("the reference solution could not be run: %v", err)
	}

	switch {
	case resp.CompileFailed:
		return "", fmt.Errorf("%w: %s", errReferenceCompile, firstLine(resp.Stderr))
	case resp.TimedOut:
		return "", errors.New("the reference solution timed out on this input")
	case resp.OOMKilled:
		return "", errors.New("the reference solution ran out of memory on this input")
	case resp.ExitCode != 0:
		return "", fmt.Errorf("the reference solution exited with code %d: %s",
			resp.ExitCode, firstLine(resp.Stderr))
	}

	// Normalised the way the judge itself compares, so a case accepted
	// from this list cannot later fail on trailing whitespace.
	expected := judge.NormalizeOutput(resp.Stdout)
	if strings.TrimSpace(expected) == "" {
		return "", errors.New("the reference solution printed nothing for this input")
	}
	return expected, nil
}

// modelReply is the shape the model is asked for.
type modelReply struct {
	Cases       []modelCase `json:"cases"`
	Ambiguities []string    `json:"ambiguities"`
}

// modelCase is one proposed input.
//
// ExpectedOutput is declared and never read. That is the point: the
// model is told not to send one, some replies will send one anyway, and
// naming the field here makes the discard explicit rather than leaving
// it to a silently ignored key. The expected output of every proposal
// comes from verify and from nowhere else.
type modelCase struct {
	Input          string `json:"input"`
	Rationale      string `json:"rationale"`
	ExpectedOutput string `json:"expectedOutput"`
}

// parseReply decodes the model's answer, strictly.
//
// The one liberty taken is stripping a markdown fence, because models
// wrap JSON in one regardless of instructions and the wrapper carries no
// meaning. Everything inside it must be a single well-formed object of
// the requested shape with at least one usable input; anything else is
// discarded whole rather than mined for the parts that happened to
// parse. A partially understood reply is how an unverified case would
// find its way into a problem.
func parseReply(raw string) (modelReply, error) {
	text := stripJSONFence(strings.TrimSpace(raw))
	if text == "" {
		return modelReply{}, fmt.Errorf("%w: empty completion", ErrBadResponse)
	}

	dec := json.NewDecoder(strings.NewReader(text))
	var reply modelReply
	if err := dec.Decode(&reply); err != nil {
		return modelReply{}, fmt.Errorf("%w: %v", ErrBadResponse, err)
	}
	// A second value after the object means the reply was not one JSON
	// document, and whatever we decoded was a fragment of something else.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return modelReply{}, fmt.Errorf("%w: trailing content after the JSON object", ErrBadResponse)
	}

	if len(reply.Cases) == 0 {
		return modelReply{}, fmt.Errorf("%w: no cases in the reply", ErrBadResponse)
	}
	for i, c := range reply.Cases {
		if strings.TrimSpace(c.Input) == "" {
			return modelReply{}, fmt.Errorf("%w: case %d has no input", ErrBadResponse, i+1)
		}
	}

	return reply, nil
}

// stripJSONFence removes a markdown code fence, and any chat around it,
// from a reply that is otherwise a JSON object.
//
// It looks for the fence rather than for the braces: trimming to the
// first '{' and last '}' would silently rescue a reply containing two
// objects, which is one of the shapes parseReply exists to reject.
func stripJSONFence(s string) string {
	for _, marker := range []string{"```", "~~~"} {
		open := strings.Index(s, marker)
		if open < 0 {
			continue
		}
		// Drop the opening fence and its language tag, which is the
		// remainder of that line.
		rest := s[open+len(marker):]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[nl+1:]
		}
		if close := strings.LastIndex(rest, marker); close >= 0 {
			rest = rest[:close]
		}
		return strings.TrimSpace(rest)
	}
	return s
}

// cleanAmbiguities tidies the sentence list without editing any
// sentence: blanks are dropped, the list is capped, and the result is
// never nil so the JSON is an array rather than null.
func cleanAmbiguities(raw []string) []string {
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
		if len(out) == MaxAmbiguities {
			break
		}
	}
	return out
}

// normaliseInput prepares a generated input to be fed to a program on
// stdin.
//
// The only change is a terminating newline. A program that reads a line
// from an unterminated stdin blocks until EOF, which turns a perfectly
// good test case into a timeout, and every real test case in the
// database ends in one anyway. The input is stored in the form that was
// actually executed, so what the admin reviews is what produced the
// expected output.
func normaliseInput(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

// firstLine reduces a stderr dump to something that fits in a proposal's
// Error field. The rest of a traceback is noise for the admin, who has
// the reference solution open in the next pane.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "no error output"
	}
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		s = s[:nl]
	}
	const maxLen = 200
	if len(s) > maxLen {
		s, _ = truncateBytes(s, maxLen)
	}
	return s
}
