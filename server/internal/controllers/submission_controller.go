package controllers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/toji339/online-judge/internal/judge"
	"github.com/toji339/online-judge/internal/problem"
)

// SubmissionController handles user code submissions against problems.
type SubmissionController struct {
	problemSvc  *problem.Service
	judgeEngine *judge.Judge
}

// NewSubmissionController creates a controller for evaluating submissions.
func NewSubmissionController(problemSvc *problem.Service, sandbox judge.Sandbox) *SubmissionController {
	return &SubmissionController{
		problemSvc:  problemSvc,
		judgeEngine: judge.NewJudge(sandbox),
	}
}

// Submit handles POST /api/problems/:slug/submit (authenticated).
// It fetches the problem and ALL test cases (including hidden ones),
// then evaluates the user's code synchronously via the judge engine.
func (sc *SubmissionController) Submit(c *gin.Context) {
	slug := c.Param("slug")

	var body struct {
		Language string `json:"language" binding:"required"`
		Code     string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request body", "details": err.Error()})
		return
	}

	// 1. Fetch the problem by slug
	p, err := sc.problemSvc.GetBySlug(c.Request.Context(), slug)
	if err != nil {
		if errors.Is(err, problem.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Problem not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to fetch problem"})
		return
	}

	// 2. Fetch ALL test cases (including hidden) for judging
	allCases, err := sc.problemSvc.ListAllTestCases(c.Request.Context(), p.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to fetch test cases"})
		return
	}

	if len(allCases) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "No test cases found for this problem"})
		return
	}

	// 3. Convert problem.TestCase → judge.TestCase
	judgeCases := make([]judge.TestCase, len(allCases))
	for i, tc := range allCases {
		judgeCases[i] = judge.TestCase{
			Input:          tc.Input,
			ExpectedOutput: tc.ExpectedOutput,
		}
	}

	// 4. Build limits from the problem's constraints
	limits := judge.Limits{
		TimeLimit:     time.Duration(p.TimeLimitMS) * time.Millisecond,
		MemoryLimitMB: int64(p.MemoryLimitMB),
	}

	// 5. Evaluate synchronously (queue layer comes in a later phase)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	result, err := sc.judgeEngine.Evaluate(ctx, body.Language, body.Code, judgeCases, limits)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Execution engine error", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"verdict":      result.Verdict,
		"runtimeMs":    result.RuntimeMS,
		"memoryKb":     result.MemoryKB,
		"failedCase":   result.FailedCase,
		"compileError": result.CompileError,
		"totalCases":   len(allCases),
	})
}
