<div align="center">

  # ⚡ CodeArena — Online Judge Platform

  <p align="center">
    <strong>A high-performance, real-time competitive programming platform built on the GERM Stack.</strong>
  </p>

  <p align="center">
    <a href="#about-the-project">About</a> •
    <a href="#tech-stack">Tech Stack</a> •
    <a href="#architecture--design-principles">Architecture</a> •
    <a href="#getting-started">Getting Started</a> •
    <a href="#api-reference">API Reference</a> •
    <a href="#project-structure">Structure</a> •
    <a href="#documentation">Docs</a>
  </p>

  <p align="center">
    <img src="https://img.shields.io/badge/Go-1.25-00ADD8?style=for-the-badge&logo=go&logoColor=white" alt="Go" />
    <img src="https://img.shields.io/badge/Gin-v1.12-008080?style=for-the-badge&logo=gin&logoColor=white" alt="Gin" />
    <img src="https://img.shields.io/badge/React-v19-61DAFB?style=for-the-badge&logo=react&logoColor=black" alt="React" />
    <img src="https://img.shields.io/badge/Vite-v6-646CFF?style=for-the-badge&logo=vite&logoColor=white" alt="Vite" />
    <img src="https://img.shields.io/badge/MongoDB-Atlas-47A248?style=for-the-badge&logo=mongodb&logoColor=white" alt="MongoDB" />
    <img src="https://img.shields.io/badge/Architecture-MVC-FF6F61?style=for-the-badge" alt="MVC" />
    <img src="https://img.shields.io/badge/Methodology-TDD-8A2BE2?style=for-the-badge" alt="TDD" />
  </p>
</div>

---

## 📋 Table of Contents

