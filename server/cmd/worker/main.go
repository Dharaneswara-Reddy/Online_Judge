// Package main is the entry point for the Online Judge judge worker.
//
// The worker consumes queued submissions, judges them inside the Docker
// sandbox, and writes the verdict back. It is deliberately a separate
// process from the API so judging capacity can be scaled independently
// of request-handling capacity.
//
// Run as many worker processes as you need:
//
//	go run ./cmd/worker
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/toji339/online-judge/internal/config"
	"github.com/toji339/online-judge/internal/database"
	"github.com/toji339/online-judge/internal/judge"
	"github.com/toji339/online-judge/internal/problem"
	problemmongo "github.com/toji339/online-judge/internal/problem/mongorepo"
	"github.com/toji339/online-judge/internal/queue"
	"github.com/toji339/online-judge/internal/queue/rabbitmq"
	"github.com/toji339/online-judge/internal/realtime"
	"github.com/toji339/online-judge/internal/submission"
	submissionmongo "github.com/toji339/online-judge/internal/submission/mongorepo"
	"github.com/toji339/online-judge/internal/warroom"
	warroommongo "github.com/toji339/online-judge/internal/warroom/mongorepo"
	"github.com/toji339/online-judge/internal/worker"
)

func main() {
	// Steps to follow while starting a judge worker
	// ===============================================

	// 1. Load configuration and connect to MongoDB
	cfg := config.Load()

	client, err := database.Connect(cfg.MongoURI)
	if err != nil {
		log.Fatalf("FATAL: Could not connect to MongoDB: %v", err)
	}
	defer database.Disconnect(client)
	db := client.Database(cfg.DBName)

	// 2. Create the broker client. It connects in the background and
	//    recovers on its own, so a broker that is down at startup — or
	//    that restarts later — is something to wait through rather than
	//    a reason to exit. The worker has nothing to do until it is up.
	broker := rabbitmq.New(cfg.RabbitMQURL)
	defer broker.Close()

	// 3. Create the Docker sandbox that actually runs user code
	sandbox, err := judge.NewDockerSandbox()
	if err != nil {
		log.Fatalf("FATAL: Docker sandbox unavailable: %v\n"+
			"Build the image with: docker build -t codearena-sandbox:latest docker/judge-sandbox", err)
	}

	// 4. Wire the judging pipeline
	problemSvc := problem.NewService(problemmongo.New(db))
	submissionSvc := submission.NewService(submissionmongo.New(db))

	// Redis pub/sub lets a War Room verdict reach whichever API instance
	// holds the racing participants' WebSockets. A worker without Redis
	// still judges correctly; only live race updates are lost.
	var notifier worker.Notifier = worker.NopNotifier{}
	bus, err := realtime.ConnectRedis(cfg.RedisURL)
	if err != nil {
		log.Printf("WARNING: Redis unavailable (%v) — War Room results will not be broadcast", err)
	} else {
		defer bus.Close()
		warRoomSvc := warroom.NewService(warroommongo.New(db), problemSvc)
		notifier = warroom.NewJudgeNotifier(warRoomSvc, bus)
		log.Println("Connected to Redis for War Room broadcasts")
	}

	processor := worker.NewProcessor(submissionSvc, problemSvc, sandbox, notifier)

	// 5. Consume both lanes until interrupted
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	for _, lane := range []queue.Lane{queue.LaneStandard, queue.LaneWarRoom} {
		wg.Add(1)
		go func(lane queue.Lane) {
			defer wg.Done()
			// Consume owns its own recovery: it blocks until the broker
			// exists, rebuilds the channel, topology, QoS and consumer
			// after any failure, and returns only on shutdown.
			if err := broker.Consume(ctx, lane, cfg.WorkerCount, processor.Process); err != nil && ctx.Err() == nil {
				log.Printf("rabbitmq: lane %q gave up: %v", lane, err)
			}
		}(lane)
	}

	log.Printf("Judge worker started with %d concurrent slots per lane", cfg.WorkerCount)

	// Wait for a signal, then let each lane drain. Consume stops taking
	// new deliveries as soon as the context is cancelled and waits for
	// the jobs already running, so a shutdown never abandons a judged
	// submission without acknowledging it.
	<-ctx.Done()
	log.Println("Judge worker shutting down, waiting for in-flight submissions...")
	wg.Wait()
	log.Println("Judge worker stopped")
}
