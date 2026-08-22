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

// DefaultStatsCacheTTL is how long one collection of landing-page totals
// is reused. Long enough that a flood of anonymous requests costs one
// set of queries rather than thousands, short enough that the figures on
// the landing page still look live.
const DefaultStatsCacheTTL = 45 * time.Second

// summaryData is the landing-page payload, cached as a unit.
type summaryData struct {
	Problems       int   `json:"problems"`
	Users          int64 `json:"users"`
	Submissions    int   `json:"submissions"`
	ProblemsSolved int   `json:"problemsSolved"`
	ActiveWarRooms int   `json:"activeWarRooms"`
}

// StatsController serves the public landing page: community totals and a
// preview of recently added problems.
type StatsController struct {
	db          *mongo.Database
	problems    *problem.Service
	submissions *submission.Service
	warRooms    *warroom.Service

	// summaryCache is per-controller rather than package-level state, so
	// it is injected with the controller and a test gets a clean one.
	summaryCache *ttlCache[summaryData]
}

// NewStatsController creates the controller with its dependencies.
//
// cacheTTL bounds how often the summary's queries actually run; pass
// DefaultStatsCacheTTL unless a caller has a reason not to. A
// non-positive value falls back to the default rather than disabling the
// cache, because an uncached summary is the denial-of-service this was
// added to close.
func NewStatsController(db *mongo.Database, problems *problem.Service, submissions *submission.Service, warRooms *warroom.Service, cacheTTL time.Duration) *StatsController {
	if cacheTTL <= 0 {
		cacheTTL = DefaultStatsCacheTTL
	}
	return &StatsController{
		db:           db,
		problems:     problems,
		submissions:  submissions,
		warRooms:     warRooms,
		summaryCache: newTTLCache[summaryData](cacheTTL),
	}
}

// Summary handles GET /api/stats/summary (public).
//
// Cheap to call, expensive to serve: four counts (one of them a full
// scan of the submissions collection) plus a war-room sweep. It is
// unauthenticated, so the whole result is cached for a window and the
// route is rate limited by address — otherwise a single client can make
// the database do unbounded work by holding down refresh.
//
// The cache also bounds the write hidden in this read path.
// warroom.Service.ListOpen sweeps stale rooms with an UpdateMany before
// listing, which an anonymous GET should not be able to trigger at will;
// behind the cache it runs at most once per window. Removing the sweep
// from the read path outright belongs in the warroom service.
func (sc *StatsController) Summary(c *gin.Context) {
	// The timeout guards the queries, not the wait for a cached value.
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	data, err := sc.summaryCache.Get(ctx, sc.loadSummary)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to load statistics"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// loadSummary runs the actual counts. It is only reached on a cache miss.
func (sc *StatsController) loadSummary(ctx context.Context) (summaryData, error) {
	var data summaryData

	problems, err := sc.problems.Count(ctx, problem.ListFilter{})
	if err != nil {
		return data, err
	}

	solved, err := sc.submissions.Count(ctx, submission.ListFilter{Status: submission.StatusAccepted})
	if err != nil {
		return data, err
	}

	submissions, err := sc.submissions.Count(ctx, submission.ListFilter{})
	if err != nil {
		return data, err
	}

	users, err := sc.db.Collection("users").CountDocuments(ctx, bson.M{})
	if err != nil {
		return data, err
	}

	// Open rooms are the "join a race right now" figure on the landing page.
	openRooms, err := sc.warRooms.ListOpen(ctx, 50)
	if err != nil {
		return data, err
	}

	return summaryData{
		Problems:       problems,
		Users:          users,
		Submissions:    submissions,
		ProblemsSolved: solved,
		ActiveWarRooms: len(openRooms),
	}, nil
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
