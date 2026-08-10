package warroom_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/problem"
	"github.com/toji339/online-judge/internal/problem/problemtest"
	"github.com/toji339/online-judge/internal/warroom"
	"github.com/toji339/online-judge/internal/warroom/warroomtest"
)

func newService(t *testing.T) (*warroom.Service, *warroomtest.FakeRepository) {
	t.Helper()
	problems := problem.NewService(problemtest.NewFakeRepository())
	_, err := problems.Create(context.Background(), problem.CreateProblemInput{
		Title: "Two Sum", Statement: "add", Difficulty: problem.DifficultyEasy,
		TimeLimitMS: 1000, MemoryLimitMB: 64,
	})
	require.NoError(t, err)

	repo := warroomtest.New()
	return warroom.NewService(repo, problems), repo
}

func player(id string) warroom.Participant {
	return warroom.Participant{UserID: id, Username: "player-" + id}
}

func createRoom(t *testing.T, svc *warroom.Service, size int) *warroom.Room {
	t.Helper()
	room, err := svc.Create(context.Background(), warroom.CreateInput{
		MaxParticipants: size, Creator: player("user-1"),
	})
	require.NoError(t, err)
	return room
}

// --- Creation ---

func TestCreate_OpensWaitingRoomWithCreatorInside(t *testing.T) {
	svc, _ := newService(t)

	room := createRoom(t, svc, 2)

	assert.Equal(t, warroom.StatusWaiting, room.Status)
	assert.Len(t, room.Participants, 1, "the creator is already a participant")
	assert.Equal(t, "user-1", room.Participants[0].UserID)
	assert.Len(t, room.RoomCode, 6, "the code is short enough to share verbally")
	assert.Equal(t, "Two Sum", room.ProblemTitle, "a random problem is picked when none is named")
	assert.Nil(t, room.StartedAt)
}

func TestCreate_RejectsInvalidRoomSize(t *testing.T) {
	svc, _ := newService(t)

	for _, size := range []int{0, 1, 4, 10} {
		_, err := svc.Create(context.Background(), warroom.CreateInput{
			MaxParticipants: size, Creator: player("user-1"),
		})
		var vErr warroom.ValidationError
		assert.ErrorAs(t, err, &vErr, "room size %d must be rejected", size)
	}
}

func TestCreate_GeneratesDistinctRoomCodes(t *testing.T) {
	svc, _ := newService(t)

	seen := make(map[string]bool)
	for range 20 {
		room := createRoom(t, svc, 2)
		assert.False(t, seen[room.RoomCode], "room codes must not repeat")
		seen[room.RoomCode] = true
	}
}

// --- Joining ---

func TestJoin_StartsRaceWhenRoomFills(t *testing.T) {
	svc, _ := newService(t)
	room := createRoom(t, svc, 2)

	updated, started, err := svc.Join(context.Background(), room.RoomCode, player("user-2"))

	require.NoError(t, err)
	assert.True(t, started, "filling the last seat starts the race")
	assert.Equal(t, warroom.StatusInProgress, updated.Status)
	require.NotNil(t, updated.StartedAt, "the start time is stamped by the server")
	assert.Len(t, updated.Participants, 2)
}

func TestJoin_ThreePlayerRoomWaitsForTheThird(t *testing.T) {
	svc, _ := newService(t)
	room := createRoom(t, svc, 3)

	_, started, err := svc.Join(context.Background(), room.RoomCode, player("user-2"))
	require.NoError(t, err)
	assert.False(t, started, "a room below capacity keeps waiting")

	updated, started, err := svc.Join(context.Background(), room.RoomCode, player("user-3"))
	require.NoError(t, err)
	assert.True(t, started)
	assert.Equal(t, warroom.StatusInProgress, updated.Status)
}

func TestJoin_RejoiningIsHarmless(t *testing.T) {
	svc, _ := newService(t)
	room := createRoom(t, svc, 3)

	updated, started, err := svc.Join(context.Background(), room.RoomCode, player("user-1"))

	require.NoError(t, err, "refreshing the page must not fail")
	assert.False(t, started)
	assert.Len(t, updated.Participants, 1, "the creator is not added twice")
}

func TestJoin_RejectsFullRoom(t *testing.T) {
	svc, _ := newService(t)
	room := createRoom(t, svc, 2)
	_, _, err := svc.Join(context.Background(), room.RoomCode, player("user-2"))
	require.NoError(t, err)

	_, _, err = svc.Join(context.Background(), room.RoomCode, player("user-3"))

	// The room started when it filled, so latecomers are turned away.
	assert.ErrorIs(t, err, warroom.ErrRoomClosed)
}

func TestJoin_UnknownCodeReturnsNotFound(t *testing.T) {
	svc, _ := newService(t)

	_, _, err := svc.Join(context.Background(), "ZZZZZZ", player("user-2"))

	assert.ErrorIs(t, err, warroom.ErrNotFound)
}

