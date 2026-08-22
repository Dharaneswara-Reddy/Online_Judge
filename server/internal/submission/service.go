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

// MaxCompileErrorBytes caps the compiler output stored on a submission.
//
// The judge already buffers up to 1MiB of a program's output, and every
// byte of it was being persisted verbatim. A template-heavy C++ error, or
// a deliberately pathological one, is trivially that large, and the
// collection grows by that much per attempt — on a small Atlas tier a
// handful of such submissions is the whole storage budget. 16KiB is far
// more than a reader needs to find the first error, which is the only
// part anyone looks at.
const MaxCompileErrorBytes = 16 * 1024

// compileErrorTruncationNotice is appended when output is cut, so the
// reader knows the message is incomplete rather than the compiler having
// stopped mid-sentence.
const compileErrorTruncationNotice = "\n... (truncated: compiler output exceeded the storage limit)"

// Lifecycle timings.
//
// StaleClaimAfter is how long a running submission may go without
// finishing before another worker may take the claim off it. One whole
// evaluation is bounded at a minute by the worker, so anything running
// for five is a worker that died holding the claim.
//
// PendingTTL and RunningTTL are the reaper's cutoffs. RunningTTL is
// deliberately longer than StaleClaimAfter, so a redelivery gets its
// chance to reclaim and finish the work before the reaper writes it off.
// PendingTTL is longer still: a pending submission may legitimately be
// sitting behind a queue backlog.
const (
	StaleClaimAfter = 5 * time.Minute
	RunningTTL      = 10 * time.Minute
	PendingTTL      = 30 * time.Minute
)

// StaleReason is what a reclaimed submission records as its explanation.
// It blames the judge, because the judge is what failed.
const StaleReason = "The judge did not finish this submission — it was reclaimed automatically. Please submit again."

// truncateCompileError bounds stored compiler output.
//
// It keeps the head rather than the tail: compilers report the first
// error first, and what follows is usually that same error echoing
// through the rest of the file. The cut lands on a rune boundary, since
// compiler output routinely quotes the user's source and the stored
// string has to stay valid UTF-8.
func truncateCompileError(s string) string {
	if len(s) <= MaxCompileErrorBytes {
		return s
	}
	cut := MaxCompileErrorBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + compileErrorTruncationNotice
}

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

// MarkRunning claims a submission for this judge worker.
//
// The claim is conditional: it succeeds only while the submission is
// pending, or while it is running but was claimed longer ago than
// StaleClaimAfter, which can only mean the worker holding it died. A
// caller that gets ErrAlreadyClaimed must abandon the job — another
// worker owns it, and judging it again would race that worker's verdict.
func (s *Service) MarkRunning(ctx context.Context, id string) error {
	return s.repo.ClaimForJudging(ctx, id, time.Now().UTC().Add(-StaleClaimAfter))
}

// MarkJudged writes the final verdict. It is called only by the judge
// worker — the user-facing API never sets a verdict, which keeps the
// outcome server-authoritative.
//
// The write is conditional on the submission still being running, so of
// two workers that judged the same submission only the first records a
// result; the second gets ErrAlreadyJudged and throws its verdict away.
func (s *Service) MarkJudged(ctx context.Context, id string, result Result) error {
	if !result.Status.IsTerminal() {
		return ValidationError{Field: "status", Message: "must be a terminal verdict"}
	}
	result.CompileError = truncateCompileError(result.CompileError)
	return s.repo.CompleteJudging(ctx, id, result)
}

// MarkFailed records an infrastructure failure — a sandbox that could not
// be created, a worker that gave up, a queue with nothing behind it — so
// the submission never sits pending forever holding the user's admission
// slot.
//
// It stores StatusJudgeError rather than a runtime error: the judge
// failed, and there is no evidence the user's program did. The write is
// conditional on the submission not already carrying a verdict.
func (s *Service) MarkFailed(ctx context.Context, id, reason string) error {
	return s.repo.FailNonTerminal(ctx, id, StatusJudgeError, truncateCompileError(reason))
}

// ExpireStale reclaims submissions that no worker will ever finish and
// reports how many were reclaimed.
//
// It exists because the admission-control index covers pending and
// running submissions: one row nothing completes is one user permanently
// unable to submit. Run it periodically from the judge worker.
func (s *Service) ExpireStale(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	return s.repo.ExpireStale(ctx, now.Add(-PendingTTL), now.Add(-RunningTTL))
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
