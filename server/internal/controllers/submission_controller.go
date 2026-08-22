package controllers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/toji339/online-judge/internal/problem"
	"github.com/toji339/online-judge/internal/queue"
	"github.com/toji339/online-judge/internal/submission"
	"github.com/toji339/online-judge/internal/warroom"
	"github.com/toji339/online-judge/internal/worker"
)

// SubmissionController accepts user code submissions, hands them to the
// judging pipeline, and serves the resulting records back to the user.
type SubmissionController struct {
	problemSvc    *problem.Service
	submissionSvc *submission.Service

	// publisher is nil when the broker is unreachable, in which case the
	// controller judges inline rather than rejecting the submission.
	publisher queue.Publisher
	processor *worker.Processor

	// warRooms is nil when the War Room feature is not wired up.
	warRooms *warroom.Service
}

// NewSubmissionController creates a controller for handling submissions.
// A nil publisher selects the inline (synchronous) judging fallback.
func NewSubmissionController(problemSvc *problem.Service, submissionSvc *submission.Service, publisher queue.Publisher, processor *worker.Processor, warRooms *warroom.Service) *SubmissionController {
	return &SubmissionController{
		problemSvc:    problemSvc,
		submissionSvc: submissionSvc,
		publisher:     publisher,
		processor:     processor,
		warRooms:      warRooms,
	}
}

// Submit handles POST /api/problems/:slug/submit (authenticated).
//
// It records the attempt as a pending submission and queues it for a
// judge worker, returning 202 immediately so a burst of submissions
// never ties up API capacity. The client then polls
// GET /api/submissions/:id for the verdict.
//
// Nothing in the request body can influence the verdict — the judge
// reads the stored submission, and only the worker writes a status.
func (sc *SubmissionController) Submit(c *gin.Context) {
	// Steps to follow while accepting a submission
	// ==============================================

	// 1. Read the request body and the authenticated user
	var body struct {
		Language string `json:"language" binding:"required"`
		Code     string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		writeBindError(c, err)
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

	// 3. Record the attempt. This runs validation and admission control,
	//    so it must happen before any expensive work is scheduled.
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

	// 4. Hand it to the judging pipeline
	sc.dispatch(c, sub)
}

// SubmitToWarRoom handles POST /api/warrooms/:code/submit (authenticated).
//
// It behaves like Submit but tags the submission with the room, which
// routes it to the dedicated high-priority judging lane so a live race is
// never delayed by background practice traffic.
func (sc *SubmissionController) SubmitToWarRoom(c *gin.Context) {
	if sc.warRooms == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "War rooms are unavailable"})
		return
	}

	var body struct {
		Language string `json:"language" binding:"required"`
		Code     string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		writeBindError(c, err)
		return
	}

	// Only a participant may submit into a room, and only while it runs.
	room, err := sc.warRooms.EnsureParticipant(c.Request.Context(), c.Param("code"), c.GetString("userID"))
	if err != nil {
		writeWarRoomError(c, err)
		return
	}
	if room.Status != warroom.StatusInProgress {
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": "This race is not running"})
		return
	}

	sub, err := sc.submissionSvc.Create(c.Request.Context(), submission.CreateInput{
		UserID:       c.GetString("userID"),
		ProblemID:    room.ProblemID,
		ProblemSlug:  room.ProblemSlug,
		ProblemTitle: room.ProblemTitle,
		WarRoomID:    room.ID,
		Language:     body.Language,
		Code:         body.Code,
	})
	if err != nil {
		writeSubmissionError(c, err)
		return
	}

	sc.dispatch(c, sub)
}

// dispatch queues the submission, falling back to judging it inline when
// no broker is available so the platform still works on a bare machine.
func (sc *SubmissionController) dispatch(c *gin.Context, sub *submission.Submission) {
	job := queue.Job{
		SubmissionID: sub.ID,
		UserID:       sub.UserID,
		ProblemID:    sub.ProblemID,
		WarRoomID:    sub.WarRoomID,
	}

	if sc.publisher != nil {
		err := sc.publisher.Publish(c.Request.Context(), queue.LaneFor(sub.WarRoomID), job)
		switch {
		case err != nil && sc.processor == nil:
			// Nothing can judge this submission — say so instead of
			// leaving the user staring at a permanently pending verdict.
			log.Printf("ERROR: could not queue submission %s and no inline judge is available: %v", sub.ID, err)
			sc.failAndReport(c, sub, "The judging service is currently unavailable")
			return
		case err != nil:
			log.Printf("WARNING: could not queue submission %s, judging inline: %v", sub.ID, err)
		default:
			c.JSON(http.StatusAccepted, gin.H{
				"success":      true,
				"message":      "Submission queued for judging",
				"submissionId": sub.ID,
				"status":       submission.StatusPending,
			})
			return
		}
	}

	// No broker at all and no inline judge either.
	if sc.processor == nil {
		sc.failAndReport(c, sub, "The judging service is currently unavailable")
		return
	}

	// Inline fallback. Process records the verdict on the submission, so
	// the response is built from the reloaded record either way.
	if err := sc.processor.Process(c.Request.Context(), job); err != nil {
		log.Printf("WARNING: inline judging of submission %s failed: %v", sub.ID, err)
	}

	judged, err := sc.submissionSvc.GetByID(c.Request.Context(), sub.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to load verdict"})
		return
	}
	c.JSON(http.StatusOK, submissionResponse(judged))
}

// failAndReport clears a submission that can never be judged and tells
// the caller the service is down, so nothing is left pending forever.
func (sc *SubmissionController) failAndReport(c *gin.Context, sub *submission.Submission, message string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 5*time.Second)
	defer cancel()

	if err := sc.submissionSvc.MarkFailed(ctx, sub.ID, message); err != nil {
		log.Printf("WARNING: could not mark submission %s as failed: %v", sub.ID, err)
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": message, "submissionId": sub.ID})
}

// GetSubmission handles GET /api/submissions/:id (authenticated) and is
// what the client polls while a verdict is pending. A user may only read
// their own submissions, since the record contains their source code.
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

	response := submissionResponse(sub)
	response["data"] = sub
	c.JSON(http.StatusOK, response)
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

// submissionResponse renders one submission in the flat shape the code
// editor's verdict panel consumes.
func submissionResponse(sub *submission.Submission) gin.H {
	return gin.H{
		"success":      true,
		"submissionId": sub.ID,
		"status":       sub.Status,
		"verdict":      sub.Status, // retained for the existing client
		"runtimeMs":    sub.RuntimeMS,
		"memoryKb":     sub.MemoryKB,
		"failedCase":   sub.FailedCase,
		"totalCases":   sub.TotalCases,
		"compileError": sub.CompileError,
	}
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
