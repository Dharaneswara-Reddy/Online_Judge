// Package main is the entry point for the Online Judge API server.
// It loads configuration, connects to MongoDB Atlas, sets up
// indexes, mounts all routes, and starts the HTTP server.
//
// This file is intentionally thin — all logic lives in the
// internal packages (config, database, routes, controllers, models).
package main

import (
	"log"

	"github.com/toji339/online-judge/internal/config"
	"github.com/toji339/online-judge/internal/database"
	"github.com/toji339/online-judge/internal/queue/rabbitmq"
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

	// 6. Set up the Gin router with all routes and middleware
	router := routes.Setup(db, cfg, deps)

	// 7. Start the HTTP server on the configured port
	log.Printf("Server starting on port %s", cfg.Port)
	if err := router.Run(":" + cfg.Port); err != nil {
		log.Fatalf("FATAL: Server failed to start: %v", err)
	}
}
