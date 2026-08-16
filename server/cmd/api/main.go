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
	"syscall"
	"time"

	"github.com/toji339/online-judge/internal/config"
	"github.com/toji339/online-judge/internal/database"
	"github.com/toji339/online-judge/internal/queue/rabbitmq"
	"github.com/toji339/online-judge/internal/ratelimit"
	"github.com/toji339/online-judge/internal/realtime"
	"github.com/toji339/online-judge/internal/routes"
)

func main() {
	// Steps to follow while starting the server
	// ============================================

	// 1. Load environment variables from .env
	cfg := config.Load()
	log.Println("Configuration loaded successfully")

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

	// 5. Connect to the submission queue. This is optional: without a
	//    broker the API judges submissions inline instead of refusing
	//    them, which keeps local development working with no extra setup.
	var deps routes.Deps
	broker, err := rabbitmq.Connect(cfg.RabbitMQURL)
	if err != nil {
		log.Printf("WARNING: submission queue unavailable (%v) — judging inline. "+
			"Start it with: docker compose up -d", err)
	} else {
		defer broker.Close()
		deps.Publisher = broker
		log.Println("Connected to the submission queue")
	}

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
