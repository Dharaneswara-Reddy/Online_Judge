package rabbitmq

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/toji339/online-judge/internal/queue"
)

// unreachableBroker points at a port nothing listens on, so a dial fails
// immediately with "connection refused" rather than timing out.
const unreachableBroker = "amqp://guest:guest@127.0.0.1:1/"

// TestPublish_ReDialsOnceThenBacksOff is the P1-8 defect seen from the
// API's side.
//
// The API used to hold a publisher that had connected exactly once, so a
// broker restart left it dead for the process lifetime. The fix is a lazy
// client that re-dials on demand — but re-dialing on *every* publish would
// charge each request a full dial timeout, turning a broker outage into a
// pile of hanging HTTP requests. So the first failure dials, and the ones
// behind it fail fast until the cooldown expires.
func TestPublish_ReDialsOnceThenBacksOff(t *testing.T) {
	c := New(unreachableBroker)
	defer c.Close()

	job := queue.Job{SubmissionID: "sub-1"}

	if err := c.Publish(context.Background(), queue.LaneStandard, job); err == nil {
		t.Fatal("publishing to an unreachable broker must fail")
	}
	afterFirst := c.dialAttempts.Load()
	if afterFirst == 0 {
		t.Fatal("the first publish must attempt a dial — that is what recovers the connection")
	}

	// Several more publishes while the broker is still down.
	for i := 0; i < 5; i++ {
		err := c.Publish(context.Background(), queue.LaneStandard, job)
		if !errors.Is(err, queue.ErrUnavailable) {
			t.Fatalf("publish %d: got %v, want queue.ErrUnavailable", i, err)
		}
	}

	if got := c.dialAttempts.Load(); got != afterFirst {
		t.Errorf("dial attempts = %d after the cooldown started, want %d — "+
			"every request re-dialing is how a broker outage becomes an API outage", got, afterFirst)
	}
}

// TestPublish_RetriesTheDialOnceTheCooldownExpires proves the back-off is a
// delay and not a permanent giving-up: an API that stopped trying would be
// the original bug wearing a different hat.
func TestPublish_RetriesTheDialOnceTheCooldownExpires(t *testing.T) {
	c := New(unreachableBroker)
	defer c.Close()

	job := queue.Job{SubmissionID: "sub-1"}
	_ = c.Publish(context.Background(), queue.LaneStandard, job)
	afterFirst := c.dialAttempts.Load()

	// Expire the cooldown rather than sleeping through it.
	c.mu.Lock()
	c.nextPublishDial = time.Now().Add(-time.Second)
	c.mu.Unlock()

	_ = c.Publish(context.Background(), queue.LaneStandard, job)

	if got := c.dialAttempts.Load(); got <= afterFirst {
		t.Errorf("dial attempts = %d, want more than %d — the client must try again "+
			"once the cooldown passes, or it never recovers", got, afterFirst)
	}
}

// TestPublish_ConcurrentPublishesShareOneDial covers the requirement that
// concurrent requests cannot open uncontrolled duplicate connections. Fifty
// simultaneous submissions against a dead broker must not produce fifty
// dials.
func TestPublish_ConcurrentPublishesShareOneDial(t *testing.T) {
	c := New(unreachableBroker)
	defer c.Close()

	const callers = 50
	done := make(chan struct{})
	for i := 0; i < callers; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			_ = c.Publish(context.Background(), queue.LaneStandard, queue.Job{SubmissionID: "sub"})
		}()
	}
	for i := 0; i < callers; i++ {
		<-done
	}

	// A small number is fine — the race between the cooldown being set and
	// other goroutines reading it can let a few through. Fifty is not.
	if got := c.dialAttempts.Load(); got > 5 {
		t.Errorf("dial attempts = %d for %d concurrent publishes, want a small number", got, callers)
	}
}

// TestPublish_CredentialsNeverReachTheError guards P1-19's sibling: the AMQP
// URL embeds a password, and an unavailable-broker error is exactly the sort
// of string that gets logged.
func TestPublish_CredentialsNeverReachTheError(t *testing.T) {
	c := New("amqp://someuser:supersecretpassword@127.0.0.1:1/")
	defer c.Close()

	err := c.Publish(context.Background(), queue.LaneStandard, queue.Job{SubmissionID: "sub-1"})
	if err == nil {
		t.Fatal("expected a failure")
	}
	for _, secret := range []string{"supersecretpassword", "someuser"} {
		if contains(err.Error(), secret) {
			t.Errorf("error text leaks %q: %v", secret, err)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		}()
}

// TestNextFailureCount_TreatsALongRunAsAFreshIncident covers the retry
// loop's hot-spin guard.
//
// ensureConnection only backs off when the connection is down. A channel
// that fails on a healthy connection returns instantly, so the consumer
// loop re-opened and failed at full speed, starving the lane. Consecutive
// quick failures escalate the delay; a consumer that ran normally for a
// while before failing starts over, so one blip does not permanently slow
// recovery.
func TestNextFailureCount_TreatsALongRunAsAFreshIncident(t *testing.T) {
	justNow := time.Now()
	if got := nextFailureCount(4, justNow); got != 5 {
		t.Errorf("a quick failure after 4 others = %d, want 5 (the delay must escalate)", got)
	}

	longRun := time.Now().Add(-2 * healthyRunDuration)
	if got := nextFailureCount(4, longRun); got != 1 {
		t.Errorf("a failure after a healthy run = %d, want 1 (a blip is not an outage)", got)
	}
}

// The escalation has to actually produce a growing, bounded delay,
// otherwise the counter above is decorative.
func TestBackoffFor_GrowsAndStaysBounded(t *testing.T) {
	first := backoffFor(1)
	later := backoffFor(6)

	if later <= first {
		t.Errorf("backoff did not grow: attempt 1 = %s, attempt 6 = %s", first, later)
	}
	if ceiling := 2 * maxBackoff; later > ceiling {
		t.Errorf("backoff %s exceeded its ceiling %s — an unbounded delay is its own outage", later, ceiling)
	}
}

// sleepBeforeRetry must abandon the wait when the process is shutting
// down, or a worker would hang for the whole backoff on SIGTERM.
func TestSleepBeforeRetry_ReturnsImmediatelyOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	if sleepBeforeRetry(ctx, 8) {
		t.Error("a cancelled context must not report a completed wait")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("waited %s after cancellation — shutdown must not block on backoff", elapsed)
	}
}
