// Package casegen proposes adversarial test cases for a problem and
// then proves each one by executing the admin's reference solution
// against it.
//
// It exists because of a real defect. CodeArena shipped a Two Sum
// problem whose expected output admitted two correct answers, so the
// canonical hash-map solution was judged wrong. Nobody caught it,
// because a human wrote the expected output by hand and nothing checked
// it. This package is the thing that would have.
//
// Two rules follow from that, and everything else here is machinery for
// keeping them:
//
//   - The model proposes inputs. It never supplies expected outputs. A
//     hallucinated expected output is precisely the defect this package
//     exists to prevent, so the expected output of every proposal is the
//     stdout of the admin's own reference solution, run on that input
//     through the ordinary playground sandbox. A proposal whose run
//     failed, timed out, or printed nothing comes back marked failed and
//     is never presented as usable.
//
//   - Nothing is written to the database. Generate returns proposals for
//     a human to read and accept. An admin reviewing ten candidate cases
//     is a low-stakes operation; an admin discovering that a model
//     silently added ten cases to a live problem is not.
//
// The second job is ambiguity detection. The Two Sum defect was not a
// wrong expected output so much as a problem statement that permitted
// two, so the model is also asked to name inputs where more than one
// output would be correct under exact-match judging. Those come back as
// plain sentences for a human to act on — the package neither resolves
// them nor lets them block a proposal.
//
// Like internal/assist, this is an optional dependency: a nil provider
// makes every call return assist.ErrDisabled, and a nil runner still
// produces proposals, just unverified ones. Neither case is an error the
// deployment has to fix.
package casegen

import (
	"errors"

	"github.com/toji339/online-judge/internal/assist"
	"github.com/toji339/online-judge/internal/playground"
)

// Package limits. Every one of them bounds work that costs a container
// or a token, so none of them is configurable from a request body.
const (
	// DefaultCount is how many cases a request that does not say gets.
	DefaultCount = 5

	// MaxProposals is the hard ceiling on proposals from one call, and
	// therefore on sandbox executions from one call. It matches
	// playground.MaxTestCases, which bounds the same resource for the
	// same reason: a reply claiming forty cases must not become forty
	// containers.
	MaxProposals = playground.MaxTestCases

	// MaxInputBytes is the largest generated input that will be run. A
	// model asked for a stress case will occasionally try to write the
	// stress case out in full; that is a proposal to reject, not an
	// input to feed a container.
	MaxInputBytes = 64 * 1024

	// DefaultMaxCodeBytes caps how much of the reference solution
	// reaches the model. It matches assist.DefaultMaxCodeBytes and the
	// judge's own submission cap, so an ordinary solution is never cut.
	DefaultMaxCodeBytes = 64 * 1024

	// MaxAmbiguities bounds the sentence list. Past a handful the list
	// has stopped being a review aid.
	MaxAmbiguities = 10
)

// Verification limits. These are deliberately tighter than the
// playground's own defaults: a reference solution is expected to be
// correct and quick on inputs the model just invented, and one that is
// neither should fail fast rather than hold a sandbox slot.
const (
	verifyTimeLimitMS   int64 = 3000
	verifyMemoryLimitMB int64 = 256
)

// ErrBadResponse means the model replied with something that is not the
// JSON shape it was asked for.
//
// It is a distinct sentinel from assist.ErrUnavailable because the two
// call for different responses: unavailable is worth retrying, and a
// malformed reply usually is not. The reply is discarded whole — no
// attempt is made to salvage the cases that happened to parse, since a
// half-understood reply is exactly how an unverified case would get in.
var ErrBadResponse = errors.New("casegen: model reply was not the requested JSON")

// Case is a test case that already exists on the problem. It is sent to
// the model so it does not re-propose what the problem already has.
type Case struct {
	Input          string
	ExpectedOutput string
}

// Request is one generation. Everything the model may see arrives here;
// the package looks nothing up for itself.
type Request struct {
	// Problem is the public description of the problem. Every field is
	// already on the student's screen.
	Problem assist.ProblemContext
	// ExistingCases are the problem's current cases, hidden ones
	// included — this endpoint is admin-only and the admin can already
	// read them.
	ExistingCases []Case
	// ReferenceSolution is the admin's own correct solution. It is the
	// source of every expected output, and it is fenced in the prompt as
	// untrusted data.
	ReferenceSolution string
	// Language is the language ReferenceSolution is written in.
	Language string
	// Count is how many cases to propose. Zero means DefaultCount;
	// anything above MaxProposals is clamped.
	Count int
}

// Proposal is one candidate test case.
//
// Verified is the field that matters. It is true only when the reference
// solution actually ran on this input and printed something, and
// ExpectedOutput is empty whenever it is false — an unverified proposal
// carries no output at all rather than an output nobody stands behind.
type Proposal struct {
	Input          string `json:"input"`
	ExpectedOutput string `json:"expectedOutput"`
	Rationale      string `json:"rationale"`
	Verified       bool   `json:"verified"`
	Error          string `json:"error,omitempty"`
}

// Result is what one generation produced. Both slices are always
// non-nil, so the admin UI can iterate them without a guard.
type Result struct {
	Proposals   []Proposal `json:"cases"`
	Ambiguities []string   `json:"ambiguities"`
}
