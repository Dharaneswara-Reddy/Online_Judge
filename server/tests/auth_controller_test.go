package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/controllers"
	"github.com/toji339/online-judge/internal/middleware"
	"github.com/toji339/online-judge/internal/models"
)

// setupRouter creates a Gin engine with all auth routes
// mounted, using the test database. This is used by every
// controller test to make HTTP requests against real handlers.
func setupRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	// Create the auth controller with the test database and a test JWT secret
	authController := controllers.NewAuthController(testDB, "test-jwt-secret-key-for-testing")

	// Mount routes exactly as they will be in production
	auth := router.Group("/api/auth")
	{
		auth.POST("/register", authController.Register)
		auth.POST("/login", authController.Login)
		auth.POST("/logout", authController.Logout)

		// Protected route — requires auth middleware
		protected := auth.Group("")
		protected.Use(middleware.AuthMiddleware("test-jwt-secret-key-for-testing"))
		{
			protected.GET("/me", authController.GetMe)
		}
	}

	return router
}

// apiResponse is a generic response structure used to
// parse JSON responses from the API in tests.
type apiResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
	Errors  []string        `json:"errors,omitempty"`
}

// =============================================================
// Register endpoint tests
// =============================================================

// TestRegister_Success verifies that a valid registration
// request returns 201 with the created user data.
func TestRegister_Success(t *testing.T) {
	cleanUsersCollection(t)
	router := setupRouter()

	body := `{
		"full_name": "Test User",
		"username": "testuser",
		"email": "test@example.com",
		"password": "password123"
	}`

	req, _ := http.NewRequest("POST", "/api/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	// Should return 201 Created
	assert.Equal(t, http.StatusCreated, w.Code)

	// Parse the response
	var resp apiResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.True(t, resp.Success)
	assert.Equal(t, "User registered successfully", resp.Message)

	// The data should contain the user object
	var data map[string]interface{}
	err = json.Unmarshal(resp.Data, &data)
	require.NoError(t, err)

	user := data["user"].(map[string]interface{})
	assert.Equal(t, "testuser", user["username"])
	assert.Equal(t, "test@example.com", user["email"])
	assert.Equal(t, "Test User", user["full_name"])
	assert.Equal(t, "user", user["role"])

	// Password hash must NEVER appear in the response
	assert.Nil(t, user["password_hash"], "Password hash should never be in the response")
}

// TestRegister_MissingFields verifies that the register endpoint
// returns 400 when required fields are missing.
func TestRegister_MissingFields(t *testing.T) {
	cleanUsersCollection(t)
	router := setupRouter()

	// Missing email and password
	body := `{
		"full_name": "Test User",
		"username": "testuser"
	}`

	req, _ := http.NewRequest("POST", "/api/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp apiResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.False(t, resp.Success)
}

// TestRegister_ShortPassword verifies that the register endpoint
// returns 400 when the password is shorter than 6 characters.
func TestRegister_ShortPassword(t *testing.T) {
	cleanUsersCollection(t)
	router := setupRouter()

	body := `{
		"full_name": "Test User",
		"username": "testuser",
		"email": "test@example.com",
		"password": "12345"
	}`

	req, _ := http.NewRequest("POST", "/api/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp apiResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.False(t, resp.Success)
}

// TestRegister_ShortUsername verifies that the register endpoint
// returns 400 when the username is shorter than 3 characters.
func TestRegister_ShortUsername(t *testing.T) {
	cleanUsersCollection(t)
	router := setupRouter()

	body := `{
		"full_name": "Test User",
		"username": "ab",
		"email": "test@example.com",
		"password": "password123"
	}`

	req, _ := http.NewRequest("POST", "/api/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestRegister_DuplicateEmail verifies that registering with
// an already-used email returns 409 Conflict.
func TestRegister_DuplicateEmail(t *testing.T) {
	cleanUsersCollection(t)
	router := setupRouter()

	body := `{
		"full_name": "User One",
		"username": "userone",
		"email": "dupe@example.com",
		"password": "password123"
	}`

	// First registration should succeed
	req, _ := http.NewRequest("POST", "/api/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)

	// Second registration with same email should fail
	body2 := `{
		"full_name": "User Two",
		"username": "usertwo",
		"email": "dupe@example.com",
		"password": "password456"
	}`
	req2, _ := http.NewRequest("POST", "/api/auth/register", strings.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusConflict, w2.Code)
}

// =============================================================
// Login endpoint tests
// =============================================================

// TestLogin_Success verifies that valid credentials return 200,
// user data, and set an HTTP-only JWT cookie.
func TestLogin_Success(t *testing.T) {
	cleanUsersCollection(t)
	router := setupRouter()

	// Register a user first
	regBody := `{
		"full_name": "Login Test",
		"username": "logintest",
		"email": "login@example.com",
		"password": "password123"
	}`
	req, _ := http.NewRequest("POST", "/api/auth/register", strings.NewReader(regBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	// Now log in with the same credentials
	loginBody := `{
		"email": "login@example.com",
		"password": "password123"
	}`
	req2, _ := http.NewRequest("POST", "/api/auth/login", strings.NewReader(loginBody))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	// Should return 200
	assert.Equal(t, http.StatusOK, w2.Code)

	// Parse the response
	var resp apiResponse
	err := json.Unmarshal(w2.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, "Login successful", resp.Message)

	// Check that the JWT cookie is set
	cookies := w2.Result().Cookies()
	var tokenCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "token" {
			tokenCookie = c
			break
		}
	}
	require.NotNil(t, tokenCookie, "A 'token' cookie should be set on login")
	assert.True(t, tokenCookie.HttpOnly, "Token cookie must be HttpOnly")
	assert.NotEmpty(t, tokenCookie.Value, "Token cookie value should not be empty")
}

// TestLogin_WrongPassword verifies that an incorrect password
// returns 401 with a generic error message.
func TestLogin_WrongPassword(t *testing.T) {
	cleanUsersCollection(t)
	router := setupRouter()

	// Register a user first
	regBody := `{
		"full_name": "Wrong Pass",
		"username": "wrongpass",
		"email": "wrong@example.com",
		"password": "correctpassword"
	}`
	req, _ := http.NewRequest("POST", "/api/auth/register", strings.NewReader(regBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	// Try logging in with the wrong password
	loginBody := `{
		"email": "wrong@example.com",
		"password": "wrongpassword"
	}`
	req2, _ := http.NewRequest("POST", "/api/auth/login", strings.NewReader(loginBody))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusUnauthorized, w2.Code)

	var resp apiResponse
	err := json.Unmarshal(w2.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.False(t, resp.Success)
	// Generic message — should NOT reveal whether email or password was wrong
	assert.Equal(t, "Invalid email or password", resp.Message)
}

// TestLogin_NonexistentEmail verifies that logging in with
// an email that doesn't exist returns 401 (same as wrong password).
func TestLogin_NonexistentEmail(t *testing.T) {
	cleanUsersCollection(t)
	router := setupRouter()

	loginBody := `{
		"email": "noone@example.com",
		"password": "password123"
	}`
	req, _ := http.NewRequest("POST", "/api/auth/login", strings.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var resp apiResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "Invalid email or password", resp.Message)
}

// TestLogin_MissingFields verifies that the login endpoint
// returns 400 when email or password is missing.
func TestLogin_MissingFields(t *testing.T) {
	cleanUsersCollection(t)
	router := setupRouter()

	// Missing password
	body := `{ "email": "test@example.com" }`
	req, _ := http.NewRequest("POST", "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// =============================================================
// GetMe endpoint tests
// =============================================================

// TestGetMe_Authenticated verifies that an authenticated user
// can access the /me endpoint and receive their profile data.
func TestGetMe_Authenticated(t *testing.T) {
	cleanUsersCollection(t)
	router := setupRouter()

	// Register a user
	regBody := `{
		"full_name": "Me Test",
		"username": "metest",
		"email": "me@example.com",
		"password": "password123"
	}`
	req, _ := http.NewRequest("POST", "/api/auth/register", strings.NewReader(regBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	// Log in to get the JWT cookie
	loginBody := `{
		"email": "me@example.com",
		"password": "password123"
	}`
	req2, _ := http.NewRequest("POST", "/api/auth/login", strings.NewReader(loginBody))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)

	// Extract the token cookie from the login response
	cookies := w2.Result().Cookies()
	var tokenCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "token" {
			tokenCookie = c
			break
		}
	}
	require.NotNil(t, tokenCookie)

	// Call GET /api/auth/me with the token cookie
	req3, _ := http.NewRequest("GET", "/api/auth/me", nil)
	req3.AddCookie(tokenCookie)
	w3 := httptest.NewRecorder()
	router.ServeHTTP(w3, req3)

	// Should return 200 with user data
	assert.Equal(t, http.StatusOK, w3.Code)

	var resp apiResponse
	err := json.Unmarshal(w3.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.True(t, resp.Success)

	var data map[string]interface{}
	err = json.Unmarshal(resp.Data, &data)
	require.NoError(t, err)
	user := data["user"].(map[string]interface{})
	assert.Equal(t, "metest", user["username"])
	assert.Equal(t, "me@example.com", user["email"])
}

// TestGetMe_NoCookie verifies that the /me endpoint returns
// 401 when no authentication cookie is present.
func TestGetMe_NoCookie(t *testing.T) {
	router := setupRouter()

	req, _ := http.NewRequest("GET", "/api/auth/me", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestGetMe_InvalidToken verifies that the /me endpoint returns
// 401 when the token cookie contains an invalid JWT.
func TestGetMe_InvalidToken(t *testing.T) {
	router := setupRouter()

	req, _ := http.NewRequest("GET", "/api/auth/me", nil)
	req.AddCookie(&http.Cookie{
		Name:  "token",
		Value: "this-is-not-a-valid-jwt",
	})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// =============================================================
// Logout endpoint tests
// =============================================================

// TestLogout_ClearsCookie verifies that the logout endpoint
// clears the JWT cookie by setting MaxAge to -1.
func TestLogout_ClearsCookie(t *testing.T) {
	router := setupRouter()

	req, _ := http.NewRequest("POST", "/api/auth/logout", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Check that the token cookie is cleared
	cookies := w.Result().Cookies()
	var tokenCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "token" {
			tokenCookie = c
			break
		}
	}
	require.NotNil(t, tokenCookie, "Logout should set the token cookie")
	assert.True(t, tokenCookie.MaxAge < 0, "Token cookie MaxAge should be negative to clear it")
}

// =============================================================
// Integration test: full auth flow
// =============================================================

// TestFullAuthFlow walks through the complete auth lifecycle:
// register → login → access protected route → logout → verify
// the protected route is no longer accessible.
func TestFullAuthFlow(t *testing.T) {
	cleanUsersCollection(t)
	router := setupRouter()

	// Step 1: Register
	regBody := `{
		"full_name": "Flow Test",
		"username": "flowtest",
		"email": "flow@example.com",
		"password": "password123"
	}`
	req, _ := http.NewRequest("POST", "/api/auth/register", strings.NewReader(regBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code, "Registration should succeed")

	// Step 2: Login
	loginBody := `{
		"email": "flow@example.com",
		"password": "password123"
	}`
	req2, _ := http.NewRequest("POST", "/api/auth/login", strings.NewReader(loginBody))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code, "Login should succeed")

	// Extract token cookie
	var tokenCookie *http.Cookie
	for _, c := range w2.Result().Cookies() {
		if c.Name == "token" {
			tokenCookie = c
			break
		}
	}
	require.NotNil(t, tokenCookie)

	// Step 3: Access /me with the cookie
	req3, _ := http.NewRequest("GET", "/api/auth/me", nil)
	req3.AddCookie(tokenCookie)
	w3 := httptest.NewRecorder()
	router.ServeHTTP(w3, req3)
	assert.Equal(t, http.StatusOK, w3.Code, "Authenticated /me should return 200")

	// Step 4: Logout
	req4, _ := http.NewRequest("POST", "/api/auth/logout", nil)
	w4 := httptest.NewRecorder()
	router.ServeHTTP(w4, req4)
	assert.Equal(t, http.StatusOK, w4.Code, "Logout should succeed")

	// Step 5: Try /me without cookie — should fail
	req5, _ := http.NewRequest("GET", "/api/auth/me", nil)
	w5 := httptest.NewRecorder()
	router.ServeHTTP(w5, req5)
	assert.Equal(t, http.StatusUnauthorized, w5.Code, "/me without cookie should return 401")
}

// =============================================================
// Edge case: register doesn't set a login cookie
// =============================================================

// TestRegister_DoesNotSetCookie verifies that the register
// endpoint does NOT automatically log the user in (no cookie).
// The user must explicitly call /login after registering.
func TestRegister_DoesNotSetCookie(t *testing.T) {
	cleanUsersCollection(t)
	router := setupRouter()

	body := `{
		"full_name": "No Cookie",
		"username": "nocookie",
		"email": "nocookie@example.com",
		"password": "password123"
	}`
	req, _ := http.NewRequest("POST", "/api/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	// No token cookie should be set
	cookies := w.Result().Cookies()
	for _, c := range cookies {
		assert.NotEqual(t, "token", c.Name, "Register should NOT set a token cookie")
	}
}

// =============================================================
// Edge case: register with invalid JSON body
// =============================================================

// TestRegister_InvalidJSON verifies that sending malformed JSON
// to the register endpoint returns 400 Bad Request.
func TestRegister_InvalidJSON(t *testing.T) {
	router := setupRouter()

	body := `{ this is not valid json }`
	req, _ := http.NewRequest("POST", "/api/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// =============================================================
// Edge case: password hash never leaked
// =============================================================

// TestRegister_PasswordHashNotInResponse_Detailed ensures that
// the raw response body does not contain the password hash string
// (defense in depth — even if json:"-" is accidentally removed).
func TestRegister_PasswordHashNotInResponse_Detailed(t *testing.T) {
	cleanUsersCollection(t)
	router := setupRouter()

	body := `{
		"full_name": "Hash Check",
		"username": "hashcheck",
		"email": "hashcheck@example.com",
		"password": "password123"
	}`
	req, _ := http.NewRequest("POST", "/api/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)

	// The response body should never contain "$2a$" which is the bcrypt prefix
	responseBody := w.Body.String()
	assert.NotContains(t, responseBody, "$2a$", "Response must never contain a bcrypt hash")
	assert.NotContains(t, responseBody, "password_hash", "Response must never contain password_hash field")
}

// =============================================================
// Edge case: empty request body
// =============================================================

// TestRegister_EmptyBody verifies that sending an empty body
// to the register endpoint returns 400 Bad Request.
func TestRegister_EmptyBody(t *testing.T) {
	router := setupRouter()

	req, _ := http.NewRequest("POST", "/api/auth/register", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestLogin_EmptyBody verifies that sending an empty body
// to the login endpoint returns 400 Bad Request.
func TestLogin_EmptyBody(t *testing.T) {
	router := setupRouter()

	req, _ := http.NewRequest("POST", "/api/auth/login", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// =============================================================
// Unused import guard — these imports ensure models is used
// =============================================================
var _ = models.User{}
