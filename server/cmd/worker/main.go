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
	"time"

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

	// 2. Connect to the broker — without it a worker has nothing to do
	broker, err := rabbitmq.Connect(cfg.RabbitMQURL)
	if err != nil {
		log.Fatalf("FATAL: Could not connect to RabbitMQ at %s: %v\n"+
			"Start it with: docker compose up -d", cfg.RabbitMQURL, err)
	}
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
			consumeLane(ctx, broker, lane, cfg.WorkerCount, processor)
		}(lane)
	}

	log.Printf("Judge worker started with %d concurrent slots per lane", cfg.WorkerCount)
	wg.Wait()
	log.Println("Judge worker stopped")
}

// consumeLane runs one lane's consumer, reconnecting after a broker
// hiccup so a worker does not silently stop judging.
func consumeLane(ctx context.Context, broker *rabbitmq.Client, lane queue.Lane, prefetch int, processor *worker.Processor) {
	for {
		err := broker.Consume(ctx, lane, prefetch, processor.Process)
		if ctx.Err() != nil {
			return
		}
		log.Printf("WARNING: lane %q consumer stopped (%v), retrying in 5s", lane, err)

		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}
