package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/toji339/online-judge/internal/problem"
	"github.com/toji339/online-judge/internal/problem/problemtest"
	"github.com/toji339/online-judge/internal/realtime"
	"github.com/toji339/online-judge/internal/warroom"
	"github.com/toji339/online-judge/internal/warroom/warroomtest"
)

// GET /api/warrooms/:code is authenticated but has no participant check,
// so any signed-in user holding a room code sees the room. This test
// pins what that actually discloses: room metadata and the roster, and
// nothing a participant would consider private — no source code, no
// hidden test data, no submission bodies. If the payload ever grows to
// carry any of that, this fails and the endpoint needs the check.
func TestGetRoom_DisclosesOnlyRoomMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)

	problems := problem.NewService(problemtest.NewFakeRepository())
	created, err := problems.Create(t.Context(), problem.CreateProblemInput{
		Title:         "Two Sum",
		Statement:     "Given an array of integers...",
		Difficulty:    problem.DifficultyEasy,
		TimeLimitMS:   2000,
		MemoryLimitMB: 256,
	})
	if err != nil {
		t.Fatalf("create problem: %v", err)
	}
	// A hidden test case exists for the problem the room is racing on.
	if err := problems.AddTestCase(t.Context(), &problem.TestCase{
		ProblemID:      created.ID,
		Input:          "4 8\n1 5 2 7",
		ExpectedOutput: "0 3",
		IsSample:       false,
	}); err != nil {
		t.Fatalf("add test case: %v", err)
	}

	rooms := warroom.NewService(warroomtest.New(), problems)
	room, err := rooms.Create(t.Context(), warroom.CreateInput{
		ProblemSlug:     created.Slug,
		MaxParticipants: 2,
		Creator:         warroom.Participant{UserID: "u1", Username: "alice"},
	})
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	controller := NewWarRoomController(rooms, realtime.NewMemoryBus(), "http://localhost:5173")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/warrooms/"+room.RoomCode, nil)
	c.Params = gin.Params{{Key: "code", Value: room.RoomCode}}
	// A signed-in user who is not in the room.
	c.Set("userID", "outsider")
	c.Set("username", "mallory")

	controller.GetRoom(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", recorder.Code, recorder.Body.String())
	}

	var body struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}

	allowed := map[string]bool{
		"id": true, "roomCode": true, "problemId": true, "problemSlug": true,
		"problemTitle": true, "difficulty": true, "participants": true,
		"maxParticipants": true, "status": true, "winnerId": true,
		"winnerUsername": true, "startedAt": true, "endedAt": true,
		"createdAt": true,
	}
	for key := range body.Data {
		if !allowed[key] {
			t.Errorf("the room payload carries an unreviewed field %q — check it discloses nothing private", key)
		}
	}

	// Belt and braces: nothing resembling a test case or a submission may
	// appear anywhere in the serialised response.
	raw := recorder.Body.String()
	for _, forbidden := range []string{"expectedOutput", "testCase", "testCases", "sourceCode", "code\":", "submissions"} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("the room payload contains %q: %s", forbidden, raw)
		}
	}

	participants, ok := body.Data["participants"].([]any)
	if !ok || len(participants) != 1 {
		t.Fatalf("participants = %v, want one entry", body.Data["participants"])
	}
	first, _ := participants[0].(map[string]any)
	for key := range first {
		switch key {
		case "userId", "username", "joinedAt":
		default:
			t.Errorf("participant carries an unreviewed field %q", key)
		}
	}
}
