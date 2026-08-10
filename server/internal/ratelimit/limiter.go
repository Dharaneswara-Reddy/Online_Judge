// Package ratelimit implements a fixed-window counter used to curb spam
// on the write-heavy, user-generated endpoints: discussion posts and
// company tags.
//
// The counter lives in Redis so the limit is shared across every API
// instance. When Redis is unavailable the limiter fails open — a rate
// limit is a spam control, not an authorization boundary, so losing it
// must never take the platform down with it.
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
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration)
}

// RedisLimiter is a fixed-window counter backed by Redis.
type RedisLimiter struct {
	client *redis.Client
}

// NewRedisLimiter creates a limiter over an existing Redis client.
func NewRedisLimiter(client *redis.Client) *RedisLimiter {
	return &RedisLimiter{client: client}
}

// Allow increments the window counter for key and compares it to limit.
//
// The first increment in a window also sets the expiry, so the whole
// window is discarded automatically once it passes and no cleanup job is
// needed.
func (l *RedisLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration) {
	// Bucket the key by window so a new window starts with a fresh count.
	bucket := time.Now().UnixNano() / int64(window)
	redisKey := fmt.Sprintf("ratelimit:%s:%d", key, bucket)

	pipe := l.client.TxPipeline()
	incr := pipe.Incr(ctx, redisKey)
	pipe.Expire(ctx, redisKey, window)
	if _, err := pipe.Exec(ctx); err != nil {
		// Fail open: never block a legitimate user because the cache is down.
		return true, 0
	}

	if incr.Val() > int64(limit) {
		ttl, err := l.client.TTL(ctx, redisKey).Result()
		if err != nil || ttl < 0 {
			ttl = window
		}
		return false, ttl
	}
	return true, 0
}

// AllowAll is a Limiter that permits everything. It is used when Redis is
// not configured, keeping call sites free of nil checks.
type AllowAll struct{}

func (AllowAll) Allow(context.Context, string, int, time.Duration) (bool, time.Duration) {
	return true, 0
}
