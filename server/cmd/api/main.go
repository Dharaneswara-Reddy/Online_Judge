// Package main is the entry point for the Online Judge API server.
// It loads configuration, connects to MongoDB Atlas, sets up
// indexes, mounts all routes, and starts the HTTP server.
//
// This file is intentionally thin — all logic lives in the
// internal packages (config, database, routes, controllers, models).
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/toji339/online-judge/internal/config"
	"github.com/toji339/online-judge/internal/database"
	"github.com/toji339/online-judge/internal/queue/rabbitmq"
	"github.com/toji339/online-judge/internal/ratelimit"
	"github.com/toji339/online-judge/internal/realtime"
	"github.com/toji339/online-judge/internal/routes"
	"github.com/toji339/online-judge/internal/session"
)

// setGinMode puts Gin in release mode unless this is explicitly a
// development environment.
//
// Gin defaults to debug mode, which dumps the whole route table at
// startup, logs a warning on every request, and is measurably slower.
// The route table is a map of the API's attack surface, so it should not
// be sitting in production logs.
//
// Release is the default on purpose: a deployment that forgets to set
// anything gets the safe mode, and only someone who asks for
// development gets the noisy one. GIN_MODE still wins if it is set,
// since that is the knob Gin's own documentation points people at.
func setGinMode(appEnv, ginMode string) string {
	// Only the three modes Gin knows are passed through. An unrecognised
	// value is reported and ignored here — though note Gin's own package
	// init already reads GIN_MODE and panics on a bad one before this
	// function ever runs, so the warning below is a backstop for a value
	// arriving some other way, not a guarantee.
	switch ginMode {
	case gin.DebugMode, gin.ReleaseMode, gin.TestMode:
		gin.SetMode(ginMode)
		return gin.Mode()
	case "":
		// Nothing pinned; fall through to the environment check.
	default:
		log.Printf("WARNING: GIN_MODE=%q is not a Gin mode, ignoring it", ginMode)
	}

	if strings.EqualFold(appEnv, "development") || strings.EqualFold(appEnv, "dev") {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}
	return gin.Mode()
}

// brokerProbeTimeout bounds the startup log probe. It is informational
// only — nothing waits on it — so it stays short.
const brokerProbeTimeout = 15 * time.Second

func main() {
	// Steps to follow while starting the server
	// ============================================

	// 1. Load environment variables from .env
	cfg := config.Load()
	log.Println("Configuration loaded successfully")

	//    Decide Gin's mode now, before anything creates an engine. This
	//    has to come after the .env file is loaded so APP_ENV can be set
	//    there like every other setting.
	log.Printf("Gin running in %s mode", setGinMode(os.Getenv("APP_ENV"), os.Getenv("GIN_MODE")))

	// 2. Connect to MongoDB Atlas
	client, err := database.Connect(cfg.MongoURI)
	if err != nil {
		log.Fatalf("FATAL: Could not connect to MongoDB: %v", err)
	}
	defer database.Disconnect(client)

	// 3. Get a reference to the application database
	db := client.Database(cfg.DBName)

	// 4. Create required indexes (unique constraints on users collection)
	if err := database.EnsureIndexes(db); err != nil {
		log.Fatalf("FATAL: Could not create database indexes: %v", err)
	}

	// 5. Attach to the submission queue.
	//
	//    The client is deliberately lazy. It used to be built with
	//    rabbitmq.Connect, which dials once: if the broker was not up at
	//    that moment the publisher stayed nil for the entire life of the
	//    process, and no later recovery was possible. That is not a rare
	//    corner — on an EC2 reboot both containers start together and the
	//    broker takes about a minute to become ready, so the API would come
	//    up healthy and 503 every submission until someone restarted it by
	//    hand. depends_on does not help, because it orders container start,
	//    not process readiness across a host reboot.
	//
	//    rabbitmq.New never dials here. The first publish opens the
	//    connection, and Publish re-dials and retries when it finds a dead
	//    one, so the API recovers from a broker restart on its own. A
	//    publish-side cooldown keeps a genuinely absent broker from costing
	//    every request a dial timeout.
	//
	//    The queue stays optional: when publishing fails and this process
	//    can reach Docker, the submission is judged inline instead of being
	//    refused, which keeps local development working with no extra setup.
	// Session revocation state. Mongo rather than Redis: Redis is
	// optional in this deployment and a revocation store that vanishes
	// with an optional dependency is not a revocation store. The records
	// carry a TTL index so they expire with the tokens they revoke, which
	// is what keeps this from becoming an unbounded denylist.
	sessions := session.NewMongoStore(db)
	if err := sessions.EnsureIndexes(context.Background()); err != nil {
		// Non-fatal for the same reason the other index builds are: a
		// missing TTL index means revocations accumulate, not that
		// authentication breaks.
		log.Printf("WARNING: could not create the session revocation TTL index: %v", err)
	}

	var deps routes.Deps
	deps.Sessions = sessions
	broker := rabbitmq.New(cfg.RabbitMQURL)
	defer broker.Close()
	deps.Publisher = broker
	// The same client also carries synchronous playground runs, used only
	// when this process cannot reach Docker itself.
	deps.Caller = broker
	// Readiness asks the same client whether the broker is reachable. It
	// probes; it never publishes.
	deps.BrokerProbe = broker

	//    Probe once in the background purely so the log says which mode the
	//    process is in. It must not gate the publisher: gating on a startup
	//    probe is the bug described above.
	go func() {
		probeCtx, cancel := context.WithTimeout(context.Background(), brokerProbeTimeout)
		defer cancel()
		if err := broker.Ping(probeCtx); err != nil {
			log.Printf("WARNING: submission queue not reachable at startup — the API will "+
				"keep trying, and judges inline meanwhile if Docker is available (%v)", err)
			return
		}
		log.Println("Connected to the submission queue")
	}()

	// 6. Connect to Redis. Also optional: without it, War Room events only
	//    reach clients attached to this instance and rate limits are not
	//    enforced, but every other feature behaves normally.
	bus, err := realtime.ConnectRedis(cfg.RedisURL)
	if err != nil {
		log.Printf("WARNING: Redis unavailable (%v) — War Room sync is limited to this "+
			"instance and rate limiting is disabled", err)
	} else {
		defer bus.Close()
		deps.Bus = bus
		deps.Limiter = ratelimit.NewRedisLimiter(bus.Client())
		log.Println("Connected to Redis")
	}

	// 7. Set up the Gin router with all routes and middleware
	router := routes.Setup(db, cfg, deps)

	// 8. Start the HTTP server with explicit timeouts.
	//
	//    The zero-value server has none, which lets a client hold a
	//    connection open indefinitely by trickling headers and exhaust the
	//    connection pool. WriteTimeout has to exceed the judge's own
	//    budget; hijacked WebSocket connections are exempt from it.
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("Server starting on port %s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("FATAL: Server failed to start: %v", err)
		}
	}()

	// 9. Shut down gracefully so in-flight requests finish and the
	//    deferred cleanup above actually runs — router.Run would exit the
	//    process before any of it.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	log.Println("Shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("WARNING: graceful shutdown failed: %v", err)
	}
	log.Println("Server stopped")
}
