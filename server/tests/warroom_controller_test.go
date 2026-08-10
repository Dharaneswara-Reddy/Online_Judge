package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/controllers"
	"github.com/toji339/online-judge/internal/problem"
	problemmongo "github.com/toji339/online-judge/internal/problem/mongorepo"
	"github.com/toji339/online-judge/internal/queue"
	"github.com/toji339/online-judge/internal/realtime"
	"github.com/toji339/online-judge/internal/submission"
	submissionmongo "github.com/toji339/online-judge/internal/submission/mongorepo"
	"github.com/toji339/online-judge/internal/warroom"
	warroommongo "github.com/toji339/online-judge/internal/warroom/mongorepo"
	"github.com/toji339/online-judge/internal/worker"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// warRoomHarness mounts the war room routes with a switchable identity,
// so one test can act as several different players.
type warRoomHarness struct {
	router    *gin.Engine
	rooms     *warroom.Service
	publisher *capturingPublisher
	problem   *problem.Problem

	// current identity used by the stub auth middleware
	userID   string
	username string
}

func setupWarRoomRouter(t *testing.T) *warRoomHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	problemSvc := problem.NewService(problemmongo.New(testDB))
	submissionSvc := submission.NewService(submissionmongo.New(testDB))
	roomSvc := warroom.NewService(warroommongo.New(testDB), problemSvc)
	publisher := &capturingPublisher{}

	h := &warRoomHarness{rooms: roomSvc, publisher: publisher}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", h.userID)
		c.Set("username", h.username)
		c.Set("role", "user")
		c.Next()
	})

	roomController := controllers.NewWarRoomController(roomSvc, realtime.NewMemoryBus(), "http://localhost:5173")
	processor := worker.NewProcessor(submissionSvc, problemSvc, acceptingSandbox(), nil)
	submissionController := controllers.NewSubmissionController(problemSvc, submissionSvc, publisher, processor, roomSvc)

	router.POST("/api/warrooms", roomController.CreateRoom)
	router.GET("/api/warrooms", roomController.ListRooms)
	router.GET("/api/warrooms/:code", roomController.GetRoom)
	router.POST("/api/warrooms/:code/join", roomController.JoinRoom)
	router.POST("/api/warrooms/:code/submit", submissionController.SubmitToWarRoom)

	h.router = router
	h.problem = seedProblem(t, problemSvc)
	return h
}

// as switches the identity subsequent requests are made with.
func (h *warRoomHarness) as(userID, username string) {
	h.userID = userID
	h.username = username
}

func (h *warRoomHarness) do(method, path, body string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("{}")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	return w
}

// createRoomVia posts a room and returns it.
func (h *warRoomHarness) createRoomVia(t *testing.T, size int) *warroom.Room {
	t.Helper()
	w := h.do(http.MethodPost, "/api/warrooms",
		fmt.Sprintf(`{"problemSlug":%q,"maxParticipants":%d}`, h.problem.Slug, size))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var resp struct {
		Data warroom.Room `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return &resp.Data
}

func clearWarRooms(t *testing.T) {
	t.Helper()
	_, err := testDB.Collection("war_rooms").DeleteMany(context.Background(), bson.M{})
	require.NoError(t, err)
	clearSubmissions(t)
}

// =============================================================
// Lifecycle
// =============================================================

func TestWarRoom_CreateAndJoinStartsTheRace(t *testing.T) {
	clearWarRooms(t)
	h := setupWarRoomRouter(t)

	h.as(bson.NewObjectID().Hex(), "alice")
	room := h.createRoomVia(t, 2)
	assert.Equal(t, warroom.StatusWaiting, room.Status)
	assert.Len(t, room.Participants, 1)

	h.as(bson.NewObjectID().Hex(), "bob")
	w := h.do(http.MethodPost, "/api/warrooms/"+room.RoomCode+"/join", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp struct {
		Data warroom.Room `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, warroom.StatusInProgress, resp.Data.Status, "a full room starts at once")
	assert.Len(t, resp.Data.Participants, 2)
	require.NotNil(t, resp.Data.StartedAt)
}

