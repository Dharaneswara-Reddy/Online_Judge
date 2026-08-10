package controllers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/toji339/online-judge/internal/problem"
	"github.com/toji339/online-judge/internal/submission"
	"github.com/toji339/online-judge/internal/warroom"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// StatsController serves the public landing page: community totals and a
// preview of recently added problems.
type StatsController struct {
	db          *mongo.Database
	problems    *problem.Service
	submissions *submission.Service
	warRooms    *warroom.Service
}

// NewStatsController creates the controller with its dependencies.
func NewStatsController(db *mongo.Database, problems *problem.Service, submissions *submission.Service, warRooms *warroom.Service) *StatsController {
	return &StatsController{db: db, problems: problems, submissions: submissions, warRooms: warRooms}
}

// Summary handles GET /api/stats/summary (public).
//
// Every figure is a count rather than a listing, so this stays cheap
// enough to serve on an unauthenticated landing page.
func (sc *StatsController) Summary(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	problems, err := sc.problems.Count(ctx, problem.ListFilter{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to load statistics"})
		return
	}

	solved, err := sc.submissions.Count(ctx, submission.ListFilter{Status: submission.StatusAccepted})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to load statistics"})
		return
	}

	submissions, err := sc.submissions.Count(ctx, submission.ListFilter{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to load statistics"})
		return
	}

	users, err := sc.db.Collection("users").CountDocuments(ctx, bson.M{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to load statistics"})
		return
	}

	// Open rooms are the "join a race right now" figure on the landing page.
	openRooms, err := sc.warRooms.ListOpen(ctx, 50)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to load statistics"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"problems":       problems,
			"users":          users,
			"submissions":    submissions,
			"problemsSolved": solved,
			"activeWarRooms": len(openRooms),
		},
	})
}

// RecentProblems handles GET /api/problems/recent (public), the preview
// list on the landing page.
func (sc *StatsController) RecentProblems(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "5"))
	if limit <= 0 || limit > 20 {
		limit = 5
	}

	// The problem listing is already sorted newest first.
	problems, err := sc.problems.List(c.Request.Context(), problem.ListFilter{Page: 1, PageSize: limit})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to load recent problems"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": problems})
}
