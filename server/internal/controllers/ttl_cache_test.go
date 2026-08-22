package controllers

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTTLCache_ServesTheCachedValueWithinTheWindow is the property the
// stats DoS fix rests on: the expensive loader runs once per window
// however many callers arrive.
func TestTTLCache_ServesTheCachedValueWithinTheWindow(t *testing.T) {
	var calls atomic.Int64
	cache := newTTLCache[int](time.Minute)

	load := func(context.Context) (int, error) {
		calls.Add(1)
		return 42, nil
	}

	for range 100 {
		got, err := cache.Get(t.Context(), load)
		require.NoError(t, err)
		assert.Equal(t, 42, got)
	}

	assert.Equal(t, int64(1), calls.Load())
}

// TestTTLCache_RefreshesAfterTheWindow proves the value does not go
// stale forever.
func TestTTLCache_RefreshesAfterTheWindow(t *testing.T) {
	var calls atomic.Int64
	cache := newTTLCache[int](time.Minute)

	now := time.Now()
	cache.now = func() time.Time { return now }

	load := func(context.Context) (int, error) {
		return int(calls.Add(1)), nil
	}

	first, err := cache.Get(t.Context(), load)
	require.NoError(t, err)
	assert.Equal(t, 1, first)

	// Still inside the window.
	now = now.Add(59 * time.Second)
	again, err := cache.Get(t.Context(), load)
	require.NoError(t, err)
	assert.Equal(t, 1, again)

	// Window has passed.
	now = now.Add(2 * time.Second)
	fresh, err := cache.Get(t.Context(), load)
	require.NoError(t, err)
	assert.Equal(t, 2, fresh)
}

// TestTTLCache_CollapsesAConcurrentStampede is the part that matters
// under attack: a burst of simultaneous requests must not each start
// their own full collection scan.
func TestTTLCache_CollapsesAConcurrentStampede(t *testing.T) {
	var calls atomic.Int64
	cache := newTTLCache[int](time.Minute)

	release := make(chan struct{})
	load := func(context.Context) (int, error) {
		calls.Add(1)
		<-release // hold the first loader open while the rest pile up
		return 7, nil
	}

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := cache.Get(context.Background(), load)
			assert.NoError(t, err)
			assert.Equal(t, 7, got)
		}()
	}

	// Give the goroutines a moment to contend, then let the loader finish.
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	assert.Equal(t, int64(1), calls.Load())
}

// TestTTLCache_DoesNotCacheAFailure keeps a transient database error
// from being pinned for the whole window.
func TestTTLCache_DoesNotCacheAFailure(t *testing.T) {
	cache := newTTLCache[int](time.Minute)

	_, err := cache.Get(t.Context(), func(context.Context) (int, error) {
		return 0, errors.New("boom")
	})
	require.Error(t, err)

	got, err := cache.Get(t.Context(), func(context.Context) (int, error) {
		return 5, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 5, got)
}
