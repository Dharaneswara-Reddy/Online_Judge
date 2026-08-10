package tests

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/toji339/online-judge/internal/judge"
	"github.com/toji339/online-judge/internal/problem"
	problemmongo "github.com/toji339/online-judge/internal/problem/mongorepo"
	"github.com/toji339/online-judge/internal/queue"
	"github.com/toji339/online-judge/internal/submission"
	submissionmongo "github.com/toji339/online-judge/internal/submission/mongorepo"
	"github.com/toji339/online-judge/internal/worker"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// =============================================================
// Test doubles
// =============================================================

// scriptedSandbox returns pre-programmed execution results, letting the
// submission endpoints be tested end-to-end without Docker.
type scriptedSandbox struct {
	compile judge.ExecuteResult
	runs    []judge.ExecuteResult
	err     error
}

func (s *scriptedSandbox) NewSubmission(context.Context, string, string, judge.Limits) (judge.SubmissionSandbox, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &scriptedSubmission{parent: s}, nil
}

type scriptedSubmission struct {
	parent *scriptedSandbox
	next   int
}

func (s *scriptedSubmission) Compile(context.Context) (judge.ExecuteResult, error) {
	return s.parent.compile, nil
}

func (s *scriptedSubmission) Run(context.Context, string) (judge.ExecuteResult, error) {
	// Repeat the last scripted result if the problem has more test cases
	// than the script provides, so tests only script what they care about.
	if s.next >= len(s.parent.runs) {
		return s.parent.runs[len(s.parent.runs)-1], nil
	}
	r := s.parent.runs[s.next]
	s.next++
	return r, nil
}

func (s *scriptedSubmission) Close(context.Context) error { return nil }

// acceptingSandbox echoes the expected output of the seeded test case.
func acceptingSandbox() *scriptedSandbox {
	return &scriptedSandbox{runs: []judge.ExecuteResult{{Stdout: "3\n", ExitCode: 0, RuntimeMS: 7}}}
}

// =============================================================
// Harness
// =============================================================

// submissionHarness bundles the router and services a submission test
// needs, all pointed at the shared test database.
type submissionHarness struct {
	router        *gin.Engine
	problemSvc    *problem.Service
	submissionSvc *submission.Service
	userID        string
}

// capturingPublisher records the jobs the controller queues instead of
// talking to a real broker.
type capturingPublisher struct {
	jobs  []queue.Job
	lanes []queue.Lane
	err   error
}

func (p *capturingPublisher) Publish(_ context.Context, lane queue.Lane, job queue.Job) error {
	if p.err != nil {
		return p.err
	}
	p.lanes = append(p.lanes, lane)
	p.jobs = append(p.jobs, job)
	return nil
}

func (p *capturingPublisher) Close() error { return nil }

// setupSubmissionRouter mounts the submission and profile routes with a
// stub authentication layer, so tests control which user is calling.
// Passing a nil publisher exercises the inline judging fallback.
func setupSubmissionRouter(t *testing.T, sandbox judge.Sandbox) *submissionHarness {
	t.Helper()
	return setupSubmissionRouterWithQueue(t, sandbox, nil)
}

func setupSubmissionRouterWithQueue(t *testing.T, sandbox judge.Sandbox, publisher queue.Publisher) *submissionHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	problemSvc := problem.NewService(problemmongo.New(testDB))
	submissionSvc := submission.NewService(submissionmongo.New(testDB))
	userID := bson.NewObjectID().Hex()

	router := gin.New()
	// Stand-in for AuthMiddleware: the JWT path is already covered by the
	// auth controller tests, so here we just assert an identity.
	router.Use(func(c *gin.Context) {
		c.Set("userID", userID)
		c.Set("role", "user")
		c.Next()
	})

	processor := worker.NewProcessor(submissionSvc, problemSvc, sandbox, nil)
	submissionController := controllers.NewSubmissionController(problemSvc, submissionSvc, publisher, processor)
	userController := controllers.NewUserController(testDB, submissionSvc, problemSvc)

	router.POST("/api/problems/:slug/submit", submissionController.Submit)
	router.GET("/api/submissions/:id", submissionController.GetSubmission)
	router.GET("/api/users/me/submissions", submissionController.ListMySubmissions)
	router.GET("/api/users/me/stats", userController.GetStats)

	return &submissionHarness{router: router, problemSvc: problemSvc, submissionSvc: submissionSvc, userID: userID}
}

// seedProblem creates a problem with one hidden test case expecting "3".
func seedProblem(t *testing.T, svc *problem.Service) *problem.Problem {
	t.Helper()
	ctx := context.Background()
	p, err := svc.Create(ctx, problem.CreateProblemInput{
		Title:         fmt.Sprintf("Sum Two Numbers %d", time.Now().UnixNano()),
		Statement:     "Read two integers and print their sum.",
		Difficulty:    problem.DifficultyEasy,
		Tags:          []string{"math"},
		TimeLimitMS:   1000,
		MemoryLimitMB: 64,
	})
	require.NoError(t, err)

	require.NoError(t, svc.AddTestCase(ctx, &problem.TestCase{
		ProblemID: p.ID, Input: "1 2\n", ExpectedOutput: "3\n", IsSample: false,
	}))
	return p
}