func TestWarRoom_RejectsInvalidRoomSize(t *testing.T) {
	clearWarRooms(t)
	h := setupWarRoomRouter(t)
	h.as(bson.NewObjectID().Hex(), "alice")

	w := h.do(http.MethodPost, "/api/warrooms", `{"maxParticipants":7}`)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWarRoom_JoiningAFullRoomIsRejected(t *testing.T) {
	clearWarRooms(t)
	h := setupWarRoomRouter(t)

	h.as(bson.NewObjectID().Hex(), "alice")
	room := h.createRoomVia(t, 2)
	h.as(bson.NewObjectID().Hex(), "bob")
	require.Equal(t, http.StatusOK, h.do(http.MethodPost, "/api/warrooms/"+room.RoomCode+"/join", "").Code)

	h.as(bson.NewObjectID().Hex(), "carol")
	w := h.do(http.MethodPost, "/api/warrooms/"+room.RoomCode+"/join", "")

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestWarRoom_UnknownCodeReturns404(t *testing.T) {
	clearWarRooms(t)
	h := setupWarRoomRouter(t)
	h.as(bson.NewObjectID().Hex(), "alice")

	assert.Equal(t, http.StatusNotFound, h.do(http.MethodGet, "/api/warrooms/ZZZZZZ", "").Code)
}

func TestWarRoom_LobbyListsOnlyWaitingRooms(t *testing.T) {
	clearWarRooms(t)
	h := setupWarRoomRouter(t)

	h.as(bson.NewObjectID().Hex(), "alice")
	open := h.createRoomVia(t, 3)
	full := h.createRoomVia(t, 2)
	h.as(bson.NewObjectID().Hex(), "bob")
	require.Equal(t, http.StatusOK, h.do(http.MethodPost, "/api/warrooms/"+full.RoomCode+"/join", "").Code)

	w := h.do(http.MethodGet, "/api/warrooms", "")
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data []warroom.Room `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	codes := map[string]bool{}
	for _, r := range resp.Data {
		codes[r.RoomCode] = true
	}
	assert.True(t, codes[open.RoomCode], "a room still waiting is advertised")
	assert.False(t, codes[full.RoomCode], "a race in progress is not")
}

// =============================================================
// Submitting inside a race
// =============================================================

// TestWarRoom_SubmissionUsesThePriorityLane is the point of the whole
// two-lane design: a race must not queue behind practice traffic.
func TestWarRoom_SubmissionUsesThePriorityLane(t *testing.T) {
	clearWarRooms(t)
	h := setupWarRoomRouter(t)

	alice := bson.NewObjectID().Hex()
	h.as(alice, "alice")
	room := h.createRoomVia(t, 2)
	h.as(bson.NewObjectID().Hex(), "bob")
	require.Equal(t, http.StatusOK, h.do(http.MethodPost, "/api/warrooms/"+room.RoomCode+"/join", "").Code)

	h.as(alice, "alice")
	w := h.do(http.MethodPost, "/api/warrooms/"+room.RoomCode+"/submit",
		`{"language":"python","code":"print(3)"}`)

	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	require.Len(t, h.publisher.lanes, 1)
	assert.Equal(t, queue.LaneWarRoom, h.publisher.lanes[0],
		"war room submissions bypass the standard queue")
	assert.Equal(t, room.ID, h.publisher.jobs[0].WarRoomID)
}

func TestWarRoom_OutsidersCannotSubmit(t *testing.T) {
	clearWarRooms(t)
	h := setupWarRoomRouter(t)

	h.as(bson.NewObjectID().Hex(), "alice")
	room := h.createRoomVia(t, 2)
	h.as(bson.NewObjectID().Hex(), "bob")
	require.Equal(t, http.StatusOK, h.do(http.MethodPost, "/api/warrooms/"+room.RoomCode+"/join", "").Code)

	h.as(bson.NewObjectID().Hex(), "mallory")
	w := h.do(http.MethodPost, "/api/warrooms/"+room.RoomCode+"/submit",
		`{"language":"python","code":"print(3)"}`)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Empty(t, h.publisher.jobs, "nothing is queued for a non-participant")
}

func TestWarRoom_CannotSubmitBeforeTheRaceStarts(t *testing.T) {
	clearWarRooms(t)
	h := setupWarRoomRouter(t)

	alice := bson.NewObjectID().Hex()
	h.as(alice, "alice")
	room := h.createRoomVia(t, 2)

	// The room is still waiting for a second player.
	w := h.do(http.MethodPost, "/api/warrooms/"+room.RoomCode+"/submit",
		`{"language":"python","code":"print(3)"}`)

	assert.Equal(t, http.StatusConflict, w.Code)
}

// =============================================================
// Winner determination
// =============================================================

// TestWarRoom_WinnerIsTheFirstAcceptedSubmission exercises the path that
// actually decides a race: judge worker → notifier → room.
func TestWarRoom_WinnerIsTheFirstAcceptedSubmission(t *testing.T) {
	clearWarRooms(t)
	h := setupWarRoomRouter(t)
	bus := realtime.NewMemoryBus()
	notifier := warroom.NewJudgeNotifier(h.rooms, bus)

	alice := bson.NewObjectID().Hex()
	bob := bson.NewObjectID().Hex()
	h.as(alice, "alice")
	room := h.createRoomVia(t, 2)
	h.as(bob, "bob")
	require.Equal(t, http.StatusOK, h.do(http.MethodPost, "/api/warrooms/"+room.RoomCode+"/join", "").Code)

	judgedAt := time.Now().UTC()
	bobWin := &submission.Submission{
		ID: "sub-bob", UserID: bob, WarRoomID: room.ID,
		Language: "python", Status: submission.StatusAccepted, JudgedAt: &judgedAt,
	}
	later := judgedAt.Add(2 * time.Second)
	aliceWin := &submission.Submission{
		ID: "sub-alice", UserID: alice, WarRoomID: room.ID,
		Language: "python", Status: submission.StatusAccepted, JudgedAt: &later,
	}

	notifier.SubmissionJudged(context.Background(), bobWin)
	notifier.SubmissionJudged(context.Background(), aliceWin)

	final, err := h.rooms.GetByID(context.Background(), room.ID)
	require.NoError(t, err)
	assert.Equal(t, warroom.StatusFinished, final.Status)
	assert.Equal(t, bob, final.WinnerID, "the first accepted submission wins")
	assert.Equal(t, "bob", final.WinnerUsername)
	require.NotNil(t, final.EndedAt)
}
