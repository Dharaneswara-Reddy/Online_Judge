package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/assist"
	"github.com/toji339/online-judge/internal/problem"
	"github.com/toji339/online-judge/internal/problem/problemtest"
	"github.com/toji339/online-judge/internal/submission"
	"github.com/toji339/online-judge/internal/submission/submissiontest"
)

// These tests are about the edge, not the model. The assist package has
// its own suite for prompts and filters; what is checked here is the
// part that package cannot check for itself — who is allowed to ask,
// which verdict is allowed to be discussed, and what is allowed to come
// back out.

// The hidden case used throughout. It is deliberately distinctive so a
// leak is unambiguous when a response body is searched for it.
const (
	hiddenInput    = "6\n17 42 99 13 58 21"
	hiddenExpected = "250"
)

// stubProvider answers with whatever it is given, without a network.
type stubProvider struct {
	reply string
	err   error
}

func (s stubProvider) Complete(context.Context, assist.Prompt) (string, error) {
	return s.reply, s.err
}

type assistRig struct {
	router      *gin.Engine
	problems    *problem.Service
	submissions *submission.Service
	problemID   string
	slug        string
}

// newAssistRig builds a router with the real services over in-memory
// repositories, and authenticates every request as userID.
func newAssistRig(t *testing.T, provider assist.Provider, userID string) *assistRig {
	t.Helper()
	gin.SetMode(gin.TestMode)

	problems := problem.NewService(problemtest.NewFakeRepository())
	submissions := submission.NewService(submissiontest.New())

	prob, err := problems.Create(context.Background(), problem.CreateProblemInput{
		Title:         "Sum The Interesting Ones",
		Statement:     "Given n numbers, print the sum of those above the mean.",
		Difficulty:    problem.DifficultyEasy,
		Tags:          []string{"array"},
		TimeLimitMS:   1000,
		MemoryLimitMB: 256,
	})
	require.NoError(t, err)

	// One sample and one hidden case, so index 1 is genuinely hidden.
	require.NoError(t, problems.AddTestCase(context.Background(), &problem.TestCase{
		ProblemID: prob.ID, Input: "1\n5", ExpectedOutput: "5", IsSample: true,
	}))
	require.NoError(t, problems.AddTestCase(context.Background(), &problem.TestCase{
		ProblemID: prob.ID, Input: hiddenInput, ExpectedOutput: hiddenExpected, IsSample: false,
	}))

	// A cache, as production wires one. Without it the rig silently
	// tests a configuration nothing runs.
	ctrl := NewAssistController(
		assist.NewService(provider, assist.Options{
			Cache: assist.NewMemoryCache(time.Minute, 64),
		}), problems, submissions)

	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("userID", userID) })
	router.GET("/api/problems/:slug/assist/state", ctrl.State)
	router.POST("/api/assist/hint", ctrl.Hint)
	router.POST("/api/assist/explain", ctrl.Explain)
	router.POST("/api/assist/review", ctrl.Review)

	return &assistRig{
		router: router, problems: problems, submissions: submissions,
		problemID: prob.ID, slug: prob.Slug,
	}
}

// judge records a submission and drives it to a terminal verdict the way
// the real pipeline does, so the test never writes a status directly.
func (r *assistRig) judge(t *testing.T, userID string, status submission.Status, failedCase int) *submission.Submission {
	t.Helper()
	ctx := context.Background()

	sub, err := r.submissions.Create(ctx, submission.CreateInput{
		UserID: userID, ProblemID: r.problemID, ProblemSlug: r.slug,
		ProblemTitle: "Sum The Interesting Ones", Language: "python", Code: "print(0)",
	})
	require.NoError(t, err)
	require.NoError(t, r.submissions.MarkRunning(ctx, sub.ID))
	require.NoError(t, r.submissions.MarkJudged(ctx, sub.ID, submission.Result{
		Status: status, RuntimeMS: 12, MemoryKB: 4096, FailedCase: failedCase, TotalCases: 2,
	}))

	judged, err := r.submissions.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	return judged
}

func (r *assistRig) post(t *testing.T, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.router.ServeHTTP(rec, req)
	return rec
}

func decodeData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body.Data
}

const okHint = "Look again at what the statement promises about ties, then re-read your comparison."

// --- the feature being switched off --------------------------------------

