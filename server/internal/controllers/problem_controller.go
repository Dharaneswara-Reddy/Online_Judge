package controllers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/toji339/online-judge/internal/problem"
)

// ProblemController handles CRUD operations for problems and test cases.
type ProblemController struct {
	svc *problem.Service
}

// NewProblemController creates a controller backed by the given service.
func NewProblemController(svc *problem.Service) *ProblemController {
	return &ProblemController{svc: svc}
}

// CreateProblem handles POST /api/problems (admin only).
func (pc *ProblemController) CreateProblem(c *gin.Context) {
	var body struct {
		Title         string            `json:"title" binding:"required"`
		Statement     string            `json:"statement" binding:"required"`
		Difficulty    string            `json:"difficulty" binding:"required"`
		Tags          []string          `json:"tags"`
		TimeLimitMS   int               `json:"timeLimitMs" binding:"required"`
		MemoryLimitMB int               `json:"memoryLimitMb" binding:"required"`
		StarterCode   map[string]string `json:"starterCode"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request body", "details": err.Error()})
		return
	}

	input := problem.CreateProblemInput{
		Title:         body.Title,
		Statement:     body.Statement,
		Difficulty:    problem.Difficulty(body.Difficulty),
		Tags:          body.Tags,
		TimeLimitMS:   body.TimeLimitMS,
		MemoryLimitMB: body.MemoryLimitMB,
		StarterCode:   body.StarterCode,
	}

	p, err := pc.svc.Create(c.Request.Context(), input)
	if err != nil {
		var ve problem.ValidationError
		if errors.As(err, &ve) {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": ve.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to create problem"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"success": true, "data": p})
}

// ListProblems handles GET /api/problems (public).
func (pc *ProblemController) ListProblems(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))

	filter := problem.ListFilter{
		Difficulty: c.Query("difficulty"),
		Tag:        c.Query("tag"),
		Company:    c.Query("company"),
		Search:     c.Query("search"),
		Page:       page,
		PageSize:   pageSize,
	}

	problems, err := pc.svc.List(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to list problems"})
		return
	}

	// The total ignores pagination so the client can size its page controls.
	total, err := pc.svc.Count(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to count problems"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"data":     problems,
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
	})
}

// GetProblem handles GET /api/problems/:slug (public).
// Returns the problem along with its sample test cases.
func (pc *ProblemController) GetProblem(c *gin.Context) {
	slug := c.Param("slug")

	p, err := pc.svc.GetBySlug(c.Request.Context(), slug)
	if err != nil {
		if errors.Is(err, problem.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Problem not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to get problem"})
		return
	}

	// Fetch only sample test cases for the public endpoint
	samples, err := pc.svc.ListPublicTestCases(c.Request.Context(), p.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to get test cases"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":     true,
		"data":        p,
		"sampleCases": samples,
	})
}

// UpdateProblem handles PUT /api/problems/:id (admin only).
func (pc *ProblemController) UpdateProblem(c *gin.Context) {
	id := c.Param("id")

	var body struct {
		Title         string            `json:"title" binding:"required"`
		Statement     string            `json:"statement" binding:"required"`
		Difficulty    string            `json:"difficulty" binding:"required"`
		Tags          []string          `json:"tags"`
		TimeLimitMS   int               `json:"timeLimitMs" binding:"required"`
		MemoryLimitMB int               `json:"memoryLimitMb" binding:"required"`
		StarterCode   map[string]string `json:"starterCode"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request body", "details": err.Error()})
		return
	}

	input := problem.CreateProblemInput{
		Title:         body.Title,
		Statement:     body.Statement,
		Difficulty:    problem.Difficulty(body.Difficulty),
		Tags:          body.Tags,
		TimeLimitMS:   body.TimeLimitMS,
		MemoryLimitMB: body.MemoryLimitMB,
		StarterCode:   body.StarterCode,
	}

	p, err := pc.svc.Update(c.Request.Context(), id, input)
	if err != nil {
		if errors.Is(err, problem.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Problem not found"})
			return
		}
		var ve problem.ValidationError
		if errors.As(err, &ve) {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": ve.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to update problem"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": p})
}

// AddTestCase handles POST /api/problems/:id/testcases (admin only).
func (pc *ProblemController) AddTestCase(c *gin.Context) {
	problemID := c.Param("id")

	var body struct {
		Input          string `json:"input"`
		ExpectedOutput string `json:"expectedOutput" binding:"required"`
		IsSample       bool   `json:"isSample"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request body", "details": err.Error()})
		return
	}

	tc := &problem.TestCase{
		ProblemID:      problemID,
		Input:          body.Input,
		ExpectedOutput: body.ExpectedOutput,
		IsSample:       body.IsSample,
	}

	if err := pc.svc.AddTestCase(c.Request.Context(), tc); err != nil {
		var ve problem.ValidationError
		if errors.As(err, &ve) {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": ve.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to add test case"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"success": true, "data": tc})
}

// ListTestCases handles GET /api/problems/:id/testcases (admin only).
// Returns ALL test cases including hidden ones.
func (pc *ProblemController) ListTestCases(c *gin.Context) {
	problemID := c.Param("id")

	tcs, err := pc.svc.ListAllTestCases(c.Request.Context(), problemID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to list test cases"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": tcs})
}
