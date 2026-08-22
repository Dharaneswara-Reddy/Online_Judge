package controllers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/controllers"
)

// stubPinger stands in for a dependency the readiness probe checks.
type stubPinger struct {
	err   error
	calls atomic.Int64
}

func (p *stubPinger) Ping(context.Context) error {
	p.calls.Add(1)
	return p.err
}

func healthRouter(t *testing.T, db, broker *stubPinger) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	hc := controllers.NewHealthController(db, broker)
	r.GET("/healthz", hc.Live)
	r.GET("/readyz", hc.Ready)
	return r
}

func get(t *testing.T, r *gin.Engine, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

// Liveness answers one question: is this process serving HTTP. It must not
// consult a dependency, because failing liveness gets the container killed
// and killing the API does not repair a database.
func TestLive_IsUpEvenWhenEveryDependencyIsDown(t *testing.T) {
	db := &stubPinger{err: errors.New("mongo unreachable")}
	broker := &stubPinger{err: errors.New("broker unreachable")}
	r := healthRouter(t, db, broker)

	w := get(t, r, "/healthz")

	assert.Equal(t, http.StatusOK, w.Code,
		"a live process with a sick database is still live")
	assert.Zero(t, db.calls.Load(), "liveness must not touch the database")
	assert.Zero(t, broker.calls.Load(), "liveness must not touch the broker")
}

// Readiness is the endpoint that is allowed to care about dependencies.
func TestReady_IsOKWhenTheDatabaseAnswers(t *testing.T) {
	db := &stubPinger{}
	broker := &stubPinger{}
	r := healthRouter(t, db, broker)

	w := get(t, r, "/readyz")

	require.Equal(t, http.StatusOK, w.Code)
	assert.Positive(t, db.calls.Load(), "readiness must actually probe the database")
}

func TestReady_FailsWhenTheDatabaseIsUnreachable(t *testing.T) {
	db := &stubPinger{err: errors.New("mongo unreachable")}
	r := healthRouter(t, db, &stubPinger{})

	w := get(t, r, "/readyz")

	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"the API cannot serve without Mongo, so it is not ready")
}

// RabbitMQ is documented as optional — the API degrades to inline judging
// without it — so its absence is reported, not treated as not-ready.
func TestReady_TolerpatesAnAbsentBroker(t *testing.T) {
	db := &stubPinger{}
	broker := &stubPinger{err: errors.New("broker unreachable")}
	r := healthRouter(t, db, broker)

	w := get(t, r, "/readyz")

	assert.Equal(t, http.StatusOK, w.Code,
		"an optional dependency being down must not take the API out of rotation")

	var body struct {
		Data struct {
			Checks map[string]string `json:"checks"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "down", body.Data.Checks["queue"],
		"the degraded dependency must still be visible to an operator")
}

// The readiness probe is unauthenticated and runs on a schedule forever, so
// it must not become a way to make the database do work on demand. This is
// the /api/stats/summary lesson.
func TestReady_DoesNotProbeOnEveryRequest(t *testing.T) {
	db := &stubPinger{}
	r := healthRouter(t, db, &stubPinger{})

	for i := 0; i < 25; i++ {
		require.Equal(t, http.StatusOK, get(t, r, "/readyz").Code)
	}

	assert.Less(t, db.calls.Load(), int64(25),
		"a cached probe is what stops readiness being an unauthenticated database amplifier")
}
