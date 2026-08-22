package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/middleware"
)

// recordingLimiter remembers every key it was asked about so a test can
// assert what the rate limiter actually counted against.
type recordingLimiter struct {
	keys []string
}

func (l *recordingLimiter) Allow(_ context.Context, key string, _ int, _ time.Duration) (bool, time.Duration) {
	l.keys = append(l.keys, key)
	return true, 0
}

// newProxyTestRouter builds a router hardened the same way the real API is.
func newProxyTestRouter(t *testing.T, limiter *recordingLimiter) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	require.NoError(t, middleware.ApplyTrustedProxies(router))
	router.POST("/login",
		middleware.RateLimitByIP(limiter, "auth-login", 100, time.Minute),
		func(c *gin.Context) { c.Status(http.StatusOK) })
	return router
}

// TestTrustedProxies_SpoofedForwardedForDoesNotChangeTheKey is the
// regression test for the bypass: an attacker on the public internet
// rotating X-Forwarded-For must stay in one rate-limit bucket.
func TestTrustedProxies_SpoofedForwardedForDoesNotChangeTheKey(t *testing.T) {
	limiter := &recordingLimiter{}
	router := newProxyTestRouter(t, limiter)

	for _, spoofed := range []string{"9.9.9.1", "9.9.9.2", "9.9.9.3, 10.0.0.5", ""} {
		req := httptest.NewRequest(http.MethodPost, "/login", nil)
		// A direct caller from a public address — not one of our proxies.
		req.RemoteAddr = "203.0.113.7:44321"
		if spoofed != "" {
			req.Header.Set("X-Forwarded-For", spoofed)
			req.Header.Set("X-Real-IP", spoofed)
		}
		router.ServeHTTP(httptest.NewRecorder(), req)
	}

	require.Len(t, limiter.keys, 4)
	for _, key := range limiter.keys {
		assert.Equal(t, "auth-login:203.0.113.7", key,
			"a client-supplied header must never influence the rate-limit key")
	}
}

// TestTrustedProxies_HonoursTheForwardedHeaderFromOurOwnProxy proves the
// fix did not simply blind the API: a request arriving from the reverse
// proxy on the private container network is still attributed to the real
// client, otherwise every user would share one bucket.
func TestTrustedProxies_HonoursTheForwardedHeaderFromOurOwnProxy(t *testing.T) {
	limiter := &recordingLimiter{}
	router := newProxyTestRouter(t, limiter)

	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = "172.18.0.4:51234" // nginx, on the compose network
	req.Header.Set("X-Forwarded-For", "198.51.100.23")
	router.ServeHTTP(httptest.NewRecorder(), req)

	require.Len(t, limiter.keys, 1)
	assert.Equal(t, "auth-login:198.51.100.23", limiter.keys[0])
}

// TestTrustedProxies_ProxyCannotBeImpersonatedFromOutside checks the
// combination that made the original bug exploitable: a public client
// forging a chain that ends in a private address must not have the
// private hop stripped and the forged head believed.
func TestTrustedProxies_ProxyCannotBeImpersonatedFromOutside(t *testing.T) {
	limiter := &recordingLimiter{}
	router := newProxyTestRouter(t, limiter)

	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = "203.0.113.7:44321"
	req.Header.Set("X-Forwarded-For", "9.9.9.9, 172.18.0.4")
	router.ServeHTTP(httptest.NewRecorder(), req)

	require.Len(t, limiter.keys, 1)
	assert.Equal(t, "auth-login:203.0.113.7", limiter.keys[0])
}
