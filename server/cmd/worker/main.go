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
	"github.com/toji339/online-judge/internal/playground"
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

	// Ensure the indexes here too, not only in the API. A worker-only
	// environment — a judge box brought up before any API instance, or an
	// API whose own index build failed and carried on — would otherwise
	// run with no partial unique index on submissions, which is what
	// enforces admission control. The build is idempotent and returns
	// immediately when the indexes already exist, and a failure is logged
	// rather than fatal for the same reason it is in the API.
	if err := database.EnsureIndexes(db); err != nil {
		log.Printf("WARNING: could not ensure database indexes: %v", err)
	}

	// 2. Create the broker client. It connects in the background and
	//    recovers on its own, so a broker that is down at startup — or
	//    that restarts later — is something to wait through rather than
	//    a reason to exit. The worker has nothing to do until it is up.
	broker := rabbitmq.New(cfg.RabbitMQURL)
	defer broker.Close()

	// 3. Create the Docker sandbox that actually runs user code
	docker, err := judge.NewDockerSandbox()
	if err != nil {
		log.Fatalf("FATAL: Docker sandbox unavailable: %v\n"+
			"Build the image with: docker build -t codearena-sandbox:latest docker/judge-sandbox", err)
	}

	// Reclaim whatever a previous worker left behind before taking any
	// new work. A judge container runs `sleep infinity`, so removing it
	// is the only thing that ever stops it: a worker killed mid-judge
	// leaks a container that holds its CPU and memory reservation for as
	// long as the host lives, and they accumulate across every crash.
	//
	// A failure here is worth reporting but not worth refusing to start
	// over — a worker that judges nothing is worse than one running
	// alongside a leaked container.
	if removed, err := docker.ReconcileOrphans(context.Background()); err != nil {
		log.Printf("WARNING: could not reclaim orphaned sandbox containers: %v", err)
	} else if removed > 0 {
		log.Printf("Reclaimed %d orphaned sandbox container(s) left by a previous run", removed)
	}

	// Every consumer below shares one ceiling on live containers. The
	// wrapper goes here, around the sandbox itself, rather than around
	// each consumer: three consumers each sized from WorkerCount is
	// exactly how the configured number quietly became three times as
	// many containers, and a fourth consumer added later would have
	// repeated it.
	sandbox := judge.NewLimitedSandbox(docker, cfg.MaxSandboxes)

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

	// Playground runs are synchronous and answered on a separate queue.
	// They are served here because this process owns a sandbox and the
	// API deliberately does not: giving the internet-facing API access to
	// the Docker daemon would make an HTTP-layer bug host root.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := broker.Respond(ctx, cfg.WorkerCount, playground.Handler(playground.NewLocalRunner(sandbox))); err != nil && ctx.Err() == nil {
			log.Printf("rabbitmq: playground responder gave up: %v", err)
		}
	}()

	// Reclaim submissions the pipeline never finished. A worker killed
	// between accepting a job and writing a status leaves the row
	// non-terminal, and the partial unique index behind admission control
	// then bars that user from submitting anything at all until someone
	// edits the database. The sweep is a conditional write, so running it
	// in every worker process at once is safe.
	wg.Add(1)
	go func() {
		defer wg.Done()
		worker.NewReaper(submissionSvc, worker.DefaultSweepInterval).Run(ctx)
	}()

	log.Printf("Judge worker started: prefetch %d per lane, at most %d judge container(s) on this host at once",
		cfg.WorkerCount, sandbox.Capacity())

	// Wait for a signal, then let each lane drain. Consume stops taking
	// new deliveries as soon as the context is cancelled and waits for
	// the jobs already running, so a shutdown never abandons a judged
	// submission without acknowledging it.
	<-ctx.Done()
	log.Println("Judge worker shutting down, waiting for in-flight submissions...")
	wg.Wait()
	log.Println("Judge worker stopped")
}
