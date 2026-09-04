package controllers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/toji339/online-judge/internal/assist"
	"github.com/toji339/online-judge/internal/problem"
	"github.com/toji339/online-judge/internal/submission"
)

// AssistController is the edge of the AI assist feature.
//
// It owns the three questions the assist package deliberately cannot
// answer for itself, because answering them needs the database:
//
//   - who is asking, and is this their submission
//   - what has this student already tried at this problem
//   - which hidden test case did their code fail
//
// The last one is why this controller exists at all rather than the
// service talking to a repository. Rung 3 of the hint ladder describes a
// property of a test case the student has never seen, which means the
// case has to enter the prompt. That is acceptable — hidden cases
// already live on this side of the trust boundary, the worker reads them
// on every judgement — but it makes the path that fetches one worth
// keeping short, obvious and in one place. Nothing here ever writes a
// test case into a response body; the assist package's leak filter is
// the second lock on the same door.
type AssistController struct {
	assist      *assist.Service
	problems    *problem.Service
	submissions *submission.Service
}

// NewAssistController wires the controller. A nil or disabled assist
// service is legal: every endpoint then answers 503 and the client hides
// the feature, which is the same degradation Redis and the broker get.
func NewAssistController(a *assist.Service, problems *problem.Service, submissions *submission.Service) *AssistController {
	return &AssistController{assist: a, problems: problems, submissions: submissions}
}

// attemptHistorySize is how many recent submissions the stuck detector
// looks at. The rules span at most four attempts; twenty is generous
// enough that a long session still shows its shape, and small enough
// that the query stays a single indexed page.
const attemptHistorySize = 20

// respondToAssistFailure maps an assist error onto a status.
//
// The important distinction is between "the feature is off" and "the
// feature is on and this call did not work". The client hides itself on
// 503 and only 503, so a withheld or failed generation must not use it —
// otherwise one filtered response would make the assistant vanish for
// the rest of the session.
func respondToAssistFailure(c *gin.Context, err error) {
	switch {
	case errors.Is(err, assist.ErrDisabled):
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "The assistant is not available on this deployment.",
		})
	case errors.Is(err, assist.ErrInvalidRung):
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "That hint level does not exist.",
		})
	case errors.Is(err, assist.ErrFiltered), errors.Is(err, assist.ErrLeak):
		// The generation happened and was thrown away. Saying so plainly
		// is better than a generic failure: the student did nothing
		// wrong and retrying is a reasonable thing to do.
		c.JSON(http.StatusBadGateway, gin.H{
			"success": false,
			"message": "That hint came back with too much of the answer in it, so it was withheld. Try again.",
		})
	default:
		c.JSON(http.StatusBadGateway, gin.H{
			"success": false,
			"message": "The assistant could not be reached. Please try again in a moment.",
		})
	}
}

// State handles GET /api/problems/:slug/assist/state (authenticated).
//
// It answers 200 even when the feature is switched off, carrying
// enabled:false, because the client asks this question in order to
// decide whether to render anything at all — and a disabled deployment
// should produce a quiet absence rather than an error in the console.
func (ac *AssistController) State(c *gin.Context) {
	if !ac.assist.Enabled() {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    gin.H{"enabled": false, "stuck": false, "attempts": 0, "maxRung": 0},
		})
		return
	}

	prob, err := ac.problems.GetBySlug(c.Request.Context(), c.Param("slug"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Problem not found"})
		return
	}

	attempts, err := ac.attemptsFor(c, c.GetString("userID"), prob.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to read your attempts"})
		return
	}

	state := assist.Detect(attempts, time.Now().UTC())

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"enabled":    true,
			"stuck":      state.Stuck,
			"reason":     state.Reason,
			"attempts":   state.Attempts,
			"maxRung":    state.MaxRung,
			"lastStatus": state.LastStatus,
		},
	})
}

