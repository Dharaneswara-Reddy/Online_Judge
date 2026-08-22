package tests

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/queue"
	"github.com/toji339/online-judge/internal/queue/rabbitmq"
)

// These tests exercise recovery against a real broker, because the whole
// point is behaviour the code cannot demonstrate on its own: a broker
// that goes away and comes back.
//
// On a developer machine with no Docker they skip, so `go test ./...`
// stays usable. Start one with:
//
//	docker compose up -d
//
// Anywhere the broker is EXPECTED — CI, or any run that sets
// RABBITMQ_REQUIRED — a missing or unrestartable broker is a failure, not
// a skip. A silent skip is indistinguishable from a pass in a CI log, and
// these seven tests skipped their way through every run for months.
//
// Each test gets its own namespaced queues, so a judge worker running
// against the same broker consumes the real lane queues and never sees
// this traffic. That keeps the suite deterministic whatever else is
// running locally.
//
// Environment overrides:
//
//	RABBITMQ_TEST_URL   AMQP URL to test against (default: local guest)
//	RABBITMQ_CONTAINER  container name/ID to restart (default: discovered)
//	RABBITMQ_REQUIRED   any non-empty value: never skip, always fail
var brokerURL = envOr("RABBITMQ_TEST_URL", "amqp://guest:guest@localhost:5672/")

// envOr returns the environment value for key, or def when it is unset or
// empty.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// brokerRequired reports whether a missing broker must fail rather than
// skip. CI counts on its own: GitHub Actions always sets CI=true, so
// forgetting RABBITMQ_REQUIRED cannot quietly re-enable skipping there.
func brokerRequired() bool {
	return os.Getenv("RABBITMQ_REQUIRED") != "" || os.Getenv("CI") != ""
}

// skipOrFail skips when a broker is merely optional and fails when one was
// expected, so the two cases can never look alike in a test report.
func skipOrFail(t *testing.T, format string, args ...any) {
	t.Helper()
	if brokerRequired() {
		t.Fatalf("a RabbitMQ broker is required in this environment but "+format, args...)
	}
	t.Skipf("no RabbitMQ available (start it with: docker compose up -d): "+format, args...)
}

// isolatedClient returns a client whose queues belong to this test alone,
// and registers cleanup that removes them from the broker afterwards.
//
// Without this, tests share the real lane queues with any locally running
// worker, which then consumes their messages and fails them for reasons
// unrelated to the code under test.
func isolatedClient(t *testing.T) *rabbitmq.Client {
	t.Helper()
	client := rabbitmq.NewNamespaced(brokerURL, testNamespace(t))

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.DeleteQueues(ctx); err != nil {
			t.Logf("cleanup: could not delete test queues: %v", err)
		}
		_ = client.Close()
	})
	return client
}

// namespaces memoises one prefix per test, so every client a test builds
// shares the same queues while remaining isolated from other tests.
var (
	namespaceMu sync.Mutex
	namespaces  = map[string]string{}
)

// testNamespace returns this test's queue prefix, unique to the run so
// even the same test executed twice cannot inherit stale messages.
func testNamespace(t *testing.T) string {
	t.Helper()
	namespaceMu.Lock()
	defer namespaceMu.Unlock()

	if ns, ok := namespaces[t.Name()]; ok {
		return ns
	}

	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("generate test namespace: %v", err)
	}
	safe := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	ns := fmt.Sprintf("test.%s.%s", safe, hex.EncodeToString(buf))
	namespaces[t.Name()] = ns
	return ns
}

// requireBroker ensures a broker is actually reachable. It skips only
// where a broker is optional; where one is expected it fails.
func requireBroker(t *testing.T) {
	t.Helper()
	client, err := rabbitmq.Connect(brokerURL)
	if err != nil {
		// The URL can carry credentials, so report the error only.
		skipOrFail(t, "could not connect: %v", err)
		return
	}
	_ = client.Close()
}

// brokerContainer finds the container to restart. Hardcoding a name meant
// this only ever worked against the local compose stack; on a hosted
// runner the broker is a service container with a generated name, so the
// name is discovered from the published AMQP port instead.
//
// Order: an explicit RABBITMQ_CONTAINER, then whatever publishes 5672,
// then the local compose container name.
func brokerContainer(t *testing.T) string {
	t.Helper()

	if name := os.Getenv("RABBITMQ_CONTAINER"); name != "" {
		return name
	}

	// `docker ps --filter publish=5672` matches both a hosted runner's
	// service container and any locally published broker.
	out, err := exec.Command("docker", "ps", "--filter", "publish=5672",
		"--format", "{{.ID}}").Output()
	if err == nil {
		if id := strings.TrimSpace(string(out)); id != "" {
			// One ID per line; the first is enough.
			return strings.Fields(id)[0]
		}
	}

	// Fall back to the name docker-compose.yml gives the dev broker.
	if err := exec.Command("docker", "inspect", "oj-rabbitmq").Run(); err == nil {
		return "oj-rabbitmq"
	}

	skipOrFail(t, "no RabbitMQ container could be found to restart "+
		"(set RABBITMQ_CONTAINER to name one)")
	return ""
}

