// Package warroom implements the live 1v1 (or small-group) racing mode:
// two or three players share a problem and race to be the first to reach
// an accepted verdict.
//
// Everything that decides the outcome is server-authoritative. The winner
// is the first submission the judge stamps accepted, using the server's
// own judged_at timestamp — a client's clock is never consulted.
package warroom

import "time"

// Status is where a room sits in its lifecycle:
//
//	waiting → in_progress → finished
//
// A room that nobody ever joins, or that nobody finishes, ends as
// expired instead of hanging around forever.
type Status string

const (
	StatusWaiting    Status = "waiting"
	StatusInProgress Status = "in_progress"
	StatusFinished   Status = "finished"
	StatusExpired    Status = "expired"
)

// Participant is one player in a room.
type Participant struct {
	UserID   string    `bson:"user_id" json:"userId"`
	Username string    `bson:"username" json:"username"`
	JoinedAt time.Time `bson:"joined_at" json:"joinedAt"`
}

// Room is one race. The problem's slug and title are denormalised so the
// lobby can list rooms without a second lookup per row.
type Room struct {
	ID              string        `bson:"_id,omitempty" json:"id"`
	RoomCode        string        `bson:"room_code" json:"roomCode"`
	ProblemID       string        `bson:"problem_id" json:"problemId"`
	ProblemSlug     string        `bson:"problem_slug" json:"problemSlug"`
	ProblemTitle    string        `bson:"problem_title" json:"problemTitle"`
	Difficulty      string        `bson:"difficulty" json:"difficulty"`
	Participants    []Participant `bson:"participants" json:"participants"`
	MaxParticipants int           `bson:"max_participants" json:"maxParticipants"`
	Status          Status        `bson:"status" json:"status"`
	WinnerID        string        `bson:"winner_id,omitempty" json:"winnerId,omitempty"`
	WinnerUsername  string        `bson:"winner_username,omitempty" json:"winnerUsername,omitempty"`
	// WinnerSolvedAt is the server-authoritative moment the winner
	// committed their accepted solution. It is what the win is ordered by,
	// so a verdict that arrives late can still take the win if the entrant
	// actually finished first.
	WinnerSolvedAt *time.Time `bson:"winner_solved_at,omitempty" json:"-"`
	StartedAt      *time.Time `bson:"started_at,omitempty" json:"startedAt,omitempty"`
	EndedAt        *time.Time `bson:"ended_at,omitempty" json:"endedAt,omitempty"`
	CreatedAt      time.Time  `bson:"created_at" json:"createdAt"`
}

// IsFull reports whether the room has reached its participant cap.
func (r *Room) IsFull() bool { return len(r.Participants) >= r.MaxParticipants }

// HasParticipant reports whether a user is already in the room.
func (r *Room) HasParticipant(userID string) bool {
	for _, p := range r.Participants {
		if p.UserID == userID {
			return true
		}
	}
	return false
}

// Event type names broadcast over the room's WebSocket. The client
// switches on these to update the live race view.
const (
	EventParticipantJoined = "participant_joined"
	EventRoomStarted       = "room_started"
	EventSubmissionUpdate  = "submission_update"
	EventRoomFinished      = "room_finished"
)

// ParticipantProgress is the opponent-facing view of a submission. It
// deliberately carries no source code — players see that an opponent is
// submitting and how it went, never what they wrote.
type ParticipantProgress struct {
	UserID   string `json:"userId"`
	Username string `json:"username"`
	Status   string `json:"status"`
	Language string `json:"language"`
}