// Hint handles POST /api/assist/hint (authenticated).
func (ac *AssistController) Hint(c *gin.Context) {
	var body struct {
		ProblemSlug  string `json:"problemSlug" binding:"required"`
		Rung         int    `json:"rung" binding:"required"`
		Language     string `json:"language"`
		Code         string `json:"code"`
		SubmissionID string `json:"submissionId"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request body"})
		return
	}

	if !ac.assist.Enabled() {
		respondToAssistFailure(c, assist.ErrDisabled)
		return
	}

	prob, err := ac.problems.GetBySlug(c.Request.Context(), body.ProblemSlug)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Problem not found"})
		return
	}

	userID := c.GetString("userID")
	attempts, err := ac.attemptsFor(c, userID, prob.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to read your attempts"})
		return
	}

	req := assist.HintRequest{
		Rung:     assist.Rung(body.Rung),
		Problem:  problemContext(prob),
		Language: body.Language,
		Code:     body.Code,
		Attempts: attempts,
	}

	// Only rung 3 is given a hidden case, and only when one can actually
	// be identified from a real failed submission of this user's.
	if req.Rung == assist.RungFailing {
		failing, err := ac.failingCase(c, userID, prob, body.SubmissionID)
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{
				"success": false,
				"message": "This hint describes the test case your code fails, so it needs a failed submission first.",
			})
			return
		}
		req.Failing = failing
	}

	hint, err := ac.assist.Hint(c.Request.Context(), req)
	if err != nil {
		respondToAssistFailure(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"rung": int(hint.Rung), "text": hint.Text, "cached": hint.Cached},
	})
}

// Explain handles POST /api/assist/explain (authenticated).
//
// The verdict explained is the one the judge recorded, read from the
// database — never a status the client asserted. That is the same rule
// the rest of the codebase follows about verdicts, and it matters here
// because a client that could name its own verdict could ask for an
// explanation of a problem it has not attempted.
func (ac *AssistController) Explain(c *gin.Context) {
	sub, prob, ok := ac.ownedSubmission(c)
	if !ok {
		return
	}

	if sub.Status == submission.StatusAccepted || !sub.Status.IsTerminal() {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "There is no failing verdict on that submission to explain.",
		})
		return
	}

	explanation, err := ac.assist.ExplainVerdict(c.Request.Context(), assist.ExplainRequest{
		Problem:      problemContext(prob),
		Language:     sub.Language,
		Code:         sub.Code,
		Status:       string(sub.Status),
		FailedCase:   sub.FailedCase,
		TotalCases:   sub.TotalCases,
		RuntimeMS:    sub.RuntimeMS,
		MemoryKB:     sub.MemoryKB,
		CompileError: sub.CompileError,
	})
	if err != nil {
		respondToAssistFailure(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"text": explanation.Text, "cached": explanation.Cached},
	})
}

// Review handles POST /api/assist/review (authenticated).
//
// It refuses anything but an accepted submission, and that refusal is
// the whole reason this endpoint is safe to have. A review discusses the
// code in full; on an unsolved problem that is a solution with extra
// steps. Once the judge has accepted it, there is nothing left to give
// away.
func (ac *AssistController) Review(c *gin.Context) {
	sub, prob, ok := ac.ownedSubmission(c)
	if !ok {
		return
	}

	if sub.Status != submission.StatusAccepted {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Reviews are only available for accepted submissions.",
		})
		return
	}

	review, err := ac.assist.ReviewSolution(c.Request.Context(), assist.ReviewRequest{
		Problem:   problemContext(prob),
		Language:  sub.Language,
		Code:      sub.Code,
		RuntimeMS: sub.RuntimeMS,
		MemoryKB:  sub.MemoryKB,
	})
	if err != nil {
		respondToAssistFailure(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"text": review.Text}})
}

// ownedSubmission reads the submission named in the body and checks the
// caller wrote it, answering on the context and reporting false when it
// has already done so.
//
// Ownership is checked here rather than trusted from the route, and it
// is a plain equality against the authenticated user with no admin
// exception: source code is owner-only in this codebase, and an
// assistant that will discuss anyone's submission is a way to read
// anyone's submission.
func (ac *AssistController) ownedSubmission(c *gin.Context) (*submission.Submission, *problem.Problem, bool) {
	var body struct {
		SubmissionID string `json:"submissionId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request body"})
		return nil, nil, false
	}

	if !ac.assist.Enabled() {
		respondToAssistFailure(c, assist.ErrDisabled)
		return nil, nil, false
	}

	sub, err := ac.submissions.GetByID(c.Request.Context(), body.SubmissionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Submission not found"})
		return nil, nil, false
	}
	if sub.UserID != c.GetString("userID") {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "You can only ask about your own submissions"})
		return nil, nil, false
	}

	prob, err := ac.problems.GetByID(c.Request.Context(), sub.ProblemID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Problem not found"})
		return nil, nil, false
	}

	return sub, prob, true
}

