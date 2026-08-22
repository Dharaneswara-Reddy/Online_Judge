// Package ratelimit implements a fixed-window counter used to curb spam
// on the write-heavy, user-generated endpoints (discussion posts and
// company tags) and to throttle the unauthenticated auth endpoints by
// address.
//
// The counter lives in Redis so the limit is shared across every API
// instance.
//
// There are two distinct "Redis is not answering" situations and they
// are handled differently on purpose:
//
//   - Redis was never configured. This is a supported deployment mode
//     (see CLAUDE.md), and the API substitutes AllowAll: every request
//     is permitted and nothing is counted.
//   - Redis is configured but erroring. Allow degrades open, because a
//     spam control is not worth an outage. AllowWithError surfaces the
//     failure so a caller that guards something worth failing closed for
//     — password guessing — can refuse instead.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Limiter answers whether one more action is allowed right now.
type Limiter interface {
	// Allow reports whether the key may act again, and how long the
	// caller should wait when it may not.
	//
	// Implementations backed by an external store degrade open here: a
	// store failure is reported as "allowed". Callers that must not do
	// that should type-assert to FallibleLimiter.
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration)
}

// FallibleLimiter is a Limiter whose counter can fail independently of
// the answer, so the caller can choose its own failure mode.
//
// Only limiters that actually talk to a store implement it. AllowAll
// deliberately does not: "no Redis configured" must never be mistaken
// for "Redis is down", because the two call for opposite responses.
type FallibleLimiter interface {
	Limiter

	// AllowWithError is Allow without the degrade-open behaviour. When
	// err is non-nil the verdict is unknown and allowed is false.
	AllowWithError(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error)
}

// RedisLimiter is a fixed-window counter backed by Redis.
type RedisLimiter struct {
	client *redis.Client
}

// NewRedisLimiter creates a limiter over an existing Redis client.
func NewRedisLimiter(client *redis.Client) *RedisLimiter {
	return &RedisLimiter{client: client}
}

// compile-time check that the Redis limiter offers the fallible contract.
var _ FallibleLimiter = (*RedisLimiter)(nil)

// AllowWithError increments the window counter for key and compares it
// to limit, reporting a store failure rather than swallowing it.
//
// The first increment in a window also sets the expiry, so the whole
// window is discarded automatically once it passes and no cleanup job is
// needed.
func (l *RedisLimiter) AllowWithError(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	// Bucket the key by window so a new window starts with a fresh count.
	bucket := time.Now().UnixNano() / int64(window)
	redisKey := fmt.Sprintf("ratelimit:%s:%d", key, bucket)

	pipe := l.client.TxPipeline()
	incr := pipe.Incr(ctx, redisKey)
	pipe.Expire(ctx, redisKey, window)
	if _, err := pipe.Exec(ctx); err != nil {
		// The verdict is genuinely unknown. Saying "allowed" here would
		// be a lie the caller cannot detect, which is how the previous
		// version silently disabled brute-force protection.
		return false, 0, fmt.Errorf("ratelimit: counter unavailable: %w", err)
	}

	if incr.Val() > int64(limit) {
		ttl, err := l.client.TTL(ctx, redisKey).Result()
		if err != nil || ttl < 0 {
			ttl = window
		}
		return false, ttl, nil
	}
	return true, 0, nil
}

// Allow is the degrade-open form of AllowWithError, used by the spam
// limiters: never block a legitimate user because the cache is down.
func (l *RedisLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration) {
	allowed, retryAfter, err := l.AllowWithError(ctx, key, limit, window)
	if err != nil {
		return true, 0
	}
	return allowed, retryAfter
}

// AllowAll is a Limiter that permits everything. It is used when Redis is
// not configured, keeping call sites free of nil checks.
//
// It intentionally does not implement FallibleLimiter — see the package
// comment: an unconfigured limiter is a deployment choice, not a fault,
// and must not cause the auth endpoints to fail closed.
type AllowAll struct{}

func (AllowAll) Allow(context.Context, string, int, time.Duration) (bool, time.Duration) {
	return true, 0
}
