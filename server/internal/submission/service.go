package submission

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/toji339/online-judge/internal/judge"
)

// maxPendingPerUser is the admission-control limit described in the HLD:
// one in-flight submission per user.
//
// The database enforces it, not this constant. A unique partial index on
// user_id over non-terminal submissions makes an over-limit insert fail
// with a duplicate key, which is correct across any number of API
// processes — a count-then-insert here would race, since two concurrent
// requests both read "none in flight" before either writes.
//
// Raising this above 1 therefore needs more than editing the constant:
// a uniqueness constraint cannot express "at most N", so it would need a
// reservation document per slot, or a counter updated conditionally.
const maxPendingPerUser = 1

// maxCodeBytes caps the source we accept, keeping oversized payloads out
// of both the queue and the database.
const maxCodeBytes = 64 * 1024

// Service holds the submission business rules and is the only type the
// controllers and the judge worker talk to.
type Service struct {
	repo Repository
}

// NewService creates a Service backed by the given Repository.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// CreateInput is everything needed to record a new submission attempt.
// The problem slug and title are denormalised onto the submission so the
// history page can render without a second lookup per row.
type CreateInput struct {
	UserID       string
	ProblemID    string
	ProblemSlug  string
	ProblemTitle string
	WarRoomID    string
	Language     string
	Code         string
}

// Create validates the input, enforces per-user admission control, and
// stores the submission in the pending state ready for the queue.
//
// It returns ErrTooManyPending when the user already has work in flight.
func (s *Service) Create(ctx context.Context, input CreateInput) (*Submission, error) {
	// Steps to follow while creating a submission
	// =============================================

	// 1. Validate the input before touching the database
	if err := validateCreateInput(input); err != nil {
		return nil, err
	}

	// 2. Admission control — a cheap pre-check so the common case gets a
	//    clean rejection without a failed insert. It is deliberately not
	//    the enforcement point: the database constraint below is, because
	//    this read and the insert that follows are not atomic together.
	pending, err := s.repo.CountPending(ctx, input.UserID)
	if err != nil {
		return nil, err
	}
	if pending >= maxPendingPerUser {
		return nil, ErrTooManyPending
	}

	// 3. Build the pending record. Verdict fields stay zeroed until the
	//    worker judges it; FailedCase of -1 means "no case failed yet".
	sub := &Submission{
		UserID:       input.UserID,
		ProblemID:    input.ProblemID,
		ProblemSlug:  input.ProblemSlug,
		ProblemTitle: input.ProblemTitle,
		WarRoomID:    input.WarRoomID,
		Language:     input.Language,
		Code:         input.Code,
		Status:       StatusPending,
		FailedCase:   -1,
		SubmittedAt:  time.Now().UTC(),
	}

	// 4. Persist it. This is where admission control is actually decided:
	//    the repository reports ErrTooManyPending when the constraint
	//    rejects the insert, which is what catches a race that slipped
	//    past the pre-check above.
	if err := s.repo.Create(ctx, sub); err != nil {
		return nil, err
	}
	return sub, nil
}

// MarkRunning flags a submission as picked up by a judge worker.
func (s *Service) MarkRunning(ctx context.Context, id string) error {
	return s.repo.UpdateStatus(ctx, id, StatusRunning, nil)
}

// maxCompileErrorBytes caps how much compiler output is stored on a
// submission.
//
// The judge buffers up to a mebibyte of it, and all of that was being
// persisted per submission. One template-heavy C++ error reaches that
// figure by itself, and nothing stops a user resubmitting it in a loop,
// so the stored size of the collection was effectively user-controlled.
// Eight kibibytes is dozens of lines of compiler output — far more than
// anyone reads before fixing the first error, which is the one that
// matters.
const maxCompileErrorBytes = 8 * 1024

// truncationMarker tells the user output was cut. Silently dropping the
// tail would leave them looking for errors that are not there.
const truncationMarker = "\n\n... compiler output truncated ...\n"

// truncateCompileError caps stored compiler output, keeping the head.
//
// The head, not the tail: compilers report the first error first, and
// everything after it is usually that same error echoing through the
// rest of the file.
func truncateCompileError(out string) string {
	if len(out) <= maxCompileErrorBytes {
		return out
	}

	cut := maxCompileErrorBytes
	// Never split a multi-byte rune — the stored text has to stay valid
	// UTF-8, since compiler output routinely quotes the user's source.
	for cut > 0 && !utf8.RuneStart(out[cut]) {
		cut--
	}
	return out[:cut] + truncationMarker
}

// MarkJudged writes the final verdict. It is called only by the judge
// worker — the user-facing API never sets a verdict, which keeps the
// outcome server-authoritative.
func (s *Service) MarkJudged(ctx context.Context, id string, result Result) error {
	if !result.Status.IsTerminal() {
		return ValidationError{Field: "status", Message: "must be a terminal verdict"}
	}
	// Capped here rather than in the judge so every path that records a
	// verdict — queued worker, inline fallback — is covered by one rule.
	result.CompileError = truncateCompileError(result.CompileError)
	return s.repo.UpdateStatus(ctx, id, result.Status, &result)
}

