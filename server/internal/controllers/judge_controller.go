package controllers

import (
	"context"
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload", "details": err.Error()})
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

	timeLimitMs := req.TimeLimitMs
	if timeLimitMs <= 0 {
		timeLimitMs = 3000 // default 3s
	}

	memLimitMB := req.MemoryLimitMB
	if memLimitMB <= 0 {
		memLimitMB = 256 // default 256MB
	}

	limits := judge.Limits{
		TimeLimit:     time.Duration(timeLimitMs) * time.Millisecond,
		MemoryLimitMB: memLimitMB,
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()

	result, err := jc.judgeEngine.Evaluate(ctx, req.Language, req.Code, testCases, limits)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Execution engine error", "details": err.Error()})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload", "details": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create sandbox", "details": err.Error()})
		return
	}
	defer sub.Close(ctx)

	// Compile
	compileResult, err := sub.Compile(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Compilation failed", "details": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Execution failed", "details": err.Error()})
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