// TestStateReportsDisabledWithoutFailing: the client asks this question
// to decide whether to render anything, so a deployment with no key must
// answer it rather than error.
func TestStateReportsDisabledWithoutFailing(t *testing.T) {
	rig := newAssistRig(t, nil, "u1")

	req := httptest.NewRequest(http.MethodGet, "/api/problems/"+rig.slug+"/assist/state", nil)
	rec := httptest.NewRecorder()
	rig.router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, false, decodeData(t, rec)["enabled"])
}

func TestHintIsUnavailableWhenDisabled(t *testing.T) {
	rig := newAssistRig(t, nil, "u1")

	rec := rig.post(t, "/api/assist/hint", gin.H{
		"problemSlug": rig.slug, "rung": 1, "language": "python", "code": "pass",
	})

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestWithheldGenerationIsNotReportedAsDisabled is the distinction the
// client depends on. It hides the whole feature on a 503, so a single
// filtered response must not use that status — otherwise one bad
// generation removes the assistant for the rest of the session.
func TestWithheldGenerationIsNotReportedAsDisabled(t *testing.T) {
	rig := newAssistRig(t, stubProvider{reply: "Try:\n```python\nprint(sum(a))\n```"}, "u1")

	rec := rig.post(t, "/api/assist/hint", gin.H{
		"problemSlug": rig.slug, "rung": 1, "language": "python", "code": "pass",
	})

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.NotEqual(t, http.StatusServiceUnavailable, rec.Code)
}

// --- ownership -----------------------------------------------------------

// TestExplainRefusesSomeoneElsesSubmission: source code is owner-only in
// this codebase, and an assistant that will discuss any submission is a
// way to read any submission.
func TestExplainRefusesSomeoneElsesSubmission(t *testing.T) {
	rig := newAssistRig(t, stubProvider{reply: okHint}, "intruder")
	theirs := rig.judge(t, "victim", submission.StatusWrongAnswer, 1)

	rec := rig.post(t, "/api/assist/explain", gin.H{"submissionId": theirs.ID})

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.NotContains(t, rec.Body.String(), okHint)
}

func TestReviewRefusesSomeoneElsesSubmission(t *testing.T) {
	rig := newAssistRig(t, stubProvider{reply: okHint}, "intruder")
	theirs := rig.judge(t, "victim", submission.StatusAccepted, -1)

	rec := rig.post(t, "/api/assist/review", gin.H{"submissionId": theirs.ID})

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// --- what may be discussed ----------------------------------------------

// TestReviewRefusesAnUnsolvedSubmission is what makes reviews safe to
// offer at all: a full critique of code that has not passed is a
// solution with extra steps.
func TestReviewRefusesAnUnsolvedSubmission(t *testing.T) {
	rig := newAssistRig(t, stubProvider{reply: okHint}, "u1")
	mine := rig.judge(t, "u1", submission.StatusWrongAnswer, 1)

	rec := rig.post(t, "/api/assist/review", gin.H{"submissionId": mine.ID})

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestReviewAllowsAnAcceptedSubmission(t *testing.T) {
	rig := newAssistRig(t, stubProvider{reply: okHint}, "u1")
	mine := rig.judge(t, "u1", submission.StatusAccepted, -1)

	rec := rig.post(t, "/api/assist/review", gin.H{"submissionId": mine.ID})

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, okHint, decodeData(t, rec)["text"])
}

func TestExplainRefusesAnAcceptedSubmission(t *testing.T) {
	rig := newAssistRig(t, stubProvider{reply: okHint}, "u1")
	mine := rig.judge(t, "u1", submission.StatusAccepted, -1)

	rec := rig.post(t, "/api/assist/explain", gin.H{"submissionId": mine.ID})

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestExplainDescribesTheJudgesVerdict(t *testing.T) {
	rig := newAssistRig(t, stubProvider{reply: okHint}, "u1")
	mine := rig.judge(t, "u1", submission.StatusWrongAnswer, 1)

	rec := rig.post(t, "/api/assist/explain", gin.H{"submissionId": mine.ID})

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, okHint, decodeData(t, rec)["text"])
}

// --- the hidden case -----------------------------------------------------

// TestRungThreeNeedsAFailedSubmission: rung 3 describes the case a
// submission failed, so without a failure there is nothing to describe
// and nothing that should be fetched.
func TestRungThreeNeedsAFailedSubmission(t *testing.T) {
	rig := newAssistRig(t, stubProvider{reply: okHint}, "u1")

	rec := rig.post(t, "/api/assist/hint", gin.H{
		"problemSlug": rig.slug, "rung": 3, "language": "python", "code": "pass",
	})

	assert.Equal(t, http.StatusConflict, rec.Code)
}

// TestRungThreeNeverReturnsTheHiddenCase is the test this file exists
// for. The case enters the prompt, which is allowed; it must not leave
// in the response, which is not.
func TestRungThreeNeverReturnsTheHiddenCase(t *testing.T) {
	// A provider that does the worst thing available to it: echo the
	// case it was asked to describe.
	leaky := stubProvider{reply: "Your code fails on " + hiddenInput + " which sums to " + hiddenExpected + "."}
	rig := newAssistRig(t, leaky, "u1")
	rig.judge(t, "u1", submission.StatusWrongAnswer, 1)

	rec := rig.post(t, "/api/assist/hint", gin.H{
		"problemSlug": rig.slug, "rung": 3, "language": "python", "code": "pass",
	})

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.NotContains(t, rec.Body.String(), "17 42 99 13 58 21",
		"the hidden test case reached the client")
}

// TestRungThreeReturnsADescription is the same path with a provider that
// behaves, so the refusal above is known to be the filter working rather
// than the path being broken.
func TestRungThreeReturnsADescription(t *testing.T) {
	good := stubProvider{reply: "The case you fail has several values above the mean. What does your loop do when more than one qualifies?"}
	rig := newAssistRig(t, good, "u1")
	rig.judge(t, "u1", submission.StatusWrongAnswer, 1)

	rec := rig.post(t, "/api/assist/hint", gin.H{
		"problemSlug": rig.slug, "rung": 3, "language": "python", "code": "pass",
	})

	require.Equal(t, http.StatusOK, rec.Code)
	body := decodeData(t, rec)
	assert.Equal(t, float64(3), body["rung"])
	assert.NotContains(t, rec.Body.String(), "17 42 99 13 58 21")
}

// TestRungThreeIgnoresAnotherUsersSubmissionID closes the obvious way to
// aim the hidden-case lookup at somebody else's failure.
func TestRungThreeIgnoresAnotherUsersSubmissionID(t *testing.T) {
	rig := newAssistRig(t, stubProvider{reply: okHint}, "u1")
	theirs := rig.judge(t, "victim", submission.StatusWrongAnswer, 1)

	rec := rig.post(t, "/api/assist/hint", gin.H{
		"problemSlug": rig.slug, "rung": 3, "language": "python", "code": "pass",
		"submissionId": theirs.ID,
	})

	assert.Equal(t, http.StatusConflict, rec.Code)
}

// --- request validation --------------------------------------------------

func TestHintRejectsARungOffTheLadder(t *testing.T) {
	rig := newAssistRig(t, stubProvider{reply: okHint}, "u1")

	rec := rig.post(t, "/api/assist/hint", gin.H{
		"problemSlug": rig.slug, "rung": 7, "language": "python", "code": "pass",
	})

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHintReportsAnUnreachableProvider(t *testing.T) {
	rig := newAssistRig(t, stubProvider{err: errors.New("dial tcp: refused")}, "u1")

	rec := rig.post(t, "/api/assist/hint", gin.H{
		"problemSlug": rig.slug, "rung": 1, "language": "python", "code": "pass",
	})

	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

// TestStateSurfacesTheStuckSignal walks the detector through the edge
// with real submissions rather than a synthetic history.
func TestStateSurfacesTheStuckSignal(t *testing.T) {
	rig := newAssistRig(t, stubProvider{reply: okHint}, "u1")
	for i := 0; i < 3; i++ {
		rig.judge(t, "u1", submission.StatusWrongAnswer, 1)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/problems/"+rig.slug+"/assist/state", nil)
	rec := httptest.NewRecorder()
	rig.router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	data := decodeData(t, rec)
	assert.Equal(t, true, data["enabled"])
	assert.Equal(t, true, data["stuck"])
	assert.Equal(t, float64(3), data["attempts"])
	assert.NotEmpty(t, data["reason"])
}

// --- post-acceptance review: the verdict gate ---------------------------
//
// The gate is the whole safety argument for this endpoint. A review
// discusses the code in full; on anything the judge has not accepted,
// that is a solution with commentary. So every non-accepted terminal
// verdict, and both transient states, must be refused.

func TestReviewRefusesEveryUnacceptedVerdict(t *testing.T) {
	cases := []struct {
		name   string
		status submission.Status
	}{
		{"wrong answer", submission.StatusWrongAnswer},
		{"time limit", submission.StatusTLE},
		{"memory limit", submission.StatusMLE},
		{"runtime error", submission.StatusRuntimeError},
		{"compile error", submission.StatusCompileError},
		{"output limit", submission.StatusOutputLimitExceeded},
		{"judge error", submission.StatusJudgeError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newAssistRig(t, stubProvider{reply: okHint}, "u1")
			mine := rig.judge(t, "u1", tc.status, 1)

			rec := rig.post(t, "/api/assist/review", gin.H{"submissionId": mine.ID})

			assert.Equal(t, http.StatusNotFound, rec.Code,
				"a %s submission was accepted for review", tc.name)
			assert.NotContains(t, rec.Body.String(), okHint)
		})
	}
}

// A submission still being judged has no verdict to stand on.
func TestReviewRefusesAPendingSubmission(t *testing.T) {
	rig := newAssistRig(t, stubProvider{reply: okHint}, "u1")

	sub, err := rig.submissions.Create(context.Background(), submission.CreateInput{
		UserID: "u1", ProblemID: rig.problemID, ProblemSlug: rig.slug,
		ProblemTitle: "Sum The Interesting Ones", Language: "python", Code: "print(0)",
	})
	require.NoError(t, err)

	rec := rig.post(t, "/api/assist/review", gin.H{"submissionId": sub.ID})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestReviewReportsACacheHit pins the field the UI shows and the
// telemetry records.
func TestReviewReportsACacheHit(t *testing.T) {
	rig := newAssistRig(t, stubProvider{reply: "Linear in time, constant in space."}, "u1")
	mine := rig.judge(t, "u1", submission.StatusAccepted, -1)

	first := rig.post(t, "/api/assist/review", gin.H{"submissionId": mine.ID})
	require.Equal(t, http.StatusOK, first.Code)
	assert.Equal(t, false, decodeData(t, first)["cached"])

	second := rig.post(t, "/api/assist/review", gin.H{"submissionId": mine.ID})
	require.Equal(t, http.StatusOK, second.Code)
	assert.Equal(t, true, decodeData(t, second)["cached"],
		"re-reviewing the same submission should not cost a second generation")
}

// TestReviewWithholdsARewrittenSolution drives the review filter through
// the edge, with a provider that returns exactly what the feature must
// never deliver.
func TestReviewWithholdsARewrittenSolution(t *testing.T) {
	rewrite := "Cleaner:\n\n```python\ndef solve(a):\n    total = 0\n    for x in a:\n        total += x\n    return total\n```"
	rig := newAssistRig(t, stubProvider{reply: rewrite}, "u1")
	mine := rig.judge(t, "u1", submission.StatusAccepted, -1)

	rec := rig.post(t, "/api/assist/review", gin.H{"submissionId": mine.ID})

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.NotContains(t, rec.Body.String(), "def solve")
}

// A review that starts talking about the judge's private data is
// withheld even though this endpoint is never given any.
func TestReviewWithholdsJudgeInternals(t *testing.T) {
	leak := "## Summary\nGood work. The hidden test case here uses a single element."
	rig := newAssistRig(t, stubProvider{reply: leak}, "u1")
	mine := rig.judge(t, "u1", submission.StatusAccepted, -1)

	rec := rig.post(t, "/api/assist/review", gin.H{"submissionId": mine.ID})

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.NotContains(t, rec.Body.String(), "single element")
}

// An accepted submission with a short illustrative snippet is delivered:
// the filter must not be so tight that the feature returns nothing.
func TestReviewDeliversAReviewWithAShortSnippet(t *testing.T) {
	good := "## Summary\nClear and idiomatic.\n\n## Readability\nConsider renaming `a`:\n\n```go\nlo := prices[0]\n```\n\n## Overall takeaway\nSolid."
	rig := newAssistRig(t, stubProvider{reply: good}, "u1")
	mine := rig.judge(t, "u1", submission.StatusAccepted, -1)

	rec := rig.post(t, "/api/assist/review", gin.H{"submissionId": mine.ID})

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, decodeData(t, rec)["text"], "Overall takeaway")
}
