package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClientOptions_AreBoundedForASmallInstance pins the resource limits
// the driver runs with. The defaults (200 connections, a 30 second server
// selection timeout, no operation timeout) are sized for a large fleet;
// this deployment is a single small instance, where those defaults turn
// an unreachable database into hung requests and exhausted memory.
func TestClientOptions_AreBoundedForASmallInstance(t *testing.T) {
	opts := clientOptions("mongodb://localhost:27017")

	require.NotNil(t, opts.MaxPoolSize, "the connection pool must be capped")
	assert.LessOrEqual(t, *opts.MaxPoolSize, uint64(32),
		"a 2-vCPU instance cannot usefully drive more connections than this")

	require.NotNil(t, opts.ServerSelectionTimeout, "server selection must be bounded")
	assert.LessOrEqual(t, *opts.ServerSelectionTimeout, 10*time.Second,
		"a request must fail fast when the database is unreachable")

	require.NotNil(t, opts.Timeout, "operations must carry a default deadline")
	assert.LessOrEqual(t, *opts.Timeout, 30*time.Second)

	require.NotNil(t, opts.ConnectTimeout)
	require.NotNil(t, opts.MaxConnIdleTime, "idle connections must be reaped")
}

// TestConnect_FailsFastWhenTheServerIsUnreachable is the behaviour the
// bounded server-selection timeout buys: an unreachable database is an
// error in seconds, not a request that hangs for the driver's default 30.
func TestConnect_FailsFastWhenTheServerIsUnreachable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing-sensitive connection test in short mode")
	}

	// Port 1 is reserved and closed, so every connection attempt is
	// refused immediately and only the retry loop consumes time.
	start := time.Now()
	_, err := Connect("mongodb://127.0.0.1:1/?directConnection=true")
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Less(t, elapsed, 8*time.Second,
		"connecting must give up on the bounded server-selection timeout")
}
