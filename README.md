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

### 🧩 Core Judge System (In Progress)
- **Problem Explorer**: Paginated browsing with multi-attribute filtering (Difficulty, Tags, Company).
- **In-Browser Code Editor**: Interactive code editor supporting multiple programming languages.
- **Async Execution Pipeline**: Decoupled submission processing via RabbitMQ worker queues.
- **Pre-warmed Sandboxing**: Resource-capped, isolated container execution using cgroups, tmpfs scrubbing, and no network access.

### ⚔️ War Room (V1 Planned)
- **Real-Time Multiplayer**: Instant 1v1/1v1v1 race mode with WebSocket communication.
- **Redis Pub/Sub Event Fanout**: Cross-instance socket state synchronization across horizontally scaled API instances.
- **Dedicated Priority Judging**: High-priority submission queue preventing practice traffic from delaying live races.
- **Server-Authoritative Winner Determination**: Judge-timestamped verdicts preventing client clock manipulation.

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
├── server/                         # Go Backend API
│   ├── cmd/
│   │   └── api/main.go             # Application Entrypoint
│   ├── internal/
│   │   ├── config/config.go        # Environment Configuration
│   │   ├── database/mongo.go       # MongoDB Atlas Connector & Indexer
│   │   ├── models/user_model.go    # User Data Model & MongoDB Logic
│   │   ├── controllers/            # HTTP Request Handlers
│   │   │   └── auth_controller.go  # Register, Login, Logout, GetMe
│   │   ├── middleware/             # HTTP Middleware
│   │   │   └── auth_middleware.go  # Cookie JWT Validation
│   │   └── routes/routes.go        # API Endpoint Routing & CORS
│   └── tests/                      # TDD Test Suite
│       ├── user_model_test.go      # Model Integration Tests
│       └── auth_controller_test.go # Controller HTTP Tests
│
├── client/                         # React Frontend SPA
│   ├── src/
│   │   ├── api/axios.js            # Axios Instance (withCredentials)
│   │   ├── context/AuthContext.jsx # Global Auth State Provider
│   │   ├── components/             # Reusable UI Components
│   │   │   └── ProtectedRoute.jsx  # Route Guard Component
│   │   ├── pages/                  # Application Page Views
│   │   │   ├── Login.jsx           # Glassmorphism Login View
│   │   │   ├── Signup.jsx          # Glassmorphism Registration View
│   │   │   └── Home.jsx            # User Dashboard View
│   │   ├── App.jsx                 # Client-side Router Setup
│   │   ├── index.css               # Design System & Utility Tokens
│   │   └── main.jsx                # React DOM Mount Entrypoint
│   └── index.html                  # HTML5 Template & SEO Meta
│
├── docs/                           # Technical Documentation
│   ├── CONSTITUTION.md             # Developer Constitution & Standards
│   └── auth.md                     # Auth Technical Specification
│
└── README.md                       # Project Documentation
```

---

## 🚀 Getting Started

### Prerequisites

Ensure you have the following installed on your system:
- **Go** (v1.25 or higher)
- **Node.js** (v22.x or higher) and `npm`
- **MongoDB Atlas** account (or active cloud cluster URI)

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
   MONGO_URI=mongodb+srv://<username>:<password>@<cluster>.mongodb.net/?retryWrites=true&w=majority
   DB_NAME=online_judge
   JWT_SECRET=your_super_secret_jwt_signing_key
   CLIENT_URL=http://localhost:5173
   ```

3. **Install Dependencies & Tidy Modules**:
   ```bash
   go mod tidy
   ```

4. **Run the API Server**:
   ```bash
   go run cmd/api/main.go
   ```
   The backend API will start on `http://localhost:8080`.

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

---

## 🧪 Testing

CodeArena maintains a comprehensive TDD test suite covering data models and HTTP controller endpoints.

To run the backend test suite:

```bash
cd server
go test ./tests/... -v
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
- [ ] **Phase 2: Problem Management & Exploration**
  - [ ] MongoDB schema for Problems & Testcases
  - [ ] Admin CRUD endpoints for problems
  - [ ] Problem List page with filtering (Difficulty, Tags, Company)
- [ ] **Phase 3: Code Execution & Judge Engine**
  - [ ] RabbitMQ integration for async submission queuing
  - [ ] Worker service with Docker sandbox pre-warming
  - [ ] Verdict evaluation (Accepted, TLE, MLE, Wrong Answer)
- [ ] **Phase 4: War Room & Live Synchronized Coding**
  - [ ] Gorilla WebSocket server implementation
  - [ ] Redis Pub/Sub cross-instance fanout
  - [ ] Dedicated priority queue lane for War Rooms
- [ ] **Phase 5: Community Features**
  - [ ] Threaded per-problem discussion board
  - [ ] Company interview tagging system

---

## 📚 Documentation

Detailed documentation is available in the [`docs/`](./docs/) directory:
- 📜 **[CONSTITUTION.md](./docs/CONSTITUTION.md)**: Coding standards, TDD rules, directory conventions, and security guidelines.
- 🔐 **[auth.md](./docs/auth.md)**: Full technical specification of the authentication architecture, security model, and API flows.

---

## 📄 License

Distributed under the MIT License. See `LICENSE` for more information.