// TestJoin_ConcurrentJoinsCannotOverfillARoom is the edge case that
// matters most: two players racing for one remaining seat.
func TestJoin_ConcurrentJoinsCannotOverfillARoom(t *testing.T) {
	svc, _ := newService(t)
	room := createRoom(t, svc, 2)

	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range 4 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, errs[i] = svc.Join(context.Background(), room.RoomCode, player(string(rune('a'+i))))
		}(i)
	}
	wg.Wait()

	final, err := svc.GetByCode(context.Background(), room.RoomCode)
	require.NoError(t, err)
	assert.Len(t, final.Participants, 2, "the room never exceeds its cap")

	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
		}
	}
	assert.Equal(t, 1, succeeded, "exactly one of the racing joins is admitted")
}

// --- Winner determination ---

func TestDeclareWinner_FirstAcceptedSubmissionWins(t *testing.T) {
	svc, _ := newService(t)
	room := createRoom(t, svc, 2)
	_, _, err := svc.Join(context.Background(), room.RoomCode, player("user-2"))
	require.NoError(t, err)

	won, err := svc.DeclareWinner(context.Background(), room.ID, "user-2", "player-user-2", time.Now().UTC())

	require.NoError(t, err)
	assert.True(t, won)

	final, err := svc.GetByID(context.Background(), room.ID)
	require.NoError(t, err)
	assert.Equal(t, warroom.StatusFinished, final.Status)
	assert.Equal(t, "user-2", final.WinnerID)
	require.NotNil(t, final.EndedAt)
}

func TestDeclareWinner_SecondFinisherDoesNotOverwriteTheWinner(t *testing.T) {
	svc, _ := newService(t)
	room := createRoom(t, svc, 2)
	_, _, err := svc.Join(context.Background(), room.RoomCode, player("user-2"))
	require.NoError(t, err)

	first, err := svc.DeclareWinner(context.Background(), room.ID, "user-1", "player-user-1", time.Now().UTC())
	require.NoError(t, err)
	require.True(t, first)

	second, err := svc.DeclareWinner(context.Background(), room.ID, "user-2", "player-user-2", time.Now().UTC())

	require.NoError(t, err)
	assert.False(t, second, "only the first finisher is announced as the winner")

	final, _ := svc.GetByID(context.Background(), room.ID)
	assert.Equal(t, "user-1", final.WinnerID)
}

func TestDeclareWinner_ConcurrentFinishersProduceOneWinner(t *testing.T) {
	svc, _ := newService(t)
	room := createRoom(t, svc, 2)
	_, _, err := svc.Join(context.Background(), room.RoomCode, player("user-2"))
	require.NoError(t, err)

	var wg sync.WaitGroup
	results := make([]bool, 2)
	for i, userID := range []string{"user-1", "user-2"} {
		wg.Add(1)
		go func(i int, userID string) {
			defer wg.Done()
			won, _ := svc.DeclareWinner(context.Background(), room.ID, userID, userID, time.Now().UTC())
			results[i] = won
		}(i, userID)
	}
	wg.Wait()

	wins := 0
	for _, won := range results {
		if won {
			wins++
		}
	}
	assert.Equal(t, 1, wins, "exactly one caller may announce the result")
}

// --- Participation guard ---

func TestEnsureParticipant_RejectsOutsiders(t *testing.T) {
	svc, _ := newService(t)
	room := createRoom(t, svc, 2)

	_, err := svc.EnsureParticipant(context.Background(), room.RoomCode, "stranger")
	assert.ErrorIs(t, err, warroom.ErrNotParticipant)

	got, err := svc.EnsureParticipant(context.Background(), room.RoomCode, "user-1")
	require.NoError(t, err)
	assert.Equal(t, room.ID, got.ID)
}

// --- Expiry ---

func TestExpireStale_ClosesAbandonedRooms(t *testing.T) {
	svc, repo := newService(t)
	room := createRoom(t, svc, 2)

	// Age the room past the waiting TTL.
	repo.Backdate(room.ID, time.Now().UTC().Add(-warroom.WaitingRoomTTL-time.Minute), nil)

	expired, err := svc.ExpireStale(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 1, expired)

	stale, err := svc.GetByID(context.Background(), room.ID)
	require.NoError(t, err)
	assert.Equal(t, warroom.StatusExpired, stale.Status, "an abandoned room does not linger in the lobby")
}

func TestExpireStale_LeavesFreshRoomsAlone(t *testing.T) {
	svc, _ := newService(t)
	room := createRoom(t, svc, 2)

	expired, err := svc.ExpireStale(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 0, expired)

	fresh, err := svc.GetByID(context.Background(), room.ID)
	require.NoError(t, err)
	assert.Equal(t, warroom.StatusWaiting, fresh.Status)
}

func TestListOpen_HidesRoomsThatAlreadyStarted(t *testing.T) {
	svc, _ := newService(t)
	open := createRoom(t, svc, 3)
	full := createRoom(t, svc, 2)
	_, _, err := svc.Join(context.Background(), full.RoomCode, player("user-2"))
	require.NoError(t, err)

	rooms, err := svc.ListOpen(context.Background(), 10)

	require.NoError(t, err)
	codes := make([]string, len(rooms))
	for i, r := range rooms {
		codes[i] = r.RoomCode
	}
	assert.Contains(t, codes, open.RoomCode)
	assert.NotContains(t, codes, full.RoomCode, "a race in progress is not advertised in the lobby")
}