// clearSubmissions wipes submission and problem state between tests.
func clearSubmissions(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	_, err := testDB.Collection("submissions").DeleteMany(ctx, bson.M{})
	require.NoError(t, err)
	_, err = testDB.Collection("problems").DeleteMany(ctx, bson.M{})
	require.NoError(t, err)
	_, err = testDB.Collection("test_cases").DeleteMany(ctx, bson.M{})
	require.NoError(t, err)
}

func postSubmit(t *testing.T, h *submissionHarness, slug, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/problems/"+slug+"/submit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	return w
}

// =============================================================
// Submit endpoint tests
// =============================================================

// TestSubmit_AcceptedIsPersisted covers the happy path: a correct
// solution gets an accepted verdict and a durable submission record.
func TestSubmit_AcceptedIsPersisted(t *testing.T) {
	clearSubmissions(t)
	h := setupSubmissionRouter(t, acceptingSandbox())
	p := seedProblem(t, h.problemSvc)

	w := postSubmit(t, h, p.Slug, `{"language":"python","code":"print(3)"}`)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp struct {
		Success      bool   `json:"success"`
		SubmissionID string `json:"submissionId"`
		Status       string `json:"status"`
		TotalCases   int    `json:"totalCases"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	assert.Equal(t, "accepted", resp.Status)
	assert.Equal(t, 1, resp.TotalCases)
	require.NotEmpty(t, resp.SubmissionID, "the submission must be persisted and its id returned")

	stored, err := h.submissionSvc.GetByID(context.Background(), resp.SubmissionID)
	require.NoError(t, err)
	assert.Equal(t, submission.StatusAccepted, stored.Status)
	assert.Equal(t, h.userID, stored.UserID)
	assert.Equal(t, "print(3)", stored.Code, "the source is stored for the history page")
	require.NotNil(t, stored.JudgedAt, "judged_at is stamped by the server")
}

// TestSubmit_WrongAnswerIsPersisted covers a failing verdict.
func TestSubmit_WrongAnswerIsPersisted(t *testing.T) {
	clearSubmissions(t)
	sandbox := &scriptedSandbox{runs: []judge.ExecuteResult{{Stdout: "4\n", ExitCode: 0}}}
	h := setupSubmissionRouter(t, sandbox)
	p := seedProblem(t, h.problemSvc)

	w := postSubmit(t, h, p.Slug, `{"language":"python","code":"print(4)"}`)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		SubmissionID string `json:"submissionId"`
		Status       string `json:"status"`
		FailedCase   int    `json:"failedCase"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "wrong_answer", resp.Status)
	assert.Equal(t, 0, resp.FailedCase)
}