// restartBroker restarts the RabbitMQ container and waits for it to accept
// connections again.
func restartBroker(t *testing.T) {
	t.Helper()

	container := brokerContainer(t)
	if container == "" {
		return // skipOrFail already stopped the test
	}

	cmd := exec.Command("docker", "restart", container)
	if out, err := cmd.CombinedOutput(); err != nil {
		skipOrFail(t, "could not restart the broker container: %v (%s)", err, out)
		return
	}

	// The container is up before AMQP is ready, so wait for a real dial.
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if client, err := rabbitmq.Connect(brokerURL); err == nil {
			_ = client.Close()
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatal("broker did not come back within 90s")
}

// =============================================================
// Test A — the broker is unavailable when the worker starts
// =============================================================

// TestRecovery_ConsumerSurvivesAnAbsentBrokerThenConnects is the
// scenario a deploy hits routinely: the worker starts before the broker
// is ready.
func TestRecovery_ConsumerSurvivesAnAbsentBrokerThenConnects(t *testing.T) {
	requireBroker(t)

	// Point at a dead port first; the client must keep retrying rather
	// than exiting or spinning.
	client := rabbitmq.New("amqp://guest:guest@127.0.0.1:1/")
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := client.Consume(ctx, queue.LaneStandard, 1, func(context.Context, queue.Job) error { return nil })
	assert.ErrorIs(t, err, context.DeadlineExceeded, "the consumer stayed alive and kept retrying")

	// Now a client pointed at the real broker consumes immediately.
	live := isolatedClient(t)

	received := make(chan string, 1)
	consumeCtx, stopConsume := context.WithCancel(context.Background())
	defer stopConsume()
	go func() {
		_ = live.Consume(consumeCtx, queue.LaneStandard, 1, func(_ context.Context, job queue.Job) error {
			received <- job.SubmissionID
			return nil
		})
	}()

	publisher := isolatedClient(t)
	require.NoError(t, publisher.Publish(context.Background(), queue.LaneStandard, queue.Job{SubmissionID: "after-wait"}))

	select {
	case id := <-received:
		assert.Equal(t, "after-wait", id)
	case <-time.After(20 * time.Second):
		t.Fatal("consumer never picked the job up")
	}
}

// =============================================================
// Test B — the broker restarts underneath a running consumer
// =============================================================

// TestRecovery_ConsumerResumesAfterBrokerRestart is the headline
// requirement: no worker restart, judging resumes by itself.
func TestRecovery_ConsumerResumesAfterBrokerRestart(t *testing.T) {
	requireBroker(t)

	client := isolatedClient(t)

	var handled int64
	received := make(chan string, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = client.Consume(ctx, queue.LaneStandard, 2, func(_ context.Context, job queue.Job) error {
			atomic.AddInt64(&handled, 1)
			received <- job.SubmissionID
			return nil
		})
	}()

	// Prove it works before the disruption.
	publisher := isolatedClient(t)
	require.NoError(t, publisher.Publish(context.Background(), queue.LaneStandard, queue.Job{SubmissionID: "before"}))
	select {
	case id := <-received:
		require.Equal(t, "before", id)
	case <-time.After(20 * time.Second):
		t.Fatal("the consumer never worked in the first place")
	}
	_ = publisher.Close()

	// Take the broker away and bring it back.
	restartBroker(t)

	// The same consumer, never restarted, must pick up new work.
	after := isolatedClient(t)

	var published bool
	for attempt := range 30 {
		if err := after.Publish(context.Background(), queue.LaneStandard, queue.Job{SubmissionID: "after-restart"}); err == nil {
			published = true
			break
		}
		_ = attempt
		time.Sleep(time.Second)
	}
	require.True(t, published, "could not publish after the restart")

	select {
	case id := <-received:
		assert.Equal(t, "after-restart", id, "the consumer recovered on its own")
	case <-time.After(60 * time.Second):
		t.Fatalf("consumer never recovered (handled %d before the restart)", atomic.LoadInt64(&handled))
	}
}

// =============================================================
// Test C — several workers recover together
// =============================================================

func TestRecovery_MultipleConsumersAllResume(t *testing.T) {
	requireBroker(t)

	const workers = 3
	var handled int64
	received := make(chan string, 32)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clients := make([]*rabbitmq.Client, workers)
	for i := range workers {
		// Same namespace as the publisher below, so all three consumers
		// share this test's queues and none touch the real lane queues.
		clients[i] = rabbitmq.NewNamespaced(brokerURL, testNamespace(t))
		defer clients[i].Close()

		go func(c *rabbitmq.Client) {
			_ = c.Consume(ctx, queue.LaneStandard, 1, func(_ context.Context, job queue.Job) error {
				atomic.AddInt64(&handled, 1)
				received <- job.SubmissionID
				return nil
			})
		}(clients[i])
	}
	time.Sleep(3 * time.Second) // let every consumer register

	restartBroker(t)

	// Publish one job per worker; between them they must take all of it,
	// which only happens if every consumer re-registered.
	publisher := isolatedClient(t)

	for i := range workers {
		var sent bool
		for range 30 {
			if err := publisher.Publish(context.Background(), queue.LaneStandard,
				queue.Job{SubmissionID: fmt.Sprintf("multi-%d", i)}); err == nil {
				sent = true
				break
			}
			time.Sleep(time.Second)
		}
		require.True(t, sent, "could not publish job %d after the restart", i)
	}

	deadline := time.After(60 * time.Second)
	for range workers {
		select {
		case <-received:
		case <-deadline:
			t.Fatalf("only %d of %d jobs were consumed after the restart", atomic.LoadInt64(&handled), workers)
		}
	}
}

// =============================================================
// Test D — message safety around a failure
// =============================================================

// TestRecovery_UnacknowledgedWorkIsRedelivered pins the delivery
// contract: at-least-once. A handler that never acknowledges — because
// its worker died mid-judge — must see the job again.
func TestRecovery_UnacknowledgedWorkIsRedelivered(t *testing.T) {
	requireBroker(t)

	publisher := isolatedClient(t)
	require.NoError(t, publisher.Publish(context.Background(), queue.LaneStandard,
		queue.Job{SubmissionID: "redelivered"}))

	// First consumer takes the job and dies without acknowledging, which
	// is what a killed worker looks like to the broker.
	first := isolatedClient(t)
	got := make(chan struct{}, 1)
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	go func() {
		_ = first.Consume(firstCtx, queue.LaneStandard, 1, func(ctx context.Context, job queue.Job) error {
			got <- struct{}{}
			<-ctx.Done() // hold it, never acknowledge
			return ctx.Err()
		})
	}()

	select {
	case <-got:
	case <-time.After(20 * time.Second):
		t.Fatal("first consumer never received the job")
	}
	cancelFirst()
	require.NoError(t, first.Close())

	// A second consumer must be given the same job.
	second := isolatedClient(t)
	redelivered := make(chan string, 1)
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	go func() {
		_ = second.Consume(secondCtx, queue.LaneStandard, 1, func(_ context.Context, job queue.Job) error {
			redelivered <- job.SubmissionID
			return nil
		})
	}()

	select {
	case id := <-redelivered:
		assert.Equal(t, "redelivered", id, "an unacknowledged job must not be lost")
	case <-time.After(30 * time.Second):
		t.Fatal("the job was lost instead of being redelivered")
	}
}

// TestRecovery_FailedJobsAreNotRequeuedForever guards the other side of
// at-least-once: a job that always fails must not loop forever.
//
// The policy is retry-once, not drop-on-first-failure. A judging failure
// used to be discarded outright, which left a submission whose record
// could not even be read stuck pending forever; decideAck in
// internal/queue/rabbitmq now hands a first failure back and discards it
// on the redelivery the broker marks. So the bound is exactly two
// attempts — one more than nothing, and finite.
//
// This assertion said 1 and had never run in CI to notice, which is the
// whole reason F4 matters: it was left behind by the ack-policy change
// and only a real broker could have caught it.
func TestRecovery_FailedJobsAreNotRequeuedForever(t *testing.T) {
	requireBroker(t)

	publisher := isolatedClient(t)
	require.NoError(t, publisher.Publish(context.Background(), queue.LaneStandard,
		queue.Job{SubmissionID: "always-fails"}))

	client := isolatedClient(t)

	var attempts int64
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	_ = client.Consume(ctx, queue.LaneStandard, 1, func(context.Context, queue.Job) error {
		atomic.AddInt64(&attempts, 1)
		return fmt.Errorf("judging failed")
	})

	assert.Equal(t, int64(2), atomic.LoadInt64(&attempts),
		"a failing job is retried exactly once and then dropped, not looped")
}

// =============================================================
// Test E — graceful shutdown
// =============================================================

// TestRecovery_ShutdownWaitsForInFlightWork checks that cancelling the
// context lets a running job finish rather than abandoning it.
func TestRecovery_ShutdownWaitsForInFlightWork(t *testing.T) {
	requireBroker(t)

	publisher := isolatedClient(t)
	require.NoError(t, publisher.Publish(context.Background(), queue.LaneStandard,
		queue.Job{SubmissionID: "in-flight"}))

	client := isolatedClient(t)

	started := make(chan struct{})
	var finished atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = client.Consume(ctx, queue.LaneStandard, 1, func(context.Context, queue.Job) error {
			close(started)
			// Judging keeps going after the shutdown signal arrives.
			time.Sleep(2 * time.Second)
			finished.Store(true)
			return nil
		})
	}()

	select {
	case <-started:
	case <-time.After(20 * time.Second):
		t.Fatal("job never started")
	}

	cancel()  // SIGTERM equivalent
	wg.Wait() // Consume must not return until the handler is done

	assert.True(t, finished.Load(), "shutdown abandoned a job that was mid-flight")
}
