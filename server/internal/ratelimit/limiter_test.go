package ratelimit_test

import (
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/ratelimit"
)

// unreachableRedis returns a client pointed at a closed port, which is
// what a Redis outage looks like from this process.
func unreachableRedis(t *testing.T) *redis.Client {
	t.Helper()
	return redis.NewClient(&redis.Options{
		// Port 1 is reserved and never listening, so the dial fails
		// immediately rather than hanging the test.
		Addr:        "127.0.0.1:1",
		DialTimeout: 200 * time.Millisecond,
		MaxRetries:  -1,
	})
}

// TestRedisLimiter_ReportsTheOutageInsteadOfHidingIt is the contract the
// fail-closed auth middleware depends on: a broken counter must be
// distinguishable from an allowed request.
func TestRedisLimiter_ReportsTheOutageInsteadOfHidingIt(t *testing.T) {
	limiter := ratelimit.NewRedisLimiter(unreachableRedis(t))

	allowed, _, err := limiter.AllowWithError(t.Context(), "auth-login:1.2.3.4", 5, time.Minute)

	require.Error(t, err, "a Redis failure must surface to the caller")
	assert.False(t, allowed, "an unanswerable counter must not report the action as allowed")
}

// TestRedisLimiter_AllowStillFailsOpen documents the deliberate
// behaviour for the spam limiters: losing Redis must not take down
// commenting or tagging.
func TestRedisLimiter_AllowStillFailsOpen(t *testing.T) {
	limiter := ratelimit.NewRedisLimiter(unreachableRedis(t))

	allowed, wait := limiter.Allow(t.Context(), "discussion-post:abc", 5, time.Minute)

	assert.True(t, allowed)
	assert.Zero(t, wait)
}

// TestAllowAll_IsNotFallible pins the structural difference the
// middleware uses to tell "Redis was never configured" (a supported
// deployment mode) from "Redis is configured but broken" (an outage).
func TestAllowAll_IsNotFallible(t *testing.T) {
	var limiter ratelimit.Limiter = ratelimit.AllowAll{}

	_, isFallible := limiter.(ratelimit.FallibleLimiter)
	assert.False(t, isFallible, "the no-Redis stand-in must never look like a broken counter")

	allowed, wait := limiter.Allow(t.Context(), "any", 1, time.Minute)
	assert.True(t, allowed)
	assert.Zero(t, wait)
}

// TestRedisLimiter_IsFallible is the other half of that pair.
func TestRedisLimiter_IsFallible(t *testing.T) {
	var limiter ratelimit.Limiter = ratelimit.NewRedisLimiter(unreachableRedis(t))

	_, isFallible := limiter.(ratelimit.FallibleLimiter)
	assert.True(t, isFallible)
}
