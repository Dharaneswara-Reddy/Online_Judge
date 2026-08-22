// Package queue decouples accepting a submission from judging it.
//
// The API publishes a lightweight job and returns immediately; judge
// workers consume jobs at their own pace, turning a burst of requests
// into a steady, worker-controlled stream.
//
// Two lanes exist. Standard practice submissions go to the standard
// lane, while War Room submissions go to a separate high-priority lane
// with its own consumers, so a live race is never stuck behind a backlog
// of background practice traffic.
//
// Fairness: the HLD calls for round-robin per user rather than strict
// FIFO. That is achieved upstream rather than inside the broker — the
// submission service admits at most one in-flight submission per user,
// so a single user can never hold more than one slot in the queue and
// plain FIFO delivery is already fair between users.
package queue

import (
	"context"
	"errors"
)

// Lane identifies which judging pipeline a job belongs to.
type Lane string

const (
	// LaneStandard carries ordinary practice submissions.
	LaneStandard Lane = "standard"
	// LaneWarRoom carries live War Room submissions and is consumed by a
	// dedicated worker pool so races are judged without delay.
	LaneWarRoom Lane = "warroom"
)

// ErrUnavailable is returned when the broker cannot be reached, letting
// callers fall back to judging inline rather than dropping the request.
var ErrUnavailable = errors.New("submission queue unavailable")

// Job is what travels through the broker. It deliberately carries only
// identifiers: the worker loads the submission (and its source code)
// from the database, so the queue stays small and the stored record
// remains the single source of truth.
type Job struct {
	SubmissionID string `json:"submissionId"`
	UserID       string `json:"userId"`
	ProblemID    string `json:"problemId"`
	WarRoomID    string `json:"warRoomId,omitempty"`
}

// LaneFor picks the lane a job belongs to. Anything tagged with a War
// Room goes to the priority lane.
func LaneFor(warRoomID string) Lane {
	if warRoomID != "" {
		return LaneWarRoom
	}
	return LaneStandard
}

// Publisher hands jobs to the broker.
type Publisher interface {
	Publish(ctx context.Context, lane Lane, job Job) error
	Close() error
}

// ErrRetryable marks a job failure that another attempt could survive —
// the database was briefly unreachable, a dependency was restarting.
//
// A handler wraps it (errors.Join, or %w) when it failed *before*
// recording anything on the submission. That distinction decides the
// acknowledgement: an unmarked failure means the handler already wrote a
// terminal status, so redelivering would only repeat work guaranteed to
// fail again, while a marked one means the submission is still sitting
// non-terminal and dropping the message would strand it — holding the
// user's single admission-control slot indefinitely.
//
// Redelivery is bounded, not unlimited; see dispatch in the rabbitmq
// subpackage. The stale-submission reaper is the backstop for a job that
// exhausts its retries.
var ErrRetryable = errors.New("transient failure")

// Handler processes one job. Returning an error tells the consumer the
// job failed; the consumer decides whether to redeliver it.
type Handler func(ctx context.Context, job Job) error

// Consumer pulls jobs from one lane and feeds them to a Handler. Consume
// blocks until the context is cancelled or the connection drops.
type Consumer interface {
	Consume(ctx context.Context, lane Lane, prefetch int, handler Handler) error
	Close() error
}

// --- Synchronous calls ---
//
// Judging is asynchronous, but the playground "Run" button is not: the
// caller is a browser waiting on an HTTP response. The API cannot run the
// code itself in production — it has no access to the Docker daemon, on
// purpose, because it is the only internet-facing process — so it asks a
// worker and waits for the reply.
//
// This stays deliberately untyped at the queue layer. The queue package
// knows how to carry bytes and match a reply to its request; what those
// bytes mean belongs to the caller.

// ErrNoWorker is returned when no worker answered before the deadline.
// The request may have been dropped unexecuted, or executed with nobody
// left to hear the answer — so callers must treat a call as at-most-once
// and never as a completed side effect.
var ErrNoWorker = errors.New("no judge worker answered in time")

// RPCHandler answers one call. The bytes it returns are sent back to the
// waiting caller; an error is reported to the caller as a failed call.
type RPCHandler func(ctx context.Context, payload []byte) ([]byte, error)

// Caller makes a synchronous request and waits for one reply. The
// context deadline bounds the wait.
type Caller interface {
	Call(ctx context.Context, payload []byte) ([]byte, error)
}

// Responder serves synchronous calls until the context is cancelled,
// running up to concurrency handlers at once.
type Responder interface {
	Respond(ctx context.Context, concurrency int, handler RPCHandler) error
}
