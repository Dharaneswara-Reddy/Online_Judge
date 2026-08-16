package controllers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/toji339/online-judge/internal/judge"
)

// JudgeController handles code execution requests.
type JudgeController struct {
	judgeEngine *judge.Judge
	sandbox     judge.Sandbox
}

// NewJudgeController creates a controller instance given a Sandbox implementation.
func NewJudgeController(sandbox judge.Sandbox) *JudgeController {
	return &JudgeController{
		judgeEngine: judge.NewJudge(sandbox),
		sandbox:     sandbox,
	}
}

// Playground execution limits. These are ceilings, not suggestions: the
// request may ask for less, never more. Real submissions take their
// limits from the problem definition instead and never consult these.
const (
	defaultTimeLimitMs   int64 = 3000
	maxTimeLimitMs       int64 = 10000
	defaultMemoryLimitMB int64 = 256
	maxMemoryLimitMB     int64 = 512

	// maxPlaygroundTestCases bounds how many container executions one
	// request can trigger; each test case is a separate exec round trip.
	maxPlaygroundTestCases = 20
	// maxPlaygroundCodeBytes mirrors the cap the submission service
	// applies, which this path does not go through.
	maxPlaygroundCodeBytes = 64 * 1024
)

// clampLimit keeps a client-proposed limit inside the allowed range,
// falling back to the default when it is absent or nonsensical.
func clampLimit(requested, fallback, max int64) int64 {
	if requested <= 0 {
		return fallback
	}
	if requested > max {
		return max
	}
	return requested
}

// RunCodeRequest defines the JSON payload for running user code.
type RunCodeRequest struct {
	Language       string           `json:"language" binding:"required"`
	Code           string           `json:"code" binding:"required"`
	TestCases      []judge.TestCase `json:"test_cases"`
	Input          string           `json:"input"`
	ExpectedOutput string           `json:"expected_output"`
	TimeLimitMs    int64            `json:"time_limit_ms"`
	MemoryLimitMB  int64            `json:"memory_limit_mb"`
}

// RunCode evaluates the user submission against provided test cases.
func (jc *JudgeController) RunCode(c *gin.Context) {
	var req RunCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request payload"})
		return
	}

	if len(req.Code) > maxPlaygroundCodeBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"success": false,
			"message": "Code exceeds the 64KB size limit",
		})
		return
	}
	if len(req.TestCases) > maxPlaygroundTestCases {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": fmt.Sprintf("At most %d test cases may be run at once", maxPlaygroundTestCases),
		})
		return
	}

	testCases := req.TestCases
	if len(testCases) == 0 {
		testCases = []judge.TestCase{
			{
				Input:          req.Input,
				ExpectedOutput: req.ExpectedOutput,
			},
		}
	}

	// The playground is the only path where a client proposes its own
	// resource limits, so they are clamped rather than merely defaulted.
	// An unclamped value becomes the container's cgroup ceiling, and a
	// ceiling larger than physical memory does not bind at all — the
	// program then allocates until the host OOM killer fires.
	timeLimitMs := clampLimit(req.TimeLimitMs, defaultTimeLimitMs, maxTimeLimitMs)
	memLimitMB := clampLimit(req.MemoryLimitMB, defaultMemoryLimitMB, maxMemoryLimitMB)

	limits := judge.Limits{
		TimeLimit:     time.Duration(timeLimitMs) * time.Millisecond,
		MemoryLimitMB: memLimitMB,
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()

	result, err := jc.judgeEngine.Evaluate(ctx, req.Language, req.Code, testCases, limits)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Execution engine error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"verdict":       result.Verdict,
		"runtime_ms":    result.RuntimeMS,
		"memory_kb":     result.MemoryKB,
		"failed_case":   result.FailedCase,
		"compile_error": result.CompileError,
	})
}

// RunRawRequest is the payload for the playground "just run my code" endpoint.
type RunRawRequest struct {
	Language string `json:"language" binding:"required"`
	Code     string `json:"code" binding:"required"`
	Stdin    string `json:"stdin"`
}

// RunRaw compiles and runs code, returning raw stdout/stderr without
// comparing against expected output. Used by the Playground page.
func (jc *JudgeController) RunRaw(c *gin.Context) {
	var req RunRawRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request payload"})
		return
	}

	limits := judge.Limits{
		TimeLimit:     10 * time.Second,
		MemoryLimitMB: 256,
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()

	sub, err := jc.sandbox.NewSubmission(ctx, req.Language, req.Code, limits)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to create sandbox"})
		return
	}
	defer sub.Close(ctx)

	// Compile
	compileResult, err := sub.Compile(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Compilation failed"})
		return
	}
	if compileResult.ExitCode != 0 {
		c.JSON(http.StatusOK, gin.H{
			"stdout":    "",
			"stderr":    compileResult.Stderr,
			"exitCode":  compileResult.ExitCode,
			"timedOut":  false,
			"oomKilled": false,
			"runtimeMs": int64(0),
		})
		return
	}

	// Run
	runResult, err := sub.Run(ctx, req.Stdin)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Execution failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"stdout":    runResult.Stdout,
		"stderr":    runResult.Stderr,
		"exitCode":  runResult.ExitCode,
		"timedOut":  runResult.TimedOut,
		"oomKilled": runResult.OOMKilled,
		"runtimeMs": runResult.RuntimeMS,
	})
}
