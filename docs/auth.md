# Authentication System — Technical Documentation

This document explains how authentication works in the Online Judge platform.
It covers the flow, API endpoints, security decisions, and how the frontend
and backend communicate.

---

## Overview

The auth system uses **JWT tokens stored in HTTP-only cookies**. Here's why:

- **HTTP-only** — JavaScript can't read the cookie, which blocks XSS attacks
  from stealing the token.
- **No localStorage** — We don't store any auth data on the client side.
  The browser handles cookie storage automatically.
- **Server-controlled** — The server sets the cookie on login and clears it
  on logout. The frontend never touches the raw JWT.

---

## Auth Flow

```
┌──────────┐          ┌──────────┐          ┌──────────┐
│  Browser │          │  Go API  │          │  MongoDB │
│ (React)  │          │  (Gin)   │          │  Atlas   │
└────┬─────┘          └────┬─────┘          └────┬─────┘
     │                     │                     │
     │  POST /auth/register│                     │
     │  {name,user,email,  │                     │
     │   password}         │                     │
     │────────────────────>│                     │
     │                     │  Hash password      │
     │                     │  Insert user doc    │
     │                     │────────────────────>│
     │                     │  Created user       │
     │                     │<────────────────────│
     │  201 {user data}    │                     │
     │<────────────────────│                     │
     │                     │                     │
     │  POST /auth/login   │                     │
     │  {email, password}  │                     │
     │────────────────────>│                     │
     │                     │  Find user by email │
     │                     │────────────────────>│
     │                     │  User document      │
     │                     │<────────────────────│
     │                     │  Compare bcrypt hash│
     │                     │  Generate JWT       │
     │  200 {user data}    │                     │
     │  Set-Cookie: token  │                     │
     │<────────────────────│                     │
     │                     │                     │
     │  GET /auth/me       │                     │
     │  Cookie: token=JWT  │                     │
     │────────────────────>│                     │
     │                     │  Validate JWT       │
     │                     │  Extract user ID    │
     │                     │  Find user by ID    │
     │                     │────────────────────>│
     │                     │  User document      │
     │                     │<────────────────────│
     │  200 {user data}    │                     │
     │<────────────────────│                     │
     │                     │                     │
     │  POST /auth/logout  │                     │
     │────────────────────>│                     │
     │                     │  Clear cookie       │
     │  200 {success}      │  (MaxAge = -1)      │
     │  Set-Cookie: token= │                     │
     │<────────────────────│                     │
```

---

## API Endpoints

### POST /api/auth/register

Creates a new user account.

**Request body:**
```json
{
  "full_name": "John Doe",
  "username": "johndoe",
  "email": "john@example.com",
  "password": "securepass123",
  "dob": "2000-01-15"
}
```

**Success response (201):**
```json
{
  "success": true,
  "message": "User registered successfully",
  "data": {
    "user": {
      "id": "64a1b2c3d4e5f6...",
      "username": "johndoe",
      "email": "john@example.com",
      "full_name": "John Doe",
      "role": "user",
      "created_at": "2024-01-15T10:30:00Z"
    }
  }
}
```

**Error responses:**
- `400` — Missing or invalid fields
- `409` — Username or email already exists
- `500` — Server error

### POST /api/auth/login

Authenticates a user and sets the JWT cookie.

**Request body:**
```json
{
  "email": "john@example.com",
  "password": "securepass123"
}
```

**Success response (200):**
```json
{
  "success": true,
  "message": "Login successful",
  "data": {
    "user": {
      "id": "64a1b2c3d4e5f6...",
      "username": "johndoe",
      "email": "john@example.com",
      "full_name": "John Doe",
      "role": "user"
    }
  }
}
```

**Cookie set:**
```
Set-Cookie: token=eyJhbGci...; Path=/; HttpOnly; Max-Age=86400; SameSite=Lax
```

**Error responses:**
- `400` — Missing email or password
- `401` — Invalid credentials (generic message)

### GET /api/auth/me

Returns the current user's profile. Requires a valid auth cookie.

**Success response (200):**
Same structure as login response.

**Error responses:**
- `401` — No cookie or invalid/expired token

### POST /api/auth/logout

Clears the JWT cookie.

**Success response (200):**
```json
{
  "success": true,
  "message": "Logged out successfully"
}
```

---

## Security Decisions

### Why HTTP-only cookies instead of localStorage?

localStorage is accessible from JavaScript. If an attacker finds an XSS
vulnerability in the app, they can steal the token with one line of code.
HTTP-only cookies are invisible to JavaScript — the browser sends them
automatically, but no script can read them.

### Why bcrypt with cost 10?

bcrypt is a slow hashing algorithm by design — it makes brute-force attacks
expensive. Cost 10 means each hash takes about 100ms, which is fast enough
for login but slow enough to make dictionary attacks impractical.

### Why generic login error messages?

When login fails, we always say "Invalid email or password" — never "Email
not found" or "Wrong password." This prevents attackers from discovering
which emails are registered (account enumeration).

### Why unique indexes on username and email?

MongoDB unique indexes enforce uniqueness at the database level, even if
the application code has a bug. This is defense in depth — the database
is the last line of defense against duplicate accounts.

---

## JWT Token Structure

The JWT payload contains:

```json
{
  "sub": "64a1b2c3d4e5f6...",   // User's MongoDB ObjectID
  "username": "johndoe",         // For quick access without DB lookup
  "role": "user",                // For authorization checks
  "exp": 1705401600,             // Expires in 24 hours
  "iat": 1705315200              // Issued at
}
```

The token is signed with HMAC-SHA256 using the `JWT_SECRET` from the `.env` file.

---

## Frontend Auth Flow

The React frontend uses an `AuthContext` provider:

1. **On page load** — `AuthContext` calls `GET /auth/me`. If the cookie is
   valid, the user state is set. If not (401), user stays null.

2. **Login** — Form calls `AuthContext.login(email, password)`. The server
   sets the cookie. AuthContext updates the user state.

3. **Register** — Form calls `AuthContext.register(data)`. The user is
   created but NOT auto-logged in. The user is redirected to `/login`.

4. **Logout** — Calls `AuthContext.logout()`. The server clears the cookie.
   The user state is set to null. The router redirects to `/login`.

5. **Protected routes** — The `ProtectedRoute` component checks `user`
   from AuthContext. If null, it redirects to `/login`.

---

## File Map

| File | Purpose |
|------|---------|
| `server/internal/models/user_model.go` | User struct + DB operations |
| `server/internal/controllers/auth_controller.go` | Register, Login, Logout, GetMe handlers |
| `server/internal/middleware/auth_middleware.go` | JWT cookie validation |
| `server/internal/routes/routes.go` | Route mounting + CORS |
| `server/internal/config/config.go` | Environment variable loading |
| `server/internal/database/mongo.go` | MongoDB Atlas connection |
| `server/tests/user_model_test.go` | Model-level tests |
| `server/tests/auth_controller_test.go` | Controller-level (HTTP) tests |
| `client/src/context/AuthContext.jsx` | Auth state management |
| `client/src/api/axios.js` | HTTP client with cookie support |
| `client/src/pages/Login.jsx` | Login page |
| `client/src/pages/Signup.jsx` | Registration page |
| `client/src/components/ProtectedRoute.jsx` | Route guard |
