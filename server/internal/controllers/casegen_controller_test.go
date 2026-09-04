package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/assist"
	"github.com/toji339/online-judge/internal/casegen"
	"github.com/toji339/online-judge/internal/playground"
	"github.com/toji339/online-judge/internal/problem"
	"github.com/toji339/online-judge/internal/problem/problemtest"
)

// These tests are about the edge of the case generator, not about the
// generator itself — internal/casegen has its own suite for parsing,
// capping and verification. What is checked here is what that package
// cannot check for itself: which problem the cases are for, that the
// problem's current cases reach the prompt, and that a generation writes
// nothing to the database.

// recordingProvider answers with a canned reply and keeps the prompts,
// so a test can assert what the problem's stored cases turned into.
type recordingProvider struct {
	reply   string
	err     error
	prompts []assist.Prompt
}

func (p *recordingProvider) Complete(_ context.Context, prompt assist.Prompt) (string, error) {
	p.prompts = append(p.prompts, prompt)
	return p.reply, p.err
}

// scriptedRunner stands in for the sandbox and never starts one.
type scriptedRunner struct {
	stdout string
	err    error
	runs   []playground.Request
}

func (r *scriptedRunner) Run(_ context.Context, req playground.Request) (playground.Response, error) {
	r.runs = append(r.runs, req)
	if r.err != nil {
		return playground.Response{}, r.err
	}
	return playground.Response{Stdout: r.stdout}, nil
}

const (
	casegenHiddenInput    = "4\n8 8 8 8"
	casegenHiddenExpected = "32"
	casegenReply          = `{"cases":[{"input":"2\n1 1","expectedOutput":"MODEL-GUESS","rationale":"a tie"}],
	                          "ambiguities":["Both index pairs are correct, but the judge compares text exactly."]}`
)

type casegenRig struct {
	router    *gin.Engine
	problems  *problem.Service
	provider  *recordingProvider
	runner    *scriptedRunner
	problemID string
}

// newCasegenRig builds an admin-authenticated router over the real
// problem service and in-memory repositories.
func newCasegenRig(t *testing.T, provider assist.Provider, runner playground.Runner) *casegenRig {
	t.Helper()
	gin.SetMode(gin.TestMode)

	problems := problem.NewService(problemtest.NewFakeRepository())
	prob, err := problems.Create(context.Background(), problem.CreateProblemInput{
		Title:         "Sum The Interesting Ones",
		Statement:     "Given n numbers, print the sum of those above the mean.",
		Difficulty:    problem.DifficultyEasy,
		Tags:          []string{"array"},
		TimeLimitMS:   1000,
		MemoryLimitMB: 256,
	})
	require.NoError(t, err)

	require.NoError(t, problems.AddTestCase(context.Background(), &problem.TestCase{
		ProblemID: prob.ID, Input: "1\n5", ExpectedOutput: "5", IsSample: true,
	}))
	require.NoError(t, problems.AddTestCase(context.Background(), &problem.TestCase{
		ProblemID: prob.ID, Input: casegenHiddenInput, ExpectedOutput: casegenHiddenExpected, IsSample: false,
	}))

	ctrl := NewCaseGenController(casegen.NewGenerator(provider, runner), problems)

	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("userID", "admin-1") })
	router.POST("/api/admin/problems/:id/assist/testcases", ctrl.GenerateTestCases)

	rig := &casegenRig{router: router, problems: problems, problemID: prob.ID}
	if p, ok := provider.(*recordingProvider); ok {
		rig.provider = p
	}
	if r, ok := runner.(*scriptedRunner); ok {
		rig.runner = r
	}
	return rig
}

func (r *casegenRig) generate(t *testing.T, id string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/problems/"+id+"/assist/testcases", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.router.ServeHTTP(rec, req)
	return rec
}

func validCasegenBody() gin.H {
	return gin.H{
		"referenceSolution": "import sys\nprint(sum(map(int, sys.stdin.read().split())))",
		"language":          "python",
		"count":             1,
	}
}

