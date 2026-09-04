# Architecture

How a submission travels through CodeArena, and why each piece is where it is.
Read `CONSTITUTION.md` first for the rules; this document explains the shape.

---

## The processes

The platform runs as two Go programs plus three external services.

| Process | Command | Responsibility |
|---|---|---|
| **API** | `go run ./cmd/api` | HTTP + WebSocket. Never judges anything when a queue is available. |
| **Judge worker** | `go run ./cmd/worker` | Consumes queued submissions and runs them in the sandbox. |
| MongoDB Atlas | — | Every durable record. |
| RabbitMQ | `docker compose up -d` | Decouples accepting a submission from judging it. |
| Redis | `docker compose up -d` | War Room event fan-out and rate-limit counters. |

Judging capacity therefore scales independently of request-handling capacity:
run more workers without touching the API tier.

An optional third outbound dependency exists: the AI assistant calls the
Anthropic Messages API. It is not a process and runs nothing on the judge host
— see `assist.md` for why inference sits outside the judging boundary and what
the hint ladder refuses to do.

**Both RabbitMQ and Redis are optional.** With no broker the API judges inline
in the request; with no Redis, War Room events reach only clients attached to
the same API instance and rate limits are not enforced. Neither absence stops
the server from starting — this is deliberate, so a fresh clone works with
nothing but Go, Docker and a Mongo URI.

---

## The path of a submission

```
POST /api/problems/:slug/submit
        │
        ▼
  submission.Service.Create        ── validates, enforces admission control,
        │                             stores the record as "pending"
        ▼
  queue.Publisher.Publish          ── standard lane, or the War Room lane
        │                             when the submission is tagged with a room
        ▼   (API responds 202 here; the client polls GET /api/submissions/:id)
   RabbitMQ
        │
        ▼
  judge worker  ──▶ worker.Processor.Process
                        │
                        ├── skip if the submission already has a verdict
                        ├── load problem + ALL test cases (hidden included)
                        ├── mark "running"
                        ├── judge.Judge.Evaluate  ──▶ Docker sandbox
                        ├── mark judged, stamping judged_at server-side
                        └── notify listeners (the War Room watches this)
```

### Why a queue at all

Judging is slow and bursty. Running it inline ties up a request thread for the
whole evaluation, so a burst of submissions consumes API capacity directly. The
queue turns that burst into a steady, worker-controlled stream.

### Why two lanes

A War Room is a live race — a player waiting behind a backlog of background
practice submissions would experience the platform as broken. War Room
submissions go to `judge.warroom`, consumed by its own worker pool, so a race is
never throttled by practice traffic.

### Where fairness comes from

The design document calls for round-robin per user rather than strict FIFO.
That is achieved **upstream of the broker**, not inside it: the submission
service admits at most one in-flight submission per user, so nobody can hold
more than one slot in the queue and plain FIFO delivery is already fair between
users. It also doubles as backpressure against accidental spam.

### Idempotency

A message can be redelivered — a worker crash, a network blip. `Process` skips
any submission that already carries a terminal verdict, so redelivery can never
overwrite a result or double-count a solve.

---

## Sandboxing

Every submission is compiled and run inside a container built from
`docker/judge-sandbox`, with:

- **memory and swap capped** — the process is OOM-killed on breach
- **`NetworkMode: none`** — no exfiltration and no payload download
- **a PID limit** — fork bombs cannot exhaust the host
- **a wall-clock timeout enforced from Go**, independent of in-container limits
- **a non-root user** with a nologin shell

Keep every one of those when touching `internal/judge/docker_sandbox.go`.

V1 creates a container per submission. The design document's pre-warmed pool,
and the eventual move from Docker to `isolate`, are V2 work: they are throughput
optimisations, not correctness or safety ones.

---

## War Room

A room moves `waiting → in_progress → finished`, or `expired` when abandoned.

Two operations are **conditional writes** rather than read-modify-write pairs,
because each settles a race between concurrent requests and only the database
can decide them:

- **`AddParticipant`** carries its preconditions in the update filter (still
  waiting, user not present, below capacity). Two players reaching for the last
  seat cannot both read "one seat free" and both write.
- **`DeclareWinner`** only matches a room with no winner yet, making it a
  compare-and-set. Exactly one finisher ever announces a result.

Both rules are covered by concurrency tests in `internal/warroom/service_test.go`.

### Who decides the winner

The judge worker. It is the only place that knows when a verdict became final,
and it stamps `judged_at` itself. A client's clock is never consulted, and
nothing in a request body can influence an outcome.

### How the news travels

Participants in one room may be connected to different API instances, so an
event published on one has to reach all of them. Redis Pub/Sub, keyed by room,
provides that fan-out: the worker publishes, and every API instance holding a
socket for that room receives it and writes it to its own clients.

`realtime.Bus` is an interface with a Redis implementation and an in-memory one.
The in-memory bus is what unit tests use, and what the API falls back to when
Redis is unreachable.

---

## Guardrails worth knowing

- **Hidden test cases never leak.** Public handlers call `ListPublicTestCases`;
  `ListAllTestCases` is reserved for admin routes and the judging pipeline.
- **Verdicts are write-only from the judge.** No user-facing path sets a status.
- **Source code is owner-only.** `GET /api/submissions/:id` refuses to serve a
  submission to anyone but its author (or an admin), and War Room broadcasts
  carry a status and language but never code.
- **Votes and tags are idempotent by construction.** Upvotes use
  `$addToSet`/`$pull` over a voter set; company tags rely on a unique index on
  `(problem_id, user_id, company)` after the company name is normalised.
- **Rate limits fail open.** A spam control must never take the platform down
  with it, so a Redis outage disables limiting rather than rejecting writes.
- **No globals.** Every dependency is injected through `routes.Setup`.
