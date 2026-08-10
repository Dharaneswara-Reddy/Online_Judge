package warroom

import (
	"context"
	"encoding/json"
	"log"

	"github.com/toji339/online-judge/internal/realtime"
	"github.com/toji339/online-judge/internal/submission"
)

// JudgeNotifier reacts to judged submissions on behalf of the War Room.
//
// It satisfies worker.Notifier structurally, so the judge worker can
// drive War Room outcomes without the worker package knowing anything
// about rooms.
//
// This is where the race is actually decided, and it runs inside the
// judge worker — the only place that knows when a verdict became final.
type JudgeNotifier struct {
	rooms *Service
	bus   realtime.Bus
}

// NewJudgeNotifier wires a notifier to a room service and an event bus.
func NewJudgeNotifier(rooms *Service, bus realtime.Bus) *JudgeNotifier {
	return &JudgeNotifier{rooms: rooms, bus: bus}
}

// SubmissionJudged broadcasts a racer's progress and, when the verdict is
// accepted, tries to claim the win.
//
// Ordinary practice submissions carry no room and are ignored.
func (n *JudgeNotifier) SubmissionJudged(ctx context.Context, sub *submission.Submission) {
	if sub.WarRoomID == "" {
		return
	}

	room, err := n.rooms.GetByID(ctx, sub.WarRoomID)
	if err != nil {
		log.Printf("warroom: could not load room %s for submission %s: %v", sub.WarRoomID, sub.ID, err)
		return
	}

	username := usernameOf(room, sub.UserID)

	// 1. Tell the room how this attempt went. Opponents see the outcome
	//    and the language, never the source code.
	n.publish(ctx, room.ID, EventSubmissionUpdate, ParticipantProgress{
		UserID:   sub.UserID,
		Username: username,
		Status:   string(sub.Status),
		Language: sub.Language,
	})

	if sub.Status != submission.StatusAccepted {
		return
	}

	// 2. An accepted submission claims the win. DeclareWinner is a
	//    compare-and-set, so only the genuinely-first finisher announces
	//    a result even if two verdicts land at the same instant.
	judgedAt := sub.SubmittedAt
	if sub.JudgedAt != nil {
		judgedAt = *sub.JudgedAt
	}

	won, err := n.rooms.DeclareWinner(ctx, room.ID, sub.UserID, username, judgedAt)
	if err != nil {
		log.Printf("warroom: could not declare winner for room %s: %v", room.ID, err)
		return
	}
	if !won {
		return
	}

	final, err := n.rooms.GetByID(ctx, room.ID)
	if err != nil {
		log.Printf("warroom: could not reload finished room %s: %v", room.ID, err)
		return
	}
	n.publish(ctx, room.ID, EventRoomFinished, final)
}

// publish encodes a payload and broadcasts it to the room's channel.
func (n *JudgeNotifier) publish(ctx context.Context, roomID, eventType string, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("warroom: could not encode %s event: %v", eventType, err)
		return
	}
	if err := n.bus.Publish(ctx, roomID, realtime.Event{Type: eventType, Payload: body}); err != nil {
		log.Printf("warroom: could not broadcast %s event: %v", eventType, err)
	}
}

// usernameOf resolves a participant's display name from the room, which
// saves denormalising it onto every submission.
func usernameOf(room *Room, userID string) string {
	for _, p := range room.Participants {
		if p.UserID == userID {
			return p.Username
		}
	}
	return "unknown"
}