// A deployment with no key must hide the feature, not fail it.
func TestGenerateTestCasesIsUnavailableWhenDisabled(t *testing.T) {
	rig := newCasegenRig(t, nil, &scriptedRunner{stdout: "0\n"})

	rec := rig.generate(t, rig.problemID, validCasegenBody())

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestGenerateTestCasesReturnsVerifiedProposals(t *testing.T) {
	rig := newCasegenRig(t,
		&recordingProvider{reply: casegenReply},
		&scriptedRunner{stdout: "EXECUTED-TRUTH\n"})

	rec := rig.generate(t, rig.problemID, validCasegenBody())
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Success bool           `json:"success"`
		Data    casegen.Result `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	assert.True(t, body.Success)
	require.Len(t, body.Data.Proposals, 1)
	assert.Equal(t, "2\n1 1\n", body.Data.Proposals[0].Input)
	assert.Equal(t, "EXECUTED-TRUTH", body.Data.Proposals[0].ExpectedOutput)
	assert.True(t, body.Data.Proposals[0].Verified)
	assert.Len(t, body.Data.Ambiguities, 1)

	// The model's own answer must not appear anywhere in the response.
	assert.NotContains(t, rec.Body.String(), "MODEL-GUESS")

	// The reference solution is what was executed, on the model's input.
	require.Len(t, rig.runner.runs, 1)
	assert.Equal(t, playground.ModeRaw, rig.runner.runs[0].Mode)
	assert.Contains(t, rig.runner.runs[0].Code, "sys.stdin")
	assert.Equal(t, "2\n1 1\n", rig.runner.runs[0].Stdin)
}

// The endpoint is a proposal desk. A generation that never gets reviewed
// must leave the problem exactly as it was.
func TestGenerateTestCasesWritesNothingToTheProblem(t *testing.T) {
	rig := newCasegenRig(t,
		&recordingProvider{reply: casegenReply},
		&scriptedRunner{stdout: "EXECUTED-TRUTH\n"})

	before, err := rig.problems.ListAllTestCases(context.Background(), rig.problemID)
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, rig.generate(t, rig.problemID, validCasegenBody()).Code)

	after, err := rig.problems.ListAllTestCases(context.Background(), rig.problemID)
	require.NoError(t, err)
	assert.Len(t, after, len(before))
}

// The existing cases are the reason the model does not re-propose what
// the problem already has, so the path that loads them is worth pinning.
func TestGenerateTestCasesShowsTheModelTheExistingCases(t *testing.T) {
	rig := newCasegenRig(t,
		&recordingProvider{reply: casegenReply},
		&scriptedRunner{stdout: "9\n"})

	require.Equal(t, http.StatusOK, rig.generate(t, rig.problemID, validCasegenBody()).Code)

	require.Len(t, rig.provider.prompts, 1)
	user := rig.provider.prompts[0].User
	assert.Contains(t, user, casegenHiddenInput)
	assert.Contains(t, user, casegenHiddenExpected)
	assert.Contains(t, user, "Sum The Interesting Ones")
}

func TestGenerateTestCasesRejectsAnEmptyReferenceSolution(t *testing.T) {
	rig := newCasegenRig(t, &recordingProvider{reply: casegenReply}, &scriptedRunner{stdout: "9\n"})

	rec := rig.generate(t, rig.problemID, gin.H{"referenceSolution": "  ", "language": "python", "count": 1})

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGenerateTestCasesRejectsAnUnsupportedLanguage(t *testing.T) {
	rig := newCasegenRig(t, &recordingProvider{reply: casegenReply}, &scriptedRunner{stdout: "9\n"})

	body := validCasegenBody()
	body["language"] = "brainfuck"
	rec := rig.generate(t, rig.problemID, body)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGenerateTestCasesRejectsAnUnknownProblem(t *testing.T) {
	rig := newCasegenRig(t, &recordingProvider{reply: casegenReply}, &scriptedRunner{stdout: "9\n"})

	rec := rig.generate(t, "no-such-problem", validCasegenBody())

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// A malformed generation is not a disabled feature. The client hides the
// whole tool on a 503, so only an unconfigured deployment may use it.
func TestGenerateTestCasesReportsABadReplyAsAGatewayFailure(t *testing.T) {
	rig := newCasegenRig(t,
		&recordingProvider{reply: "Sure, here are some ideas!"},
		&scriptedRunner{stdout: "9\n"})

	rec := rig.generate(t, rig.problemID, validCasegenBody())

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.NotEqual(t, http.StatusServiceUnavailable, rec.Code)
}

func TestGenerateTestCasesReportsAnUnreachableProviderAsAGatewayFailure(t *testing.T) {
	rig := newCasegenRig(t,
		&recordingProvider{err: errors.New("connection refused")},
		&scriptedRunner{stdout: "9\n"})

	rec := rig.generate(t, rig.problemID, validCasegenBody())

	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

// No sandbox is a legal deployment. The proposals still come back, and
// they say plainly that nobody ran them.
func TestGenerateTestCasesStillAnswersWithoutASandbox(t *testing.T) {
	rig := newCasegenRig(t, &recordingProvider{reply: casegenReply}, nil)

	rec := rig.generate(t, rig.problemID, validCasegenBody())
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Data casegen.Result `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Data.Proposals, 1)
	assert.False(t, body.Data.Proposals[0].Verified)
	assert.NotEmpty(t, body.Data.Proposals[0].Error)
	assert.Empty(t, body.Data.Proposals[0].ExpectedOutput)
}
