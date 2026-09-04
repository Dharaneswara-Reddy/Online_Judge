package assist

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestMemoryCacheRoundTrip(t *testing.T) {
	c := NewMemoryCache(time.Minute, 8)

	if _, ok := c.Get("absent"); ok {
		t.Fatal("Get on an empty cache reported a hit")
	}

	c.Set("k", "v")
	got, ok := c.Get("k")
	if !ok || got != "v" {
		t.Fatalf("Get(k) = %q, %v; want \"v\", true", got, ok)
	}
}

// TestMemoryCacheExpires uses an injected clock rather than a sleep: a
// test that waits for a TTL is a test that is slow when it passes and
// flaky when the machine is busy.
func TestMemoryCacheExpires(t *testing.T) {
	now := time.Unix(1700000000, 0)
	c := newMemoryCache(time.Minute, 8, func() time.Time { return now })

	c.Set("k", "v")
	now = now.Add(59 * time.Second)
	if _, ok := c.Get("k"); !ok {
		t.Fatal("entry expired before its TTL elapsed")
	}

	now = now.Add(2 * time.Second)
	if _, ok := c.Get("k"); ok {
		t.Fatal("entry survived past its TTL")
	}
}

func TestMemoryCacheEvictsOldestWhenFull(t *testing.T) {
	c := NewMemoryCache(time.Minute, 3)

	for i := 0; i < 4; i++ {
		c.Set(fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i))
	}

	if _, ok := c.Get("k0"); ok {
		t.Fatal("k0 should have been evicted to make room for k3")
	}
	for _, k := range []string{"k1", "k2", "k3"} {
		if _, ok := c.Get(k); !ok {
			t.Fatalf("%s was evicted but should have been retained", k)
		}
	}
}

func TestMemoryCacheOverwriteDoesNotGrow(t *testing.T) {
	c := NewMemoryCache(time.Minute, 2)

	c.Set("a", "1")
	c.Set("a", "2")
	c.Set("b", "3")

	if got, ok := c.Get("a"); !ok || got != "2" {
		t.Fatalf("Get(a) = %q, %v; want \"2\", true — overwrite must not evict", got, ok)
	}
	if _, ok := c.Get("b"); !ok {
		t.Fatal("b was evicted although only two distinct keys exist")
	}
}

// TestMemoryCacheIsConcurrencySafe exists because the API serves
// requests in parallel and every one of them may touch this map. Run it
// under -race for it to mean anything.
func TestMemoryCacheIsConcurrencySafe(t *testing.T) {
	c := NewMemoryCache(time.Minute, 16)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				key := fmt.Sprintf("k%d", (i+j)%32)
				c.Set(key, "v")
				c.Get(key)
			}
		}(i)
	}
	wg.Wait()
}

// TestMemoryCacheUnboundedOptions documents the degenerate settings so
// nobody has to guess what a zero means.
func TestMemoryCacheUnboundedOptions(t *testing.T) {
	c := newMemoryCache(0, 0, time.Now)
	c.Set("k", "v")
	if _, ok := c.Get("k"); !ok {
		t.Fatal("a zero TTL should mean no expiry, not instant expiry")
	}
}
