// Package submission owns the lifecycle of a user's code submission:
// creating it in a pending state, tracking it through judging, and
// recording the final verdict.
//
// The package follows the service + repository pattern. Service holds
// all business rules and is unit-tested against the in-memory fake in
// the submissiontest subpackage; the production storage lives in the
// mongorepo subpackage.
package submission

import "time"

// Status is the lifecycle state of a submission. The first two values
// are transient states owned by the queue and worker; the rest are
// terminal verdicts mirroring judge.Verdict.
type Status string

const (
	StatusPending      Status = "pending"
	StatusRunning      Status = "running"
	StatusAccepted     Status = "accepted"
	StatusWrongAnswer  Status = "wrong_answer"
	StatusTLE          Status = "tle"
	StatusMLE          Status = "mle"
	StatusRuntimeError Status = "runtime_error"
	StatusCompileError Status = "compile_error"
	// StatusOutputLimitExceeded means the program printed more than the
	// judge will buffer, so its output could not be compared.
	StatusOutputLimitExceeded Status = "output_limit_exceeded"
	// StatusJudgeError means the judge itself failed — the Docker daemon
	// was unreachable, the sandbox image was missing, the worker died
	// mid-run — so the submission never got a fair verdict. It says
	// nothing about the code, which is the whole point: recording
	// infrastructure failures as StatusRuntimeError told the user their
	// correct solution crashed on test case 0.
	//
	// It is terminal. The partial unique index behind admission control
	// covers only pending and running, so a terminal status is what
	// releases the user's single in-flight slot; a non-terminal "failed"
	// state would lock them out until someone edited the database.
	//
	// The client already speaks this vocabulary: ProblemDetail renders a
	// status of "error" as "Could Not Judge".
	StatusJudgeError Status = "error"
)

// IsTerminal reports whether the status is a final verdict, meaning the
// judge is done with this submission and will not update it again.
//
// Only the two transient states are non-terminal, so a status this
// package does not know about is treated as final rather than leaving a
// submission holding its owner's admission-control slot forever.
func (s Status) IsTerminal() bool {
	switch s {
	case StatusPending, StatusRunning:
		return false
	default:
		return true
	}
}

// Submission is one attempt by a user at one problem. Code is stored so
// the user can review past attempts from their submission history.
//
// WarRoomID is empty for ordinary practice submissions and set only when
// the submission was made inside a War Room race, which routes it to the
// high-priority judging lane.
type Submission struct {
	ID           string    `bson:"_id,omitempty" json:"id"`
	UserID       string    `bson:"user_id" json:"userId"`
	ProblemID    string    `bson:"problem_id" json:"problemId"`
	ProblemSlug  string    `bson:"problem_slug" json:"problemSlug"`
	ProblemTitle string    `bson:"problem_title" json:"problemTitle"`
	WarRoomID    string    `bson:"war_room_id,omitempty" json:"warRoomId,omitempty"`
	Language     string    `bson:"language" json:"language"`
	Code         string    `bson:"code" json:"code"`
	Status       Status    `bson:"status" json:"status"`
	RuntimeMS    int64     `bson:"runtime_ms" json:"runtimeMs"`
	MemoryKB     int64     `bson:"memory_kb" json:"memoryKb"`
	FailedCase   int       `bson:"failed_case" json:"failedCase"`
	TotalCases   int       `bson:"total_cases" json:"totalCases"`
	CompileError string    `bson:"compile_error,omitempty" json:"compileError,omitempty"`
	SubmittedAt  time.Time `bson:"submitted_at" json:"submittedAt"`
	// StartedAt is when a judge worker claimed the submission. It is what
	// makes the claim conditional rather than advisory: a second worker
	// may only take the claim off a submission whose StartedAt is older
	// than StaleClaimAfter, which means the holder is gone.
	StartedAt *time.Time `bson:"started_at,omitempty" json:"startedAt,omitempty"`
	JudgedAt  *time.Time `bson:"judged_at,omitempty" json:"judgedAt,omitempty"`
}

// ListFilter narrows a submission history query. Zero values mean "no
// filter" for the string fields; Page is 1-based.
type ListFilter struct {
	UserID    string
	ProblemID string
	Status    Status
	Page      int
	PageSize  int
}

// Result carries the judge's outcome back into the submission record.
// TotalCases is how many test cases the problem had at judging time, so
// the UI can render "failed on case 3 of 12".
type Result struct {
	Status       Status
	RuntimeMS    int64
	MemoryKB     int64
	FailedCase   int
	TotalCases   int
	CompileError string
}

// Stats summarises a user's judging record. SolvedProblemIDs holds the
// distinct problems the user has ever had accepted, which the caller can
// join against problems to break the count down by difficulty.
type Stats struct {
	TotalSubmissions int      `json:"totalSubmissions"`
	Accepted         int      `json:"accepted"`
	SolvedProblemIDs []string `json:"-"`
}