// MarkFailed records an infrastructure failure — a sandbox that would
// not start, a worker that gave up, a judge that never reported back.
//
// It records StatusError, not a verdict. The judge never ran the code to
// a conclusion, so claiming it crashed would be a lie told at the user's
// expense; "Could Not Judge" is the honest answer. The status is still
// terminal, so the submission never sits pending forever and the user's
// admission-control slot is released.
//
// The reason travels in CompileError, which is the record's only free
// text field. It is for operators reading the database, not for the UI,
// which renders that field only for a compile_error verdict.
func (s *Service) MarkFailed(ctx context.Context, id, reason string) error {
	return s.repo.UpdateStatus(ctx, id, StatusError, &Result{
		Status:       StatusError,
		FailedCase:   -1,
		CompileError: reason,
	})
}

// StaleReason is what a reclaimed submission carries as its reason. It
// is written to be read by the person whose submission it was: the judge
// is at fault here, not their code, and it says so.
const StaleReason = "the judge did not report a result for this submission, " +
	"so it was closed automatically — your code was not rejected, please try again"

// ReclaimStale closes submissions that have been stuck in a non-terminal
// state for longer than olderThan, and returns how many it closed.
//
// Nothing else ever releases them. A worker can die between accepting a
// job and writing a status — killed, OOM-ed, disconnected from the
// database — and the row then sits pending forever, holding the user's
// one admission-control slot and locking them out of submitting at all.
//
// The write is conditional on the row still being non-terminal, so a
// sweep that overlaps a worker finishing the same submission loses the
// race harmlessly rather than overwriting the verdict.
func (s *Service) ReclaimStale(ctx context.Context, olderThan time.Duration) (int, error) {
	if olderThan <= 0 {
		return 0, ValidationError{Field: "olderThan", Message: "must be positive"}
	}
	return s.repo.ReclaimStale(ctx, time.Now().UTC().Add(-olderThan), StaleReason)
}

// GetByID returns one submission, or ErrNotFound.
func (s *Service) GetByID(ctx context.Context, id string) (*Submission, error) {
	return s.repo.GetByID(ctx, id)
}

// List returns a page of submissions matching the filter, newest first.
func (s *Service) List(ctx context.Context, filter ListFilter) ([]Submission, error) {
	return s.repo.List(ctx, filter)
}

// Count returns the total number of submissions matching the filter,
// ignoring pagination, so the client can render page controls.
func (s *Service) Count(ctx context.Context, filter ListFilter) (int, error) {
	return s.repo.Count(ctx, filter)
}

// Stats summarises one user's judging record for the profile page.
func (s *Service) Stats(ctx context.Context, userID string) (Stats, error) {
	total, err := s.repo.Count(ctx, ListFilter{UserID: userID})
	if err != nil {
		return Stats{}, err
	}
	accepted, err := s.repo.CountAccepted(ctx, userID)
	if err != nil {
		return Stats{}, err
	}
	solved, err := s.repo.SolvedProblemIDs(ctx, userID)
	if err != nil {
		return Stats{}, err
	}
	return Stats{TotalSubmissions: total, Accepted: accepted, SolvedProblemIDs: solved}, nil
}

// FirstAcceptedInRoom returns the submission that won a War Room, judged
// by server-stamped JudgedAt rather than any client-reported time.
func (s *Service) FirstAcceptedInRoom(ctx context.Context, warRoomID string) (*Submission, error) {
	return s.repo.FirstAcceptedInRoom(ctx, warRoomID)
}

// StatusFromVerdict maps a judge verdict onto a submission status. The
// two vocabularies are deliberately identical, so this is a total
// mapping with a safe fallback.
func StatusFromVerdict(v judge.Verdict) Status {
	switch v {
	case judge.VerdictAccepted:
		return StatusAccepted
	case judge.VerdictWrongAnswer:
		return StatusWrongAnswer
	case judge.VerdictTimeLimitExceeded:
		return StatusTLE
	case judge.VerdictMemoryLimitExceeded:
		return StatusMLE
	case judge.VerdictCompileError:
		return StatusCompileError
	case judge.VerdictOutputLimitExceeded:
		return StatusOutputLimitExceeded
	default:
		return StatusRuntimeError
	}
}

func validateCreateInput(input CreateInput) error {
	if strings.TrimSpace(input.UserID) == "" {
		return ValidationError{Field: "userId", Message: "is required"}
	}
	if strings.TrimSpace(input.ProblemID) == "" {
		return ValidationError{Field: "problemId", Message: "is required"}
	}
	if _, err := judge.GetLanguage(input.Language); err != nil {
		return ValidationError{Field: "language", Message: "is not supported"}
	}
	if strings.TrimSpace(input.Code) == "" {
		return ValidationError{Field: "code", Message: "must not be empty"}
	}
	if len(input.Code) > maxCodeBytes {
		return ValidationError{Field: "code", Message: "exceeds the 64KB size limit"}
	}
	return nil
}
