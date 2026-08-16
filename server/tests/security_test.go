package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/controllers"
	"github.com/toji339/online-judge/internal/judge"
	"github.com/toji339/online-judge/internal/middleware"
	"github.com/toji339/online-judge/internal/problem"
	problemmongo "github.com/toji339/online-judge/internal/problem/mongorepo"
	"github.com/toji339/online-judge/internal/ratelimit"
)

// recordingSandbox captures the limits it is asked for, so a test can
// assert on what the controller actually requested of Docker.
type recordingSandbox struct{ lastLimits judge.Limits }

func (s *recordingSandbox) NewSubmission(_ context.Context, _ string, _ string, limits judge.Limits) (judge.SubmissionSandbox, error) {
	s.lastLimits = limits
	return &scriptedSubmission{parent: &scriptedSandbox{runs: []judge.ExecuteResult{{Stdout: "", ExitCode: 0}}}}, nil
}

// =============================================================
// Playground resource limits
// =============================================================

// TestPlayground_ClampsClientSuppliedMemoryLimit covers the worst issue
// the audit found: the request body chose the container's memory cap, so
// asking for more than the host has made the cap stop binding and let a
// program run the machine out of memory.
func TestPlayground_ClampsClientSuppliedMemoryLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sandbox := &recordingSandbox{}
	router := gin.New()
	router.POST("/api/judge/run", controllers.NewJudgeController(sandbox).RunCode)

	body := `{"language":"python","code":"print(1)","memory_limit_mb":65536,"time_limit_ms":600000}`
	req := httptest.NewRequest(http.MethodPost, "/api/judge/run", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.LessOrEqual(t, sandbox.lastLimits.MemoryLimitMB, int64(512),
		"a client must not be able to widen the sandbox memory ceiling")
	assert.LessOrEqual(t, sandbox.lastLimits.TimeLimit, 10*time.Second,
		"a client must not be able to widen the time ceiling")
}

func TestPlayground_HonoursASmallerRequestedLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sandbox := &recordingSandbox{}
	router := gin.New()
	router.POST("/api/judge/run", controllers.NewJudgeController(sandbox).RunCode)

	body := `{"language":"python","code":"print(1)","memory_limit_mb":64,"time_limit_ms":500}`
	req := httptest.NewRequest(http.MethodPost, "/api/judge/run", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int64(64), sandbox.lastLimits.MemoryLimitMB, "asking for less is allowed")
	assert.Equal(t, 500*time.Millisecond, sandbox.lastLimits.TimeLimit)
}

func TestPlayground_RejectsAFloodOfTestCases(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/judge/run", controllers.NewJudgeController(&recordingSandbox{}).RunCode)

	cases := make([]string, 500)
	for i := range cases {
		cases[i] = `{"Input":"1","ExpectedOutput":"1"}`
	}
	body := fmt.Sprintf(`{"language":"python","code":"print(1)","test_cases":[%s]}`, strings.Join(cases, ","))

	req := httptest.NewRequest(http.MethodPost, "/api/judge/run", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"each test case is a container exec, so the count must be bounded")
}

// =============================================================
// Middleware
// =============================================================

func TestMaxBodySize_RejectsAnOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.MaxBodySize(1024))
	router.POST("/echo", func(c *gin.Context) {
		var body map[string]any
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"success": false})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	huge := fmt.Sprintf(`{"code":%q}`, strings.Repeat("x", 5000))
	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(huge))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

func TestSecurityHeaders_AreSetOnEveryResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.SecurityHeaders(true))
	router.GET("/x", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{}) })

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))

	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
	assert.Equal(t, "no-referrer", w.Header().Get("Referrer-Policy"))
	assert.Contains(t, w.Header().Get("Strict-Transport-Security"), "max-age=")
}

func TestSecurityHeaders_OmitsHSTSWithoutTLS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.SecurityHeaders(false))
	router.GET("/x", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{}) })

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))

	assert.Empty(t, w.Header().Get("Strict-Transport-Security"),
		"HSTS over plain HTTP would lock a developer out of localhost for a year")
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
}

// countingLimiter allows the first n calls and refuses the rest.
type countingLimiter struct {
	allowed int
	seen    []string
}

func (l *countingLimiter) Allow(_ context.Context, key string, _ int, _ time.Duration) (bool, time.Duration) {
	l.seen = append(l.seen, key)
	if len(l.seen) <= l.allowed {
		return true, 0
	}
	return false, 30 * time.Second
}

// TestRateLimitByIP_ThrottlesAnonymousCallers is what protects login:
// the per-user limiter cannot, because there is no user yet.
func TestRateLimitByIP_ThrottlesAnonymousCallers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := &countingLimiter{allowed: 2}
	router := gin.New()
	router.POST("/login",
		middleware.RateLimitByIP(limiter, "auth-login", 2, time.Minute),
		func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"success": true}) })

	codes := make([]int, 4)
	for i := range codes {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/login", nil))
		codes[i] = w.Code
	}

	assert.Equal(t, []int{200, 200, 429, 429}, codes)
	assert.Contains(t, limiter.seen[0], "auth-login:", "the counter is keyed by route and address")
}

func TestRateLimitByIP_SetsRetryAfter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/login",
		middleware.RateLimitByIP(&countingLimiter{allowed: 0}, "auth-login", 1, time.Minute),
		func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{}) })

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/login", nil))

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.NotEmpty(t, w.Header().Get("Retry-After"))
}

// TestAllowAllLimiter_PermitsEverything documents the graceful-degradation
// path used when Redis is absent.
func TestAllowAllLimiter_PermitsEverything(t *testing.T) {
	allowed, wait := ratelimit.AllowAll{}.Allow(t.Context(), "any", 1, time.Minute)

	assert.True(t, allowed)
	assert.Zero(t, wait)
}

// =============================================================
// Pagination bounds
// =============================================================

// TestProblemList_CapsPageSize stops one unauthenticated request from
// pulling the whole collection, statements included.
func TestProblemList_CapsPageSize(t *testing.T) {
	clearSubmissions(t)
	svc := problem.NewService(problemmongo.New(testDB))
	for range 3 {
		seedProblem(t, svc)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/problems", controllers.NewProblemController(svc).ListProblems)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/problems?pageSize=1000000", nil))

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data     []problem.Problem `json:"data"`
		PageSize int               `json:"pageSize"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.LessOrEqual(t, len(resp.Data), 100, "the service clamps the page size regardless of the request")
}
