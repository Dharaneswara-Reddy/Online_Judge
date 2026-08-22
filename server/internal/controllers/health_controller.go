package controllers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Pinger is anything whose reachability can be checked cheaply.
//
// Both the Mongo client and the queue client already satisfy it, which is
// the point: readiness reuses the checks those packages own rather than
// inventing a second opinion about what "up" means.
type Pinger interface {
	Ping(ctx context.Context) error
}

// probeTimeout bounds a single dependency check. A readiness probe that
// can hang is worse than one that reports a failure: the orchestrator
// stops getting an answer at all.
const probeTimeout = 2 * time.Second

// readinessTTL is how long one dependency probe is reused.
//
// Readiness is unauthenticated and polled forever, so serving it live
// would let anyone turn a scheduled health check into on-demand database
// work — the same shape of bug as the public stats summary, which ran
// four collection counts per anonymous request. Two seconds is short
// enough that a real outage is noticed almost immediately and long enough
// that a burst of probes costs one ping.
const readinessTTL = 2 * time.Second

// HealthController answers liveness and readiness.
//
// The two are deliberately separate endpoints with separate meanings.
// Liveness asks whether this process should be restarted; readiness asks
// whether it should receive traffic. Conflating them is actively harmful:
// if a Mongo outage failed liveness, the orchestrator would kill and
// restart every API container, which does nothing for Mongo and removes
// the process that could have served the cached and static routes.
type HealthController struct {
	db     Pinger
	broker Pinger

	// ready caches the last dependency probe. A nil value means "not yet
	// probed", which the cache handles by loading.
	ready *ttlCache[readiness]
}

// readiness is one probe result.
type readiness struct {
	ok     bool
	checks map[string]string
}

// NewHealthController wires the probes. broker may be nil: the queue is
// documented as optional and the API judges inline without it.
func NewHealthController(db, broker Pinger) *HealthController {
	return &HealthController{db: db, broker: broker, ready: newTTLCache[readiness](readinessTTL)}
}

// Live reports that the process is running and serving HTTP.
//
// It touches nothing. No database, no broker, no Redis, no state change,
// no rate limiter, no auth. That is not laziness — it is the definition:
// a process that can answer this is alive, whatever its dependencies are
// doing, and a process that cannot is one a restart might genuinely fix.
func (hc *HealthController) Live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "alive",
		"data":    gin.H{"status": "ok"},
	})
}

// Ready reports whether this instance can usefully serve requests.
//
// Mongo is required — without it almost every route fails — so an
// unreachable database means not ready. RabbitMQ is not required, because
// the API falls back to judging inline, so it is reported as degraded
// rather than fatal. Both use a ping, never a query: a readiness check
// that counted documents would be a scan on a timer.
func (hc *HealthController) Ready(c *gin.Context) {
	result, _ := hc.ready.Get(c.Request.Context(), func(ctx context.Context) (readiness, error) {
		return hc.probe(ctx), nil
	})

	status := http.StatusOK
	message := "ready"
	if !result.ok {
		status = http.StatusServiceUnavailable
		message = "not ready"
	}

	c.JSON(status, gin.H{
		"success": result.ok,
		"message": message,
		"data":    gin.H{"status": message, "checks": result.checks},
	})
}

// probe checks each dependency once, on its own bounded deadline.
func (hc *HealthController) probe(ctx context.Context) readiness {
	checks := map[string]string{}

	// The database is the one hard requirement.
	dbOK := hc.check(ctx, hc.db)
	checks["database"] = upDown(dbOK)

	// The queue is optional by design; record it for an operator without
	// letting it remove the instance from rotation.
	switch {
	case hc.broker == nil:
		checks["queue"] = "not configured"
	case hc.check(ctx, hc.broker):
		checks["queue"] = "up"
	default:
		checks["queue"] = "down"
	}

	return readiness{ok: dbOK, checks: checks}
}

// check runs one ping under probeTimeout. A nil dependency counts as
// healthy: it was never configured, so it cannot be broken.
func (hc *HealthController) check(ctx context.Context, p Pinger) bool {
	if p == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	return p.Ping(ctx) == nil
}

func upDown(ok bool) string {
	if ok {
		return "up"
	}
	return "down"
}
