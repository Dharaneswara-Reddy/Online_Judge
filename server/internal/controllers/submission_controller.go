package controllers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/toji339/online-judge/internal/judge"
	"github.com/toji339/online-judge/internal/problem"
	"github.com/toji339/online-judge/internal/submission"
)

// judgeTimeout bounds one full evaluation (compile plus every test case)
// independently of the per-run limits enforced inside the sandbox.
const judgeTimeout = 60 * time.Second

// SubmissionController handles user code submissions against problems and
// serves the resulting submission records back to the user.
type SubmissionController struct {
	problemSvc    *problem.Service
	submissionSvc *submission.Service
	judgeEngine   *judge.Judge
}

// NewSubmissionController creates a controller for evaluating submissions.
func NewSubmissionController(problemSvc *problem.Service, submissionSvc *submission.Service, sandbox judge.Sandbox) *SubmissionController {
	return &SubmissionController{
		problemSvc:    problemSvc,
		submissionSvc: submissionSvc,
		judgeEngine:   judge.NewJudge(sandbox),
	}
}

// Submit handles POST /api/problems/:slug/submit (authenticated).
//
// It records the attempt as a pending submission, judges it against ALL
// test cases (including hidden ones), and writes the verdict back to the
// record. The verdict is derived entirely on the server — nothing in the
// request body can influence it.
func (sc *SubmissionController) Submit(c *gin.Context) {
	// Steps to follow while handling a submission
	// =============================================

	// 1. Read the request body and the authenticated user
	var body struct {
		Language string `json:"language" binding:"required"`
		Code     string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request body", "details": err.Error()})
		return
	}
	userID := c.GetString("userID")

	// 2. Fetch the problem by slug
	p, err := sc.problemSvc.GetBySlug(c.Request.Context(), c.Param("slug"))
	if err != nil {
		if errors.Is(err, problem.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Problem not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to fetch problem"})
		return
	}

	// 3. Record the attempt. This also runs validation and admission
	//    control, so it must happen before any expensive work.
	sub, err := sc.submissionSvc.Create(c.Request.Context(), submission.CreateInput{
		UserID:       userID,
		ProblemID:    p.ID,
		ProblemSlug:  p.Slug,
		ProblemTitle: p.Title,
		Language:     body.Language,
		Code:         body.Code,
	})
	if err != nil {
		writeSubmissionError(c, err)
		return
	}

	// 4. Fetch ALL test cases (including hidden) for judging
	allCases, err := sc.problemSvc.ListAllTestCases(c.Request.Context(), p.ID)
	if err != nil {
		sc.failSubmission(sub.ID, "could not load test cases")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to fetch test cases"})
		return
	}
	if len(allCases) == 0 {
		sc.failSubmission(sub.ID, "problem has no test cases")
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "No test cases found for this problem"})
		return
	}

	// 5. Evaluate it, then persist the verdict
	result, err := sc.evaluate(c.Request.Context(), sub, p, allCases)
	if err != nil {
		sc.failSubmission(sub.ID, "execution engine error")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Execution engine error", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"submissionId": sub.ID,
		"status":       result.Status,
		"verdict":      result.Status, // retained for the existing client
		"runtimeMs":    result.RuntimeMS,
		"memoryKb":     result.MemoryKB,
		"failedCase":   result.FailedCase,
		"compileError": result.CompileError,
		"totalCases":   len(allCases),
	})
}

// evaluate runs the judge and records the outcome on the submission.
func (sc *SubmissionController) evaluate(ctx context.Context, sub *submission.Submission, p *problem.Problem, cases []problem.TestCase) (submission.Result, error) {
	judgeCases := make([]judge.TestCase, len(cases))
	for i, tc := range cases {
		judgeCases[i] = judge.TestCase{Input: tc.Input, ExpectedOutput: tc.ExpectedOutput}
	}

	limits := judge.Limits{
		TimeLimit:     time.Duration(p.TimeLimitMS) * time.Millisecond,
		MemoryLimitMB: int64(p.MemoryLimitMB),
	}

	if err := sc.submissionSvc.MarkRunning(ctx, sub.ID); err != nil {
		return submission.Result{}, err
	}

	judgeCtx, cancel := context.WithTimeout(ctx, judgeTimeout)
	defer cancel()

	verdict, err := sc.judgeEngine.Evaluate(judgeCtx, sub.Language, sub.Code, judgeCases, limits)
	if err != nil {
		return submission.Result{}, err
	}

	result := submission.Result{
		Status:       submission.StatusFromVerdict(verdict.Verdict),
		RuntimeMS:    verdict.RuntimeMS,
		MemoryKB:     verdict.MemoryKB,
		FailedCase:   verdict.FailedCase,
		CompileError: verdict.CompileError,
	}
	if err := sc.submissionSvc.MarkJudged(ctx, sub.ID, result); err != nil {
		return submission.Result{}, err
	}
	return result, nil
}

// failSubmission clears a stuck submission after an infrastructure error.
// It uses a fresh context because the request context may already be done.
func (sc *SubmissionController) failSubmission(id, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sc.submissionSvc.MarkFailed(ctx, id, reason); err != nil {
		log.Printf("WARNING: could not mark submission %s as failed: %v", id, err)
	}
}

// GetSubmission handles GET /api/submissions/:id (authenticated).
// A user may only read their own submissions, since the record contains
// their source code.
func (sc *SubmissionController) GetSubmission(c *gin.Context) {
	sub, err := sc.submissionSvc.GetByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, submission.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Submission not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to fetch submission"})
		return
	}

	if sub.UserID != c.GetString("userID") && c.GetString("role") != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "You can only view your own submissions"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": sub})
}

// ListMySubmissions handles GET /api/users/me/submissions (authenticated),
// returning a filtered, paginated page of the caller's own history.
func (sc *SubmissionController) ListMySubmissions(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))
	if pageSize > 100 {
		pageSize = 100
	}

	filter := submission.ListFilter{
		UserID:    c.GetString("userID"),
		ProblemID: c.Query("problemId"),
		Status:    submission.Status(c.Query("status")),
		Page:      page,
		PageSize:  pageSize,
	}

	items, err := sc.submissionSvc.List(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to fetch submissions"})
		return
	}
	total, err := sc.submissionSvc.Count(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to count submissions"})
		return
	}

	// The list view never needs the source code, and shipping it for every
	// row would bloat the response — the detail endpoint serves it instead.
	for i := range items {
		items[i].Code = ""
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"data":     items,
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
	})
}

// writeSubmissionError maps submission service errors to HTTP responses.
func writeSubmissionError(c *gin.Context, err error) {
	var vErr submission.ValidationError
	switch {
	case errors.As(err, &vErr):
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Validation failed", "errors": []string{vErr.Error()}})
	case errors.Is(err, submission.ErrTooManyPending):
		c.JSON(http.StatusTooManyRequests, gin.H{"success": false, "message": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to record submission"})
	}
}
