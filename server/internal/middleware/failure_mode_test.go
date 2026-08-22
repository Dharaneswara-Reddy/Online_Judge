package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/toji339/online-judge/internal/middleware"
	"github.com/toji339/online-judge/internal/ratelimit"
)

// brokenLimiter is a configured counter whose backing store is down.
type brokenLimiter struct{}

func (brokenLimiter) Allow(context.Context, string, int, time.Duration) (bool, time.Duration) {
	return true, 0
}

func (brokenLimiter) AllowWithError(context.Context, string, int, time.Duration) (bool, time.Duration, error) {
	return false, 0, errors.New("redis: connection refused")
}

// compile-time proof that the fake models the real thing.
var _ ratelimit.FallibleLimiter = brokenLimiter{}

func runGuarded(t *testing.T, guard gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/x", guard, func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.RemoteAddr = "203.0.113.7:1234"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// TestRateLimitAuthByIP_FailsClosedWhenTheCounterIsBroken is the point of
// the whole finding: brute-force protection must not evaporate because
// Redis blipped.
func TestRateLimitAuthByIP_FailsClosedWhenTheCounterIsBroken(t *testing.T) {
	w := runGuarded(t, middleware.RateLimitAuthByIP(brokenLimiter{}, "auth-login", 10, time.Minute))

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.NotEmpty(t, w.Header().Get("Retry-After"))
	assert.Contains(t, w.Body.String(), `"success":false`)
	assert.Contains(t, w.Body.String(), `"errors"`)
}

// TestRateLimitAuthByIP_AllowsTrafficWhenRedisWasNeverConfigured keeps
// the documented deployment mode working: no Redis at all is a choice,
// not an outage, and must not lock everyone out of logging in.
func TestRateLimitAuthByIP_AllowsTrafficWhenRedisWasNeverConfigured(t *testing.T) {
	w := runGuarded(t, middleware.RateLimitAuthByIP(ratelimit.AllowAll{}, "auth-login", 10, time.Minute))

	assert.Equal(t, http.StatusOK, w.Code)
}

// TestRateLimitByIP_StillFailsOpen documents that the non-auth variant
// keeps degrading gracefully — a public read path is not worth a 503.
func TestRateLimitByIP_StillFailsOpen(t *testing.T) {
	w := runGuarded(t, middleware.RateLimitByIP(brokenLimiter{}, "stats-summary", 10, time.Minute))

	assert.Equal(t, http.StatusOK, w.Code)
}

// TestRateLimit_PerUserStillFailsOpen covers the spam limiters.
func TestRateLimit_PerUserStillFailsOpen(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/x",
		func(c *gin.Context) { c.Set("userID", "u1") },
		middleware.RateLimit(brokenLimiter{}, "discussion-post", 5, time.Minute),
		func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/x", nil))
	assert.Equal(t, http.StatusOK, w.Code)
}
