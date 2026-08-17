package controllers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/toji339/online-judge/internal/discussion"
	"github.com/toji339/online-judge/internal/problem"
)

// DiscussionController serves a problem's comment thread.
type DiscussionController struct {
	discussions *discussion.Service
	problems    *problem.Service
}

// NewDiscussionController creates the controller with its dependencies.
func NewDiscussionController(discussions *discussion.Service, problems *problem.Service) *DiscussionController {
	return &DiscussionController{discussions: discussions, problems: problems}
}

// ListForProblem handles GET /api/problems/:slug/discussions (public).
//
// Reading a thread does not require an account, but a signed-in reader
// also gets their own votes marked so the UI renders correctly.
func (dc *DiscussionController) ListForProblem(c *gin.Context) {
	prob, err := dc.problems.GetBySlug(c.Request.Context(), c.Param("slug"))
	if err != nil {
		writeProblemLookupError(c, err)
		return
	}

	limit, _ := strconv.Atoi(c.Query("limit"))

	page, err := dc.discussions.ListThreadPage(
		c.Request.Context(), prob.ID, c.GetString("userID"), c.Query("cursor"), limit)
	if err != nil {
		if errors.Is(err, discussion.ErrInvalidCursor) {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid pagination cursor"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to load discussion"})
		return
	}

	// `data` keeps its existing shape so an older client still works; the
	// pagination fields are additive.
	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"data":       page.Threads,
		"nextCursor": page.NextCursor,
		"hasMore":    page.HasMore,
	})
}

// Create handles POST /api/problems/:slug/discussions (authenticated).
// Supply parentId to reply to an existing top-level comment.
func (dc *DiscussionController) Create(c *gin.Context) {
	var body struct {
		Content  string `json:"content" binding:"required"`
		ParentID string `json:"parentId"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request body", "details": err.Error()})
		return
	}

	prob, err := dc.problems.GetBySlug(c.Request.Context(), c.Param("slug"))
	if err != nil {
		writeProblemLookupError(c, err)
		return
	}

	comment, err := dc.discussions.Create(c.Request.Context(), discussion.CreateInput{
		ProblemID: prob.ID,
		UserID:    c.GetString("userID"),
		Username:  c.GetString("username"),
		ParentID:  body.ParentID,
		Content:   body.Content,
	})
	if err != nil {
		writeDiscussionError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{"success": true, "message": "Comment posted", "data": comment})
}

// Upvote handles POST /api/discussions/:id/upvote (authenticated).
// Voting is idempotent, so a double click cannot inflate the count.
func (dc *DiscussionController) Upvote(c *gin.Context) {
	count, err := dc.discussions.Upvote(c.Request.Context(), c.Param("id"), c.GetString("userID"))
	if err != nil {
		writeDiscussionError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"upvotes": count, "upvotedByMe": true}})
}

// RemoveUpvote handles DELETE /api/discussions/:id/upvote (authenticated).
func (dc *DiscussionController) RemoveUpvote(c *gin.Context) {
	count, err := dc.discussions.RemoveUpvote(c.Request.Context(), c.Param("id"), c.GetString("userID"))
	if err != nil {
		writeDiscussionError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"upvotes": count, "upvotedByMe": false}})
}

// Delete handles DELETE /api/discussions/:id (authenticated).
// Users may remove their own comments; admins may moderate any.
func (dc *DiscussionController) Delete(c *gin.Context) {
	isAdmin := c.GetString("role") == "admin"

	if err := dc.discussions.Delete(c.Request.Context(), c.Param("id"), c.GetString("userID"), isAdmin); err != nil {
		writeDiscussionError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Comment deleted"})
}

// writeProblemLookupError maps a problem lookup failure to a response.
func writeProblemLookupError(c *gin.Context, err error) {
	if errors.Is(err, problem.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Problem not found"})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to fetch problem"})
}

// writeDiscussionError maps discussion service errors to responses.
func writeDiscussionError(c *gin.Context, err error) {
	var vErr discussion.ValidationError
	switch {
	case errors.As(err, &vErr):
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Validation failed", "errors": []string{vErr.Error()}})
	case errors.Is(err, discussion.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Comment not found"})
	case errors.Is(err, discussion.ErrNestingTooDeep):
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
	case errors.Is(err, discussion.ErrNotAuthor):
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Discussion request failed"})
	}
}