// attemptsFor loads a page of this user's history at one problem and
// reduces it to the fields stuck detection is allowed to see.
//
// The reduction is not decoration: assist.Attempt has no code field, so
// a history loaded here cannot carry one student's source into a prompt.
func (ac *AssistController) attemptsFor(c *gin.Context, userID, problemID string) ([]assist.Attempt, error) {
	subs, err := ac.submissions.List(c.Request.Context(), submission.ListFilter{
		UserID:    userID,
		ProblemID: problemID,
		Page:      1,
		PageSize:  attemptHistorySize,
	})
	if err != nil {
		return nil, err
	}

	attempts := make([]assist.Attempt, 0, len(subs))
	for _, s := range subs {
		attempts = append(attempts, assist.Attempt{
			Status:      string(s.Status),
			FailedCase:  s.FailedCase,
			TotalCases:  s.TotalCases,
			SubmittedAt: s.SubmittedAt,
			JudgedAt:    s.JudgedAt,
		})
	}
	return attempts, nil
}

// failingCase resolves the hidden test case a rung-3 hint is about.
//
// It uses the submission the client named when there is one, and
// otherwise the caller's most recent failure at this problem. Either way
// the submission must belong to the caller and must have failed on a
// real case index — the index is the judge's own, produced by the worker
// against this same ordered list of cases.
func (ac *AssistController) failingCase(c *gin.Context, userID string, prob *problem.Problem, submissionID string) (*assist.HiddenCase, error) {
	sub, err := ac.latestFailure(c, userID, prob.ID, submissionID)
	if err != nil {
		return nil, err
	}

	cases, err := ac.problems.ListAllTestCases(c.Request.Context(), prob.ID)
	if err != nil {
		return nil, err
	}
	if sub.FailedCase < 0 || sub.FailedCase >= len(cases) {
		return nil, errNoFailingCase
	}

	failed := cases[sub.FailedCase]
	return &assist.HiddenCase{Input: failed.Input, ExpectedOutput: failed.ExpectedOutput}, nil
}

// errNoFailingCase means there is nothing for rung 3 to describe.
var errNoFailingCase = errors.New("assist: no failed submission to describe")

// latestFailure finds the submission a rung-3 hint should be about.
func (ac *AssistController) latestFailure(c *gin.Context, userID, problemID, submissionID string) (*submission.Submission, error) {
	if submissionID != "" {
		sub, err := ac.submissions.GetByID(c.Request.Context(), submissionID)
		if err != nil {
			return nil, err
		}
		if sub.UserID != userID || sub.ProblemID != problemID {
			return nil, errNoFailingCase
		}
		if !describesACase(sub.Status) {
			return nil, errNoFailingCase
		}
		return sub, nil
	}

	subs, err := ac.submissions.List(c.Request.Context(), submission.ListFilter{
		UserID:    userID,
		ProblemID: problemID,
		Page:      1,
		PageSize:  attemptHistorySize,
	})
	if err != nil {
		return nil, err
	}

	// List is newest-first, so the first match is the most recent
	// failure — the one the student is currently looking at.
	for i := range subs {
		if describesACase(subs[i].Status) {
			return &subs[i], nil
		}
	}
	return nil, errNoFailingCase
}

// describesACase reports whether a verdict points at a particular test
// case. A compile error never ran one, and an accepted submission failed
// none, so neither has a case to describe.
func describesACase(status submission.Status) bool {
	switch status {
	case submission.StatusWrongAnswer, submission.StatusTLE, submission.StatusMLE,
		submission.StatusRuntimeError, submission.StatusOutputLimitExceeded:
		return true
	default:
		return false
	}
}

// problemContext reduces a problem to what a model may be told about it.
// Every field here is already rendered on the student's screen.
func problemContext(p *problem.Problem) assist.ProblemContext {
	return assist.ProblemContext{
		Title:         p.Title,
		Statement:     p.Statement,
		Difficulty:    string(p.Difficulty),
		Tags:          p.Tags,
		TimeLimitMS:   p.TimeLimitMS,
		MemoryLimitMB: p.MemoryLimitMB,
	}
}
