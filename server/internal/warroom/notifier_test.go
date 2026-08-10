package warroom_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/realtime"
	"github.com/toji339/online-judge/internal/submission"
	"github.com/toji339/online-judge/internal/warroom"
)

// startedRoom creates a two-player room and fills it so the race is live.
func startedRoom(t *testing.T, svc *warroom.Service) *warroom.Room {
	t.Helper()
	room := createRoom(t, svc, 2)
	updated, _, err := svc.Join(context.Background(), room.RoomCode, player("user-2"))
	require.NoError(t, err)
	return updated
}

// judged builds a judged submission tagged with a room.
func judged(roomID, userID string, status submission.Status) *submission.Submission {
	at := time.Now().UTC()
	return &submission.Submission{
		ID: "sub-1", UserID: userID, WarRoomID: roomID,
		Language: "python", Status: status, JudgedAt: &at,
	}
}

// collect drains the events currently buffered on a subscription.
func collect(events <-chan realtime.Event) []realtime.Event {
	var out []realtime.Event
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return out
			}
			out = append(out, event)
		case <-time.After(100 * time.Millisecond):
			return out
		}
	}
}

func TestNotifier_IgnoresOrdinaryPracticeSubmissions(t *testing.T) {
	svc, _ := newService(t)
	bus := realtime.NewMemoryBus()
	notifier := warroom.NewJudgeNotifier(svc, bus)
	room := startedRoom(t, svc)

	events, cancel, err := bus.Subscribe(context.Background(), room.ID)
	require.NoError(t, err)
	defer cancel()

	// A submission with no room must not touch the race at all.
	notifier.SubmissionJudged(context.Background(), judged("", "user-1", submission.StatusAccepted))

	assert.Empty(t, collect(events))

	final, _ := svc.GetByID(context.Background(), room.ID)
	assert.Equal(t, warroom.StatusInProgress, final.Status)
}

func TestNotifier_BroadcastsProgressWithoutRevealingCode(t *testing.T) {
	svc, _ := newService(t)
	bus := realtime.NewMemoryBus()
	notifier := warroom.NewJudgeNotifier(svc, bus)
	room := startedRoom(t, svc)

	events, cancel, err := bus.Subscribe(context.Background(), room.ID)
	require.NoError(t, err)
	defer cancel()

	sub := judged(room.ID, "user-2", submission.StatusWrongAnswer)
	sub.Code = "the secret solution"
	notifier.SubmissionJudged(context.Background(), sub)

	received := collect(events)
	require.Len(t, received, 1)
	assert.Equal(t, warroom.EventSubmissionUpdate, received[0].Type)

	var progress warroom.ParticipantProgress
	require.NoError(t, json.Unmarshal(received[0].Payload, &progress))
	assert.Equal(t, "user-2", progress.UserID)
	assert.Equal(t, "player-user-2", progress.Username)
	assert.Equal(t, "wrong_answer", progress.Status)
	assert.NotContains(t, string(received[0].Payload), "secret",
		"an opponent's source code must never be broadcast")

	// A losing verdict does not end the race.
	final, _ := svc.GetByID(context.Background(), room.ID)
	assert.Equal(t, warroom.StatusInProgress, final.Status)
}

func TestNotifier_AcceptedSubmissionWinsAndFinishesTheRoom(t *testing.T) {
	svc, _ := newService(t)
	bus := realtime.NewMemoryBus()
	notifier := warroom.NewJudgeNotifier(svc, bus)
	room := startedRoom(t, svc)

	events, cancel, err := bus.Subscribe(context.Background(), room.ID)
	require.NoError(t, err)
	defer cancel()

	notifier.SubmissionJudged(context.Background(), judged(room.ID, "user-2", submission.StatusAccepted))

	received := collect(events)
	require.Len(t, received, 2, "progress first, then the finish")
	assert.Equal(t, warroom.EventSubmissionUpdate, received[0].Type)
	assert.Equal(t, warroom.EventRoomFinished, received[1].Type)

	var finished warroom.Room
	require.NoError(t, json.Unmarshal(received[1].Payload, &finished))
	assert.Equal(t, "user-2", finished.WinnerID)
	assert.Equal(t, warroom.StatusFinished, finished.Status)

	stored, _ := svc.GetByID(context.Background(), room.ID)
	assert.Equal(t, "user-2", stored.WinnerID)
	require.NotNil(t, stored.EndedAt)
}

func TestNotifier_SecondFinisherDoesNotAnnounceAResult(t *testing.T) {
	svc, _ := newService(t)
	bus := realtime.NewMemoryBus()
	notifier := warroom.NewJudgeNotifier(svc, bus)
	room := startedRoom(t, svc)

	notifier.SubmissionJudged(context.Background(), judged(room.ID, "user-1", submission.StatusAccepted))

	events, cancel, err := bus.Subscribe(context.Background(), room.ID)
	require.NoError(t, err)
	defer cancel()

	// A second player finishing later still gets their progress shown,
	// but must not overwrite or re-announce the winner.
	notifier.SubmissionJudged(context.Background(), judged(room.ID, "user-2", submission.StatusAccepted))

	received := collect(events)
	require.Len(t, received, 1)
	assert.Equal(t, warroom.EventSubmissionUpdate, received[0].Type)

	stored, _ := svc.GetByID(context.Background(), room.ID)
	assert.Equal(t, "user-1", stored.WinnerID, "the first finisher keeps the win")
}
