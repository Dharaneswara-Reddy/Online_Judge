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
)

// IsTerminal reports whether the status is a final verdict, meaning the
// judge is done with this submission and will not update it again.
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
	ID           string     `bson:"_id,omitempty" json:"id"`
	UserID       string     `bson:"user_id" json:"userId"`
	ProblemID    string     `bson:"problem_id" json:"problemId"`
	ProblemSlug  string     `bson:"problem_slug" json:"problemSlug"`
	ProblemTitle string     `bson:"problem_title" json:"problemTitle"`
	WarRoomID    string     `bson:"war_room_id,omitempty" json:"warRoomId,omitempty"`
	Language     string     `bson:"language" json:"language"`
	Code         string     `bson:"code" json:"code"`
	Status       Status     `bson:"status" json:"status"`
	RuntimeMS    int64      `bson:"runtime_ms" json:"runtimeMs"`
	MemoryKB     int64      `bson:"memory_kb" json:"memoryKb"`
	FailedCase   int        `bson:"failed_case" json:"failedCase"`
	TotalCases   int        `bson:"total_cases" json:"totalCases"`
	CompileError string     `bson:"compile_error,omitempty" json:"compileError,omitempty"`
	SubmittedAt  time.Time  `bson:"submitted_at" json:"submittedAt"`
	JudgedAt     *time.Time `bson:"judged_at,omitempty" json:"judgedAt,omitempty"`
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
