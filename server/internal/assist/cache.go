package assist

import (
	"sync"
	"time"
)

// Cache stores generated text keyed by a caller-computed key.
//
// The key is computed by the caller, not derived from the request
// struct, because deciding what may be shared between two students is a
// safety decision and it belongs where it can be read: see cacheKey uses
// in service.go.
type Cache interface {
	Get(key string) (string, bool)
	Set(key, value string)
}

// Cache sizing for a service constructed without one. Generations are a
// couple of kilobytes each, so a thousand of them is a rounding error
// against the API's footprint, and fifteen minutes is long enough for a
// contest's worth of students to hit the same failing case.
const (
	defaultCacheTTL     = 15 * time.Minute
	defaultCacheEntries = 1024
)

// memoryCache is a TTL-bounded, size-bounded map behind a mutex.
//
// In-process rather than Redis on purpose. The thing being cached is
// advisory prose: a second API instance regenerating it costs one model
// call, whereas a shared cache would make the assistant depend on Redis,
// and CLAUDE.md is explicit that Redis is optional. Per-instance
// duplication is the cheaper mistake.
//
// Eviction is first-in-first-out rather than least-recently-used. The
// access pattern here is a burst of interest in one problem that decays,
// which the TTL already handles; LRU would buy bookkeeping and no hits.
type memoryCache struct {
	mu         sync.Mutex
	entries    map[string]cacheEntry
	order      []string // insertion order, for FIFO eviction
	ttl        time.Duration
	maxEntries int
	now        func() time.Time
}

type cacheEntry struct {
	value   string
	expires time.Time // zero means "never"
}

// NewMemoryCache returns a Cache that holds at most maxEntries values
// for ttl each and is safe for concurrent use by the API's request
// goroutines.
//
// A ttl of zero or less means entries never expire; a maxEntries of zero
// or less means the cache is unbounded. Neither is a sensible production
// setting, but both are useful in tests and neither should be a panic.
func NewMemoryCache(ttl time.Duration, maxEntries int) Cache {
	return newMemoryCache(ttl, maxEntries, time.Now)
}

// newMemoryCache is NewMemoryCache with the clock injected, so the tests
// can expire an entry without waiting for one.
func newMemoryCache(ttl time.Duration, maxEntries int, now func() time.Time) *memoryCache {
	if now == nil {
		now = time.Now
	}
	return &memoryCache{
		entries:    make(map[string]cacheEntry),
		order:      []string{},
		ttl:        ttl,
		maxEntries: maxEntries,
		now:        now,
	}
}

// Get returns the value for key when one is present and unexpired.
func (c *memoryCache) Get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.entries[key]
	if !ok {
		return "", false
	}
	// Expiry is checked on read as well as swept on write, so a key that
	// is never written again still stops being served on time.
	if !e.expires.IsZero() && !c.now().Before(e.expires) {
		return "", false
	}
	return e.value, true
}

// Set stores value under key, sweeping expired entries and evicting the
// oldest survivors if that leaves the cache over its bound.
func (c *memoryCache) Set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	c.sweepLocked(now)

	if _, exists := c.entries[key]; !exists {
		c.order = append(c.order, key)
	}

	var expires time.Time
	if c.ttl > 0 {
		expires = now.Add(c.ttl)
	}
	c.entries[key] = cacheEntry{value: value, expires: expires}

	for c.maxEntries > 0 && len(c.entries) > c.maxEntries && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
}

// sweepLocked drops expired entries and rebuilds the insertion order so
// the two structures cannot drift apart. Callers hold c.mu.
func (c *memoryCache) sweepLocked(now time.Time) {
	if c.ttl <= 0 {
		return
	}

	kept := make([]string, 0, len(c.order))
	for _, k := range c.order {
		e, ok := c.entries[k]
		if !ok {
			continue
		}
		if !e.expires.IsZero() && !now.Before(e.expires) {
			delete(c.entries, k)
			continue
		}
		kept = append(kept, k)
	}
	c.order = kept
}
