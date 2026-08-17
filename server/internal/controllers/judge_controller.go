package controllers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/toji339/online-judge/internal/judge"
	"github.com/toji339/online-judge/internal/playground"
	"github.com/toji339/online-judge/internal/queue"
)

// JudgeController handles playground code execution requests.
//
// It does not own a sandbox. Where the code actually runs is the
// runner's business: in development that is this process, and in
// production it is a judge worker reached over the broker, because the
// API container is deliberately given no access to the Docker daemon.
type JudgeController struct {
	runner playground.Runner
}

// NewJudgeController creates a controller over a playground runner.
func NewJudgeController(runner playground.Runner) *JudgeController {
	return &JudgeController{runner: runner}
}

// respondToRunFailure maps a runner failure onto an HTTP status.
//
// A missing worker is a 503 and says so plainly: it is a temporary
// capacity problem the user can retry, not a bug in their code, and
// telling them "execution engine error" would send them debugging the
// wrong thing.
func respondToRunFailure(c *gin.Context, err error) {
	if errors.Is(err, queue.ErrNoWorker) {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "No judge worker is available right now. Please try again in a moment.",
		})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Execution engine error"})
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

	if len(req.Code) > playground.MaxCodeBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"success": false,
			"message": "Code exceeds the 64KB size limit",
		})
		return
	}
	if len(req.TestCases) > playground.MaxTestCases {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": fmt.Sprintf("At most %d test cases may be run at once", playground.MaxTestCases),
		})
		return
	}

	testCases := req.TestCases
	if len(testCases) == 0 {
		testCases = []judge.TestCase{{Input: req.Input, ExpectedOutput: req.ExpectedOutput}}
	}

	// Limits proposed by the client are clamped by whichever process
	// creates the container, so nothing here has to be trusted downstream.
	result, err := jc.runner.Run(c.Request.Context(), playground.Request{
		Mode:          playground.ModeEvaluate,
		Language:      req.Language,
		Code:          req.Code,
		TestCases:     testCases,
		TimeLimitMs:   req.TimeLimitMs,
		MemoryLimitMB: req.MemoryLimitMB,
	})
	if err != nil {
		respondToRunFailure(c, err)
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
// comparing against expected output. Used by the Playground page and by
// the Run button on a problem.
func (jc *JudgeController) RunRaw(c *gin.Context) {
	var req RunRawRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request payload"})
		return
	}

	if len(req.Code) > playground.MaxCodeBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"success": false,
			"message": "Code exceeds the 64KB size limit",
		})
		return
	}

	result, err := jc.runner.Run(c.Request.Context(), playground.Request{
		Mode:     playground.ModeRaw,
		Language: req.Language,
		Code:     req.Code,
		Stdin:    req.Stdin,
		// The raw playground gets the maximum allowance; there is no
		// problem definition here to take a limit from.
		TimeLimitMs:   playground.MaxTimeLimitMs,
		MemoryLimitMB: playground.DefaultMemoryLimitMB,
	})
	if err != nil {
		respondToRunFailure(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"stdout":    result.Stdout,
		"stderr":    result.Stderr,
		"exitCode":  result.ExitCode,
		"timedOut":  result.TimedOut,
		"oomKilled": result.OOMKilled,
		"runtimeMs": result.RuntimeMS,
	})
}