- [About The Project](#about-the-project)
- [Key Features](#key-features)
- [Tech Stack](#tech-stack)
- [Architecture \& Design Principles](#architecture--design-principles)
  - [MVC Architecture](#mvc-architecture)
  - [Security First](#security-first)
  - [Test-Driven Development (TDD)](#test-driven-development-tdd)
- [Project Structure](#project-structure)
- [Getting Started](#getting-started)
  - [Prerequisites](#prerequisites)
  - [1. Backend Setup](#1-backend-setup)
  - [2. Frontend Setup](#2-frontend-setup)
- [API Reference](#api-reference)
- [Testing](#testing)
- [Roadmap](#roadmap)
- [Documentation](#documentation)
- [License](#license)

---

## 🎯 About The Project

**CodeArena** is a full-stack, distributed online judge platform designed for competitive programmers and interview candidates. Similar to platforms like LeetCode and Codeforces, CodeArena enables users to practice algorithmic challenges, submit solutions for automated evaluation against hidden test cases, and track performance.

Going beyond standard online judges, **CodeArena** features:
1. **War Room**: Real-time 1v1 and small-group competitive coding sessions with low-latency WebSocket synchronization.
2. **Per-Problem Discussions**: Threaded discussion boards for collaborative problem-solving.
3. **Company Tags**: User-curated company and interview round tags for targeted interview preparation.

The platform is engineered using the **GERM Stack** (Go, Gin, React, MongoDB) prioritizing high throughput, strict security sandboxing, and ultra-low latency.

---

## ✨ Key Features

### 🔐 Auth & Identity (V1 Complete)
- **Secure Registration**: Multi-field signup with email, username, full name, password, and optional DOB.
- **HTTP-Only JWT Authentication**: Session tokens are issued via server-side, `HttpOnly`, `SameSite=Lax` cookies, preventing XSS token theft.
- **Session Persistence**: Automatic session restoration on app load without storing tokens in `localStorage`.
- **Role-Based Access**: Role validation (`user` / `admin`) protecting sensitive administrative operations.

### 🧩 Core Judge System (V1 Complete)
- **Problem Explorer**: Paginated browsing with multi-attribute filtering (Difficulty, Tags, Company).
- **In-Browser Code Editor**: Interactive code editor supporting multiple programming languages.
- **Async Execution Pipeline**: Decoupled submission processing via RabbitMQ worker queues.
- **Sandboxed Execution**: Resource-capped containers with hard memory/CPU limits, a PID cap, no network namespace, and a wall-clock timeout enforced from Go.
- **Server-Authoritative Verdicts**: Only judge workers write a status; nothing in a request body can influence an outcome.
- **Admission Control**: One in-flight submission per user, which also makes plain FIFO delivery fair between users.

### ⚔️ War Room (V1 Complete)
- **Real-Time Multiplayer**: Instant 1v1/1v1v1 race mode with WebSocket communication.
- **Redis Pub/Sub Event Fanout**: Cross-instance socket state synchronization across horizontally scaled API instances.
- **Dedicated Priority Judging**: High-priority submission queue preventing practice traffic from delaying live races.
- **Server-Authoritative Winner Determination**: Judge-timestamped verdicts preventing client clock manipulation.

### 💬 Community (V1 Complete)
- **Per-Problem Discussions**: One-level-deep threads with idempotent upvoting and soft deletion, so replies survive a removed parent.
- **Company Tags**: "Have you seen this in an interview?" reports, normalised and deduplicated per user, powering a company explorer and the problem-list filter.
- **Rate Limiting**: Redis fixed-window limits on user-generated writes, failing open so a cache outage never takes the platform down.
- **Profile & History**: Solve statistics by difficulty and a filterable, paginated submission history.

---

## 🛠️ Tech Stack

| Layer | Technology | Purpose |
| :--- | :--- | :--- |
| **Frontend** | React 19 + Vite | Fast Single Page Application (SPA) with glassmorphism UI |
| **API Server** | Go 1.25 + Gin | High-performance RESTful API endpoints & WebSocket engine |
| **Database** | MongoDB Atlas (Cloud) | Primary document store for users, problems, submissions, & rooms |
| **Caching / PubSub** | Redis | Ephemeral session state, admission control, and WebSocket fan-out |
| **Message Queue** | RabbitMQ | Decoupled background submission processing and queue management |
| **Sandbox** | Docker / cgroups | Isolated, resource-limited execution environment for untrusted code |

---

## 🏗️ Architecture & Design Principles

### Domain Packages

Newer backend domains (`problem`, `submission`, `warroom`, `discussion`,
`companytag`) follow the same shape:

```text
internal/<domain>/
├── types.go          # Domain types, free of transport and driver concerns
├── service.go        # All business rules — the only thing controllers call
├── repository.go     # Storage interface
├── mongorepo/        # Production MongoDB implementation
└── <domain>test/     # In-memory fake, so service rules are unit-tested
                      # without a database
```

Controllers stay thin: they parse a request, call one service method, and map
domain errors to status codes. The `models/` package is the earlier MVC-style
layer and is kept as-is for users.

### MVC Architecture

CodeArena enforces a strict Model-View-Controller (MVC) separation of concerns:

```
                      +-------------------+
                      |   React Frontend  |
                      |     (View)        |
                      +---------+---------+
                                |  HTTP / WebSockets
                                v
                      +-------------------+
                      |   Gin Controllers |
                      |   (Controller)    |
                      +---------+---------+
                                |
                                v
                      +-------------------+
                      |  Go Data Models   |
                      |     (Model)       |
                      +---------+---------+
                                |
                                v
                      +-------------------+
                      |   MongoDB Atlas   |
                      +-------------------+
```

- **Model** (`server/internal/models/`): Encapsulates data schemas, MongoDB operations, bcrypt password hashing, and business validation. Models never read raw HTTP requests.
- **Controller** (`server/internal/controllers/`): Handles incoming HTTP requests, validates request payloads, delegates to models, and formats JSON responses. Controllers never touch MongoDB directly.
- **View** (`client/src/`): React SPA delivering interactive UI, local state, and API integration.

### Security First

- **HTTP-Only Cookies**: JWTs are strictly transmitted via `HttpOnly`, `SameSite` cookies, rendering them inaccessible to client-side scripts.
- **Bcrypt Password Protection**: Passwords are hashed using bcrypt with cost factor 10.
- **Zero Information Leakage**: Authentication errors return generic responses to eliminate user enumeration vectors.
- **Database Level Uniqueness**: Unique MongoDB indexes on `email` and `username` guarantee data integrity.
- **Restricted CORS**: Cross-Origin Resource Sharing is locked down to the explicit client domain.

### Test-Driven Development (TDD)

All backend features are implemented following strict **TDD (Red-Green-Refactor)**:
1. **Red**: Unit & integration tests are written *before* feature code.
2. **Green**: Minimal implementation code is written to satisfy all test cases.
3. **Refactor**: Code is optimized while maintaining 100% test passing rates.

---

## 📁 Project Structure

```text
Online_Judge/
├── docker-compose.yml              # RabbitMQ + Redis for local development
├── docker/judge-sandbox/           # Image user code is executed inside
│
├── server/                         # Go backend
│   ├── cmd/
│   │   ├── api/main.go             # API entrypoint
│   │   ├── worker/main.go          # Judge worker entrypoint
│   │   └── seed/main.go            # Dataset seeder
│   ├── internal/
│   │   ├── config/                 # Environment configuration
│   │   ├── database/               # Mongo connector + index creation
│   │   ├── models/                 # User model (MVC style)
│   │   ├── problem/                # Problems + test cases
│   │   ├── submission/             # Submission lifecycle and history
│   │   ├── warroom/                # Live racing: rooms, winner, notifier
│   │   ├── discussion/             # Threaded per-problem comments
│   │   ├── companytag/             # Company reports and aggregation
│   │   ├── judge/                  # Sandbox, languages, verdict engine
│   │   ├── worker/                 # Judging pipeline shared by API + worker
│   │   ├── queue/                  # Queue contract + RabbitMQ implementation
│   │   ├── realtime/               # Event bus (Redis Pub/Sub, in-memory)
│   │   ├── ratelimit/              # Redis fixed-window limiter
│   │   ├── controllers/            # HTTP handlers (thin)
│   │   ├── middleware/             # Auth, optional auth, admin, rate limit
│   │   └── routes/routes.go        # All routing and dependency wiring
│   └── tests/                      # Integration tests against a test database
│
├── client/                         # React SPA
│   ├── src/
│   │   ├── api/                    # One module per backend domain
│   │   ├── context/AuthContext.jsx # Global auth state
│   │   ├── components/
│   │   │   ├── layout/NavBar.jsx   # Shared navigation
│   │   │   ├── editor/             # Monaco editor, language picker, output
│   │   │   └── problem/            # Discussion panel, company tag widget
│   │   ├── pages/                  # Home, Problems, ProblemDetail, Profile,
│   │   │                           # WarRoomLobby, WarRoomLive, Companies,
│   │   │                           # Admin, Playground, Login, Signup
│   │   └── index.css               # Design tokens and shared utilities
│   └── index.html
│
├── docs/
│   ├── CONSTITUTION.md             # Coding standards every contributor follows
│   ├── architecture.md             # How a submission flows through the system
│   └── auth.md                     # Auth specification
│
└── README.md
```

---

## 🚀 Getting Started

### Prerequisites

Ensure you have the following installed on your system:
- **Go** (v1.25 or higher)
- **Node.js** (v22.x or higher) and `npm`
- **MongoDB Atlas** account (or active cloud cluster URI)
- **Docker** — for the judge sandbox, and for RabbitMQ and Redis via Compose

---

### 1. Backend Setup

1. **Navigate to the server directory**:
   ```bash
   cd server
   ```

2. **Configure Environment Variables**:
   Create a `.env` file inside the `server/` directory (or update the template):
   ```env
   PORT=8080
   # Your Atlas connection string, copied from the MongoDB dashboard.
   MONGO_URI=
   DB_NAME=online_judge
   JWT_SECRET=your_super_secret_jwt_signing_key
   CLIENT_URL=http://localhost:5173

   # Optional — these defaults match docker-compose.yml.
   # Without them the API judges submissions inline instead of queueing,
   # and War Room updates stay within a single API instance.
   RABBITMQ_URL=amqp://guest:guest@localhost:5672/
   REDIS_URL=redis://localhost:6379/0
   WORKER_COUNT=4
   ```

3. **Install Dependencies & Tidy Modules**:
   ```bash
   go mod tidy
   ```

4. **Start the infrastructure** (from the repository root):
   ```bash
   docker compose up -d          # RabbitMQ + Redis
   docker build -t codearena-sandbox:latest docker/judge-sandbox
   ```

5. **Seed some problems** (optional, and promotes a user to admin):
   ```bash
   go run ./cmd/seed --admin-email you@example.com
   ```

6. **Run the API server**:
   ```bash
   go run ./cmd/api
   ```
   The backend API will start on `http://localhost:8080`.

7. **Run at least one judge worker**, in a second terminal:
   ```bash
   go run ./cmd/worker
   ```
   Workers consume the queue and judge submissions. Run as many as you
   need — judging capacity scales independently of the API.

---

### 2. Frontend Setup

1. **Navigate to the client directory**:
   ```bash
   cd client
   ```

2. **Install Dependencies**:
   ```bash
   npm install
   ```

3. **Start Development Server**:
   ```bash
   npm run dev
   ```
   The frontend application will launch at `http://localhost:5173`.

---

## 🔌 API Reference

### Authentication Endpoints

| Endpoint | Method | Auth Required | Description |
| :--- | :--- | :---: | :--- |
| `/api/auth/register` | `POST` | ❌ | Register a new user account |
| `/api/auth/login` | `POST` | ❌ | Authenticate user & issue HTTP-Only JWT cookie |
| `/api/auth/logout` | `POST` | ❌ | Invalidate session & clear HTTP-Only cookie |
| `/api/auth/me` | `GET` | ✅ | Fetch currently authenticated user's profile |

#### Request & Response Example: User Registration

`POST /api/auth/register`

```json
// Request Payload
{
  "full_name": "Jane Doe",
  "username": "janedoe",
  "email": "jane@example.com",
  "password": "SecurePassword123!",
  "dob": "2000-05-15"
}

// Response (201 Created)
{
  "success": true,
  "message": "User registered successfully",
  "data": {
    "user": {
      "id": "669fa12b84c9f1a23b4e5f67",
      "username": "janedoe",
      "email": "jane@example.com",
      "full_name": "Jane Doe",
      "dob": "2000-05-15T00:00:00Z",
      "role": "user",
      "created_at": "2026-07-23T14:20:00Z"
    }
  }
}
```

### Problems & Submissions

| Endpoint | Method | Auth | Description |
| :--- | :--- | :---: | :--- |
| `/api/problems` | `GET` | ❌ | List problems, filtered by difficulty, tag, company or search |
| `/api/problems/recent` | `GET` | ❌ | Most recently added problems |
| `/api/problems/:slug` | `GET` | ❌ | Problem detail with its **sample** test cases only |
| `/api/problems/:slug/submit` | `POST` | ✅ | Queue a solution for judging (returns `202` and a submission id) |
| `/api/submissions/:id` | `GET` | ✅ | Poll one submission; readable only by its author |
| `/api/judge/run`, `/api/judge/run-raw` | `POST` | ✅ | Playground execution against ad-hoc input |
| `/api/stats/summary` | `GET` | ❌ | Community totals for the landing page |

### Profile

| Endpoint | Method | Auth | Description |
| :--- | :--- | :---: | :--- |
| `/api/users/me` | `GET` / `PATCH` | ✅ | Read or edit your own account details |
| `/api/users/me/stats` | `GET` | ✅ | Solve counts broken down by difficulty |
| `/api/users/me/submissions` | `GET` | ✅ | Filterable, paginated submission history |

### War Rooms

| Endpoint | Method | Auth | Description |
| :--- | :--- | :---: | :--- |
| `/api/warrooms` | `GET` / `POST` | ✅ | List open rooms, or create one (rate limited) |
| `/api/warrooms/mine` | `GET` | ✅ | Rooms you have taken part in |
| `/api/warrooms/:code` | `GET` | ✅ | Room detail by shareable code |
| `/api/warrooms/:code/join` | `POST` | ✅ | Join a waiting room; filling it starts the race |
| `/api/warrooms/:code/submit` | `POST` | ✅ | Submit inside a race, on the priority judging lane |
| `/ws/warroom/:code` | `WS` | ✅ | Live race events for participants |

### Discussions & Company Tags

| Endpoint | Method | Auth | Description |
| :--- | :--- | :---: | :--- |
| `/api/problems/:slug/discussions` | `GET` | ❌ | Read a problem's threads |
| `/api/problems/:slug/discussions` | `POST` | ✅ | Post a comment or reply (rate limited) |
| `/api/discussions/:id/upvote` | `POST` / `DELETE` | ✅ | Cast or withdraw an upvote (idempotent) |
| `/api/discussions/:id` | `DELETE` | ✅ | Delete your own comment; admins may moderate any |
| `/api/problems/:slug/company-tags` | `GET` / `POST` | ❌ / ✅ | Read aggregated tags, or report a company |
| `/api/companies` | `GET` | ❌ | Companies with at least one tag |
| `/api/companies/:name/problems` | `GET` | ❌ | Problems tagged with a company |

### Admin

| Endpoint | Method | Auth | Description |
| :--- | :--- | :---: | :--- |
| `/api/admin/problems` | `POST` | 🛡️ | Create a problem |
| `/api/admin/problems/:id` | `PUT` | 🛡️ | Update a problem |
| `/api/admin/problems/:id/testcases` | `GET` / `POST` | 🛡️ | List **all** test cases, or add one |

🛡️ = requires `role: admin`.

---

## 🧪 Testing

CodeArena maintains a comprehensive TDD test suite covering data models and HTTP controller endpoints.

To run the backend test suite:

```bash
cd server
go test ./...          # unit tests plus the MongoDB integration suite
go vet ./... && gofmt -l .
```

Unit tests run against in-memory fake repositories (`*test` subpackages), so
service rules are verified without a database. The `server/tests` package holds
the integration tests, which use a dedicated `online_judge_test` database and a
scripted sandbox so no Docker daemon is needed.

Frontend:

```bash
cd client
npm run lint
npm run build
```

> **Note**: Integration tests connect to MongoDB Atlas to execute real database assertions against a dedicated `online_judge_test` database which is automatically purged after test execution.

---

## 🗺️ Roadmap

- [x] **Phase 1: Project Setup & Auth System**
  - [x] MVC Architecture & Coding Constitution setup
  - [x] Go/Gin API & MongoDB Atlas connection
  - [x] TDD Backend User Model & Auth Controllers
  - [x] HTTP-Only Cookie JWT Authentication
  - [x] React SPA with Glassmorphism Login & Signup UI
- [x] **Phase 2: Problem Management & Exploration**
  - [x] MongoDB schema for Problems & Testcases
  - [x] Admin CRUD endpoints for problems
  - [x] Problem List page with filtering (Difficulty, Tags, Company)
- [x] **Phase 3: Code Execution & Judge Engine**
  - [x] RabbitMQ integration for async submission queuing
  - [x] Worker service with a Docker sandbox
  - [x] Verdict evaluation (Accepted, TLE, MLE, Wrong Answer, Runtime/Compile Error)
  - [x] Durable submission records, history and profile statistics
- [x] **Phase 4: War Room & Live Synchronized Coding**
  - [x] Gorilla WebSocket server implementation
  - [x] Redis Pub/Sub cross-instance fanout
  - [x] Dedicated priority queue lane for War Rooms
  - [x] Server-authoritative winner determination
- [x] **Phase 5: Community Features**
  - [x] Threaded per-problem discussion board
  - [x] Company interview tagging system and company explorer
  - [x] Admin dashboard for problems and test cases
- [ ] **Phase 6 (V2): AI & Scale**
  - [ ] Pre-warmed sandbox pooling, moving from Docker to `isolate`
  - [ ] Autoscaling worker pools driven by queue depth
  - [ ] MOSS-style plagiarism detection
  - [ ] AI-assisted hints and code review

---

## 📚 Documentation

Detailed documentation is available in the [`docs/`](./docs/) directory:
- 📜 **[CONSTITUTION.md](./docs/CONSTITUTION.md)**: Coding standards, TDD rules, directory conventions, and security guidelines.
- 🏗️ **[architecture.md](./docs/architecture.md)**: How a submission flows from request to verdict, why the queue has two lanes, how War Room races are decided, and the sandbox guarantees.
- 🔐 **[auth.md](./docs/auth.md)**: Full technical specification of the authentication architecture, security model, and API flows.

---

## 📄 License

Distributed under the MIT License. See `LICENSE` for more information.
