# Online Judge — Coding Constitution

> The rules every contributor follows. No exceptions.

---

## 1. Architecture

### 1.1 MVC Pattern (Model–View–Controller)

The project follows a strict MVC separation:

| Layer | Location | Responsibility |
|-------|----------|----------------|
| **Model** | `server/internal/models/` | Data structures, DB schemas, validation rules |
| **Controller** | `server/internal/controllers/` | Receives HTTP requests, calls model logic, returns responses |
| **View** | `client/src/` | React frontend — all UI rendering and user interaction |

- **Controllers** never touch the database directly. They call model functions.
- **Models** never read HTTP request objects. They receive plain Go types.
- **Views** (React) never call the database. They talk to controllers via the REST API.

### 1.2 Directory Conventions

```
server/                          # Go backend
├── cmd/api/main.go              # Single entry point
├── internal/
│   ├── config/                  # Environment + app configuration
│   ├── database/                # DB connection setup
│   ├── models/                  # Data models + DB operations
│   ├── controllers/             # Request handlers (thin — delegate to models)
│   ├── middleware/               # Auth, logging, rate-limiting
│   └── routes/                  # Route definitions + grouping
└── tests/                       # All test files

client/                          # React frontend
├── src/
│   ├── api/                     # HTTP client setup
│   ├── context/                 # React context providers
│   ├── components/              # Reusable UI components
│   └── pages/                   # Page-level components
```

---

## 2. Test-Driven Development (TDD)

Every feature follows the **Red → Green → Refactor** cycle:

1. **Red** — Write a failing test that defines the expected behavior.
2. **Green** — Write the minimum code to make the test pass.
3. **Refactor** — Clean up the code while keeping all tests green.

### Rules

- No production code is written without a corresponding test being written **first**.
- Tests live in `server/tests/` (Go backend) and `client/src/__tests__/` (React frontend).
- Test file naming: `auth_test.go`, `user_model_test.go`, etc.
- Every API endpoint has at least:
  - A **happy path** test (valid input → expected output).
  - A **validation failure** test (missing/bad input → proper error).
  - An **edge case** test (duplicates, unauthorized access, etc.).
- Run `go test ./...` before every commit. All tests must pass.

---

## 3. Commenting Standards

### 3.1 Function-Level Comments

Every exported function gets a Go-doc comment explaining **what** it does:

```go
// RegisterUser creates a new user account in the database.
// It hashes the password, validates uniqueness, and returns the created user.
func RegisterUser(ctx context.Context, user *User) (*User, error) {
```

### 3.2 Step-by-Step Procedural Comments

Inside controller and model functions, include a numbered step outline
at the top, then implement each step beneath its comment:

```go
func Register(c *gin.Context) {
    // Steps to follow while registering a user
    // ==========================================

    // 1. Get the user data from the request body

    // 2. Check if we are getting all required data fields
    //    (full_name, username, email, password)

    // 3. Validate the user data (email format, password length)

    // 4. Check if the user already exists in the database

    // 5. Hash the password using bcrypt

    // 6. Save the user document to the database

    // 7. Send a success response to the client
}
```

### 3.3 Package-Level Comments

Every package gets a doc comment in one of its files:

```go
// Package controllers handles incoming HTTP requests,
// validates input, delegates to model functions, and
// returns JSON responses to the client.
package controllers
```

### 3.4 Inline Comments

Use inline comments for non-obvious logic only. Don't comment the obvious:

```go
// Good — explains WHY
cost := 12 // bcrypt cost of 12 balances security and speed for our use case

// Bad — restates WHAT
i++ // increment i
```

---

## 4. Code Style

### 4.1 Go Backend

- **Formatter**: `gofmt` — no exceptions, no custom formatting.
- **Linter**: `go vet` at minimum.
- **Naming**:
  - Files: `snake_case.go` (e.g., `auth_controller.go`, `user_model.go`).
  - Exported types/functions: `PascalCase` (e.g., `RegisterUser`).
  - Unexported types/functions: `camelCase` (e.g., `hashPassword`).
  - Constants: `PascalCase` (Go convention, not `SCREAMING_SNAKE`).
- **Error handling**: Always check errors. Never use `_` to ignore them silently.
  Return errors up the call chain; let the controller decide the HTTP status.
- **No global state**: Pass dependencies (DB, config) explicitly.
  Use dependency injection, not package-level variables.

### 4.2 React Frontend

- **Formatter**: Prettier with default settings.
- **Naming**:
  - Components: `PascalCase.jsx` (e.g., `Login.jsx`, `ProtectedRoute.jsx`).
  - Utilities/hooks: `camelCase.js` (e.g., `useAuth.js`, `axios.js`).
  - CSS classes: `kebab-case` (e.g., `.auth-card`, `.form-input`).
- **Component style**: Functional components with hooks. No class components.
- **State management**: React Context for auth state. No external state libraries (Redux, Zustand) unless complexity demands it later.

---

## 5. Security Principles

- **No client-side token storage** (no `localStorage`, no `sessionStorage`).
  JWTs are stored in **HTTP-only, Secure, SameSite cookies** set by the server.
  The frontend never sees or handles the raw token.
- **Passwords** are hashed with **bcrypt** (cost ≥ 10). Plain-text passwords
  never touch the database or logs.
- **Generic error messages** for auth failures — never reveal whether
  the email or password was wrong.
- **Input validation** happens on both the frontend (for UX) and backend
  (for security). The backend is the source of truth.
- **CORS** is explicitly configured — no wildcard `*` in production.

---

## 6. API Conventions

- **Base path**: `/api/`
- **Versioning**: Not applied in V1. Will add `/api/v2/` if breaking changes arise.
- **Response format** (success):
  ```json
  {
    "success": true,
    "message": "User registered successfully",
    "data": { ... }
  }
  ```
- **Response format** (error):
  ```json
  {
    "success": false,
    "message": "Validation failed",
    "errors": ["Email is required", "Password must be at least 6 characters"]
  }
  ```
- **HTTP status codes**: Use them correctly — `200`, `201`, `400`, `401`, `403`, `404`, `409`, `500`.

---

## 7. Git & Version Control

- **Branch naming**: `feature/auth`, `fix/login-bug`, `refactor/user-model`.
- **Commit messages**: Present-tense imperative — `"Add user registration endpoint"`, not `"Added..."`.
- **No secrets in Git**: `.env` files are always `.gitignore`d.
- **Small, focused commits**: One logical change per commit.

---

## 8. Documentation

- All documentation lives in the `docs/` folder.
- Every major feature gets its own doc file (e.g., `docs/auth.md`).
- Documentation is **technical but readable** — write like you're explaining
  to a developer who just joined the team.
- Keep sentences short. Avoid jargon when a simpler word works.
- Include code snippets and examples wherever possible.
- Update docs when the code changes. Stale docs are worse than no docs.

---

## 9. Environment & Configuration

- All secrets and environment-specific values go in `.env` (never hardcoded).
- The backend reads config via `godotenv` + a `Config` struct.
- Required env vars for the backend:
  - `MONGO_URI` — MongoDB Atlas connection string
  - `JWT_SECRET` — Secret key for signing JWTs
  - `PORT` — Server port (default: `8080`)
  - `DB_NAME` — Database name (default: `online_judge`)
  - `CLIENT_URL` — Frontend origin for CORS (default: `http://localhost:5173`)

---

## 10. Database Rules

- **MongoDB Atlas** (cloud) — no local MongoDB in production or development.
- All collections follow the schemas defined in the HLD document.
- **Indexes** are created programmatically on app startup, not manually.
- **No raw MongoDB queries in controllers** — all DB access goes through
  model functions.
