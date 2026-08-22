package controllers

import (
	"context"
	"sync"
	"time"
)

// ttlCache holds one value for a fixed window and collapses concurrent
// misses into a single load.
//
// It exists for the public landing-page summary, which was an
// unauthenticated request that ran four collection counts — one of them
// a full scan of the largest collection — plus a war-room sweep that
// also wrote. Serving that live meant anyone could turn one cheap HTTP
// request into a lot of database work, repeatedly.
//
// The single mutex is held across the load on purpose. A read-lock /
// double-check arrangement would let a burst of simultaneous misses each
// start their own scan, which is precisely the traffic pattern this is
// meant to stop; serialising them means the tenth caller waits on the
// first caller's answer rather than adding a tenth query.
//
// It is a plain value with no package-level state, so a caller owns its
// own cache and tests get a fresh one.
type ttlCache[T any] struct {
	ttl time.Duration
	// now is injectable so tests can advance the window without sleeping.
	now func() time.Time

	mu        sync.Mutex
	value     T
	expiresAt time.Time
	valid     bool
}

// newTTLCache creates an empty cache whose entries live for ttl.
func newTTLCache[T any](ttl time.Duration) *ttlCache[T] {
	return &ttlCache[T]{ttl: ttl, now: time.Now}
}

// Get returns the cached value, calling load when there is nothing fresh
// to serve. A failed load is not cached, so a transient database error
// does not pin an error for the whole window.
func (c *ttlCache[T]) Get(ctx context.Context, load func(context.Context) (T, error)) (T, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.valid && c.now().Before(c.expiresAt) {
		return c.value, nil
	}

	value, err := load(ctx)
	if err != nil {
		var zero T
		return zero, err
	}

	c.value = value
	c.expiresAt = c.now().Add(c.ttl)
	c.valid = true
	return value, nil
}
