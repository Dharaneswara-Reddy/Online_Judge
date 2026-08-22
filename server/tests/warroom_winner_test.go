package tests

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/warroom"
	warroommongo "github.com/toji339/online-judge/internal/warroom/mongorepo"
)

// winnerRoom creates an in-progress room with two participants.
func winnerRoom(t *testing.T, repo *warroommongo.MongoRepository) *warroom.Room {
	t.Helper()
	ctx := context.Background()

	room := &warroom.Room{
		RoomCode:        randomRoomCode(t),
		ProblemID:       "problem-1",
		ProblemSlug:     "two-sum",
		ProblemTitle:    "Two Sum",
		Difficulty:      "easy",
		MaxParticipants: 2,
		Status:          warroom.StatusWaiting,
		Participants: []warroom.Participant{
			{UserID: "user-a", Username: "alice", JoinedAt: time.Now().UTC()},
			{UserID: "user-b", Username: "bob", JoinedAt: time.Now().UTC()},
		},
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, repo.Create(ctx, room))
	require.NoError(t, repo.Start(ctx, room.ID, time.Now().UTC()))
	return room
}

// TestDeclareWinner_EarliestJudgedAtWins is the W1 defect.
//
// DeclareWinner filtered only on "no winner yet", so whichever worker's
// write reached Mongo first took the win. The verdict timestamp it was
// handed was stored as ended_at and never compared. Two workers finishing
// at nearly the same moment therefore raced on write scheduling rather
// than on when the entrants actually solved the problem — and the loser
// could be the one who genuinely finished first.
func TestDeclareWinner_EarliestJudgedAtWins(t *testing.T) {
	repo := warroommongo.New(testDB)
	ctx := context.Background()
	room := winnerRoom(t, repo)

	early := time.Now().UTC().Add(-10 * time.Second)
	late := time.Now().UTC()

	// The LATER finisher's write arrives first — the ordering the old code
	// rewarded.
	won, err := repo.DeclareWinner(ctx, room.ID, "user-b", "bob", late)
	require.NoError(t, err)
	assert.True(t, won, "the first write with no winner set does provisionally win")

	// The genuinely-earlier finisher arrives second.
	won, err = repo.DeclareWinner(ctx, room.ID, "user-a", "alice", early)
	require.NoError(t, err)
	assert.True(t, won, "an earlier judged_at must be able to correct a provisional winner")

	final, err := repo.GetByID(ctx, room.ID)
	require.NoError(t, err)
	assert.Equal(t, "user-a", final.WinnerID,
		"the winner must be whoever the judge stamped earliest, not whoever's write landed first")
}

// A later verdict must never displace an earlier one.
func TestDeclareWinner_LaterJudgedAtCannotStealTheWin(t *testing.T) {
	repo := warroommongo.New(testDB)
	ctx := context.Background()
	room := winnerRoom(t, repo)

	early := time.Now().UTC().Add(-10 * time.Second)
	late := time.Now().UTC()

	won, err := repo.DeclareWinner(ctx, room.ID, "user-a", "alice", early)
	require.NoError(t, err)
	require.True(t, won)

	won, err = repo.DeclareWinner(ctx, room.ID, "user-b", "bob", late)
	require.NoError(t, err)
	assert.False(t, won, "a slower solve must not take the win")

	final, err := repo.GetByID(ctx, room.ID)
	require.NoError(t, err)
	assert.Equal(t, "user-a", final.WinnerID)
}