// TestSubmit_QueuesInsteadOfJudgingInline covers the production path:
// with a broker available the API accepts the work and returns at once.
func TestSubmit_QueuesInsteadOfJudgingInline(t *testing.T) {
	clearSubmissions(t)
	publisher := &capturingPublisher{}
	h := setupSubmissionRouterWithQueue(t, acceptingSandbox(), publisher)
	p := seedProblem(t, h.problemSvc)

	w := postSubmit(t, h, p.Slug, `{"language":"python","code":"print(3)"}`)

	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())

	var resp struct {
		SubmissionID string `json:"submissionId"`
		Status       string `json:"status"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "pending", resp.Status, "the verdict is not known yet")

	require.Len(t, publisher.jobs, 1, "the job reaches the broker")
	assert.Equal(t, resp.SubmissionID, publisher.jobs[0].SubmissionID)
	assert.Equal(t, queue.LaneStandard, publisher.lanes[0],
		"practice submissions use the standard lane")

	// The record exists and is waiting for a worker.
	stored, err := h.submissionSvc.GetByID(context.Background(), resp.SubmissionID)
	require.NoError(t, err)
	assert.Equal(t, submission.StatusPending, stored.Status)
}

// TestSubmit_FallsBackToInlineJudgingWhenBrokerFails keeps the platform
// usable when RabbitMQ is down.
func TestSubmit_FallsBackToInlineJudgingWhenBrokerFails(t *testing.T) {
	clearSubmissions(t)
	publisher := &capturingPublisher{err: errors.New("broker is down")}
	h := setupSubmissionRouterWithQueue(t, acceptingSandbox(), publisher)
	p := seedProblem(t, h.problemSvc)

	w := postSubmit(t, h, p.Slug, `{"language":"python","code":"print(3)"}`)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "accepted", resp.Status, "the submission is judged inline instead")
}

// TestSubmit_RejectsSecondPendingSubmission verifies admission control
// through the HTTP layer: a queued submission blocks the next one.
func TestSubmit_RejectsSecondPendingSubmission(t *testing.T) {
	clearSubmissions(t)
	h := setupSubmissionRouterWithQueue(t, acceptingSandbox(), &capturingPublisher{})
	p := seedProblem(t, h.problemSvc)

	require.Equal(t, http.StatusAccepted, postSubmit(t, h, p.Slug, `{"language":"python","code":"print(3)"}`).Code)
	w := postSubmit(t, h, p.Slug, `{"language":"python","code":"print(3)"}`)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
}

// TestSubmit_RejectsUnsupportedLanguage is the validation-failure case.
func TestSubmit_RejectsUnsupportedLanguage(t *testing.T) {
	clearSubmissions(t)
	h := setupSubmissionRouter(t, acceptingSandbox())
	p := seedProblem(t, h.problemSvc)

	w := postSubmit(t, h, p.Slug, `{"language":"cobol","code":"DISPLAY 3."}`)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestSubmit_UnknownProblemReturns404 is an edge case.
func TestSubmit_UnknownProblemReturns404(t *testing.T) {
	clearSubmissions(t)
	h := setupSubmissionRouter(t, acceptingSandbox())

	w := postSubmit(t, h, "no-such-problem", `{"language":"python","code":"print(3)"}`)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestSubmit_ProblemWithoutTestCasesNeverReportsAccepted guards the judge
// against silently accepting anything when a problem has no cases.
//
// The check lives in the judging pipeline rather than the HTTP handler,
// because in production the worker — not the API — loads the test cases.
// The submission therefore ends in a terminal non-accepted state.
func TestSubmit_ProblemWithoutTestCasesNeverReportsAccepted(t *testing.T) {
	clearSubmissions(t)
	h := setupSubmissionRouter(t, acceptingSandbox())
	p, err := h.problemSvc.Create(context.Background(), problem.CreateProblemInput{
		Title: "Empty Problem", Statement: "x", Difficulty: problem.DifficultyEasy,
		TimeLimitMS: 1000, MemoryLimitMB: 64,
	})
	require.NoError(t, err)

	w := postSubmit(t, h, p.Slug, `{"language":"python","code":"print(3)"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp struct {
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEqual(t, "accepted", resp.Status, "an untested solution must never be accepted")

	// The attempt must not be left pending forever either.
	items, err := h.submissionSvc.List(context.Background(), submission.ListFilter{UserID: h.userID})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.True(t, items[0].Status.IsTerminal())
}

// =============================================================
// History and stats endpoint tests
// =============================================================

// TestListMySubmissions_ReturnsHistoryWithoutCode verifies the history
// endpoint pages correctly and omits source code from list rows.
func TestListMySubmissions_ReturnsHistoryWithoutCode(t *testing.T) {
	clearSubmissions(t)
	h := setupSubmissionRouter(t, acceptingSandbox())
	p := seedProblem(t, h.problemSvc)

	for range 3 {
		require.Equal(t, http.StatusOK, postSubmit(t, h, p.Slug, `{"language":"python","code":"print(3)"}`).Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/users/me/submissions?pageSize=2", nil)
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data  []submission.Submission `json:"data"`
		Total int                     `json:"total"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 3, resp.Total)
	assert.Len(t, resp.Data, 2, "page size is honoured")
	for _, s := range resp.Data {
		assert.Empty(t, s.Code, "list rows must not ship the source code")
		assert.Equal(t, p.Slug, s.ProblemSlug)
	}
}

// TestGetSubmission_RejectsAnotherUsersSubmission is the authorization
// edge case — source code must never leak between users.
func TestGetSubmission_RejectsAnotherUsersSubmission(t *testing.T) {
	clearSubmissions(t)
	h := setupSubmissionRouter(t, acceptingSandbox())
	p := seedProblem(t, h.problemSvc)

	// A submission belonging to somebody else entirely.
	other, err := h.submissionSvc.Create(context.Background(), submission.CreateInput{
		UserID: bson.NewObjectID().Hex(), ProblemID: p.ID, ProblemSlug: p.Slug,
		Language: "python", Code: "secret",
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/submissions/"+other.ID, nil)
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.NotContains(t, w.Body.String(), "secret")
}

// TestGetStats_CountsSolvedByDifficulty checks the profile statistics.
func TestGetStats_CountsSolvedByDifficulty(t *testing.T) {
	clearSubmissions(t)
	h := setupSubmissionRouter(t, acceptingSandbox())
	p := seedProblem(t, h.problemSvc)

	require.Equal(t, http.StatusOK, postSubmit(t, h, p.Slug, `{"language":"python","code":"print(3)"}`).Code)

	req := httptest.NewRequest(http.MethodGet, "/api/users/me/stats", nil)
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Data struct {
			TotalSubmissions   int            `json:"totalSubmissions"`
			Accepted           int            `json:"accepted"`
			Solved             int            `json:"solved"`
			SolvedByDifficulty map[string]int `json:"solvedByDifficulty"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 1, resp.Data.TotalSubmissions)
	assert.Equal(t, 1, resp.Data.Accepted)
	assert.Equal(t, 1, resp.Data.Solved)
	assert.Equal(t, 1, resp.Data.SolvedByDifficulty["easy"])
	assert.Equal(t, 0, resp.Data.SolvedByDifficulty["hard"])
}