// TestDeclareWinner_CannotResurrectAnExpiredRoom is W4. The filter had no
// status precondition, so a verdict arriving after ExpireStale had closed
// a room flipped it back to finished and gave it a winner.
func TestDeclareWinner_CannotResurrectAnExpiredRoom(t *testing.T) {
	repo := warroommongo.New(testDB)
	ctx := context.Background()
	room := winnerRoom(t, repo)

	future := time.Now().UTC().Add(time.Hour)
	_, err := repo.ExpireStale(ctx, future, future)
	require.NoError(t, err)

	expired, err := repo.GetByID(ctx, room.ID)
	require.NoError(t, err)
	require.Equal(t, warroom.StatusExpired, expired.Status, "precondition: the room is expired")

	won, err := repo.DeclareWinner(ctx, room.ID, "user-a", "alice", time.Now().UTC())
	require.NoError(t, err)
	assert.False(t, won, "an expired room must not be resurrected as finished")

	final, err := repo.GetByID(ctx, room.ID)
	require.NoError(t, err)
	assert.Equal(t, warroom.StatusExpired, final.Status)
	assert.Empty(t, final.WinnerID)
}

// TestDeclareWinner_ConcurrentDeclarationsSettleOnTheEarliest runs the race
// for real: many workers declaring at once, in an arrival order deliberately
// uncorrelated with the judged_at order.
func TestDeclareWinner_ConcurrentDeclarationsSettleOnTheEarliest(t *testing.T) {
	repo := warroommongo.New(testDB)
	ctx := context.Background()
	room := winnerRoom(t, repo)

	base := time.Now().UTC()
	const racers = 12

	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Racer 0 has the earliest verdict but no scheduling advantage.
			judgedAt := base.Add(time.Duration(i) * time.Second)
			_, _ = repo.DeclareWinner(ctx, room.ID, userIDFor(i), userIDFor(i), judgedAt)
		}(i)
	}
	wg.Wait()

	final, err := repo.GetByID(ctx, room.ID)
	require.NoError(t, err)
	assert.Equal(t, userIDFor(0), final.WinnerID,
		"under concurrency the earliest verdict must still win")
	assert.Equal(t, warroom.StatusFinished, final.Status)
}

// randomRoomCode keeps each test's room distinct under the unique index.
func randomRoomCode(t *testing.T) string {
	t.Helper()
	b := make([]byte, 4)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return strings.ToUpper(hex.EncodeToString(b))
}

func userIDFor(i int) string {
	return string(rune('a'+i)) + "-racer"
}

// TestAddParticipant_ExactlyOneJoinerSeesTheRoomBecomeFull is W3.
//
// becameFull was derived from a GetByID issued after the push, not from
// the push itself. Two players taking the last two seats at the same
// moment therefore both read the final, full room and both were told they
// had filled it — so room_started was broadcast twice and the start
// side-effect ran twice.
func TestAddParticipant_ExactlyOneJoinerSeesTheRoomBecomeFull(t *testing.T) {
	repo := warroommongo.New(testDB)
	ctx := context.Background()

	const seats = 6
	room := &warroom.Room{
		RoomCode:        randomRoomCode(t),
		ProblemID:       "problem-1",
		ProblemSlug:     "two-sum",
		ProblemTitle:    "Two Sum",
		Difficulty:      "easy",
		MaxParticipants: seats,
		Status:          warroom.StatusWaiting,
		Participants:    []warroom.Participant{},
		CreatedAt:       time.Now().UTC(),
	}
	require.NoError(t, repo.Create(ctx, room))

	var (
		mu        sync.Mutex
		fullCount int
	)
	var wg sync.WaitGroup
	for i := 0; i < seats; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			full, err := repo.AddParticipant(ctx, room.ID, warroom.Participant{
				UserID:   userIDFor(i),
				Username: userIDFor(i),
				JoinedAt: time.Now().UTC(),
			})
			if err != nil {
				return
			}
			if full {
				mu.Lock()
				fullCount++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	final, err := repo.GetByID(ctx, room.ID)
	require.NoError(t, err)
	require.Len(t, final.Participants, seats, "every seat should be taken exactly once")

	assert.Equal(t, 1, fullCount,
		"exactly one joiner may observe the room becoming full — otherwise room_started "+
			"is broadcast once per racing joiner")
}
