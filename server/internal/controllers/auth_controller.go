// Package controllers handles incoming HTTP requests for the
// Online Judge API. Each controller receives a request, validates
// the input, delegates to model functions for database operations,
// and returns a JSON response.
//
// Controllers are the "C" in MVC — they never touch the database
// directly. All DB access goes through the models package.
package controllers

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"github.com/toji339/online-judge/internal/models"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// AuthController holds the dependencies needed by the auth
// handlers — a reference to the MongoDB database and the JWT
// secret key. Dependencies are injected via NewAuthController
// rather than using global variables.
type AuthController struct {
	db        *mongo.Database
	jwtSecret string
}

// NewAuthController creates a new AuthController with the given
// database and JWT secret. This is called once at startup and
// the controller is then passed to the route setup.
func NewAuthController(db *mongo.Database, jwtSecret string) *AuthController {
	return &AuthController{
		db:        db,
		jwtSecret: jwtSecret,
	}
}

// emailRegex is a simple regex pattern for validating email format.
// It checks for the basic structure: something@something.something
var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

// Register handles new user registration.
// It validates the request body, delegates to the model for
// user creation, and returns the created user data.
//
// Route: POST /api/auth/register
func (ac *AuthController) Register(c *gin.Context) {
	// Steps to follow while registering a user
	// ==========================================

	// 1. Get the user data from the request body
	var input models.RegisterInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request body",
			"errors":  []string{"Request body must be valid JSON with the required fields"},
		})
		return
	}

	// 2. Check if we are getting all required data fields
	//    (full_name, username, email, password)
	var validationErrors []string

	if strings.TrimSpace(input.FullName) == "" {
		validationErrors = append(validationErrors, "Full name is required")
	}
	if strings.TrimSpace(input.Username) == "" {
		validationErrors = append(validationErrors, "Username is required")
	}
	if strings.TrimSpace(input.Email) == "" {
		validationErrors = append(validationErrors, "Email is required")
	}
	if strings.TrimSpace(input.Password) == "" {
		validationErrors = append(validationErrors, "Password is required")
	}

	if len(validationErrors) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Validation failed",
			"errors":  validationErrors,
		})
		return
	}

	// 3. Validate the user data
	//    - email must be a valid email format
	//    - password must be at least 6 characters
	//    - username must be at least 3 characters
	if !emailRegex.MatchString(input.Email) {
		validationErrors = append(validationErrors, "Email format is invalid")
	}
	if len(input.Password) < 6 {
		validationErrors = append(validationErrors, "Password must be at least 6 characters")
	}
	if len(input.Username) < 3 {
		validationErrors = append(validationErrors, "Username must be at least 3 characters")
	}

	if len(validationErrors) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Validation failed",
			"errors":  validationErrors,
		})
		return
	}

	// 4. Call the model to create the user
	//    (model handles hashing + uniqueness check internally)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	user, err := models.CreateUser(ctx, ac.db, &input)
	if err != nil {
		// Check if it's a duplicate key error (username or email taken)
		if strings.Contains(err.Error(), "already exists") {
			c.JSON(http.StatusConflict, gin.H{
				"success": false,
				"message": "Username or email already exists",
			})
			return
		}

		// Check for invalid DOB format
		if strings.Contains(err.Error(), "invalid date of birth") {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": "Invalid date of birth format",
				"errors":  []string{err.Error()},
			})
			return
		}

		// Any other error is a server error
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to create user",
		})
		return
	}

	// 5. Send a success response to the client (201 Created)
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": "User registered successfully",
		"data": gin.H{
			"user": user,
		},
	})
}

// Login authenticates a user and sets a JWT cookie.
// It finds the user by email, verifies the password, generates
// a JWT token, and sets it as an HTTP-only cookie.
//
// Route: POST /api/auth/login
func (ac *AuthController) Login(c *gin.Context) {
	// Steps to follow while logging in a user
	// =========================================

	// 1. Get the credentials from the request body (email, password)
	var input models.LoginInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request body",
			"errors":  []string{"Request body must be valid JSON with email and password"},
		})
		return
	}

	// 2. Check if both email and password are provided
	if strings.TrimSpace(input.Email) == "" || strings.TrimSpace(input.Password) == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Validation failed",
			"errors":  []string{"Email and password are required"},
		})
		return
	}

	// 3. Find the user by email in the database
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	user, err := models.FindUserByEmail(ctx, ac.db, input.Email)
	if err != nil {
		// Generic error message — never reveal whether email or password was wrong
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Invalid email or password",
		})
		return
	}

	// 4. Compare the provided password with the stored hash
	if !models.CheckPassword(user.PasswordHash, input.Password) {
		// Same generic message as above — no information leakage
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Invalid email or password",
		})
		return
	}

	// 5. Generate a JWT token with user claims (id, username, role)
	token, err := ac.generateJWT(user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to generate authentication token",
		})
		return
	}

	// 6. Set the JWT as an HTTP-only cookie on the response
	//    - HttpOnly: true  → JavaScript cannot access it (XSS protection)
	//    - Secure: false    → Allow HTTP in development (set true in production)
	//    - SameSite: Lax    → Cookie sent on top-level navigations and GET requests
	//    - MaxAge: 86400    → Cookie expires in 24 hours
	//    - Path: /          → Cookie is sent with every request
	c.SetCookie("token", token, 86400, "/", "", false, true)

	// 7. Send a success response with the user data (no token in body)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Login successful",
		"data": gin.H{
			"user": user,
		},
	})
}

// Logout clears the JWT cookie from the client.
// This is a simple operation — we just overwrite the cookie
// with an expired one so the browser deletes it.
//
// Route: POST /api/auth/logout
func (ac *AuthController) Logout(c *gin.Context) {
	// Steps to follow while logging out a user
	// ==========================================

	// 1. Clear the JWT cookie by setting MaxAge to -1
	//    This tells the browser to delete the cookie immediately
	c.SetCookie("token", "", -1, "/", "", false, true)

	// 2. Send a success response
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Logged out successfully",
	})
}

// GetMe returns the currently authenticated user's profile.
// This endpoint is protected by the auth middleware, which
// extracts the user ID from the JWT and sets it on the context.
//
// Route: GET /api/auth/me (protected)
func (ac *AuthController) GetMe(c *gin.Context) {
	// Steps to follow while getting the current user
	// ================================================

	// 1. Get the user ID from the Gin context (set by auth middleware)
	userIDStr, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "User not authenticated",
		})
		return
	}

	// 2. Convert the string ID to a MongoDB ObjectID
	userID, err := bson.ObjectIDFromHex(userIDStr.(string))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Invalid user ID in token",
		})
		return
	}

	// 3. Find the user by ID in the database
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	user, err := models.FindUserByID(ctx, ac.db, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "User not found",
		})
		return
	}

	// 4. Send the user data in the response
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "User profile retrieved",
		"data": gin.H{
			"user": user,
		},
	})
}

// generateJWT creates a signed JWT token for the given user.
// The token contains the user's ID, username, and role as claims,
// and expires after 24 hours.
func (ac *AuthController) generateJWT(user *models.User) (string, error) {
	// Define the claims for the token
	claims := jwt.MapClaims{
		"sub":      user.ID.Hex(),  // Subject — the user's MongoDB ObjectID
		"username": user.Username,  // Username for quick access without DB lookup
		"role":     user.Role,      // Role for authorization checks
		"exp":      time.Now().Add(24 * time.Hour).Unix(), // Expires in 24 hours
		"iat":      time.Now().Unix(), // Issued at
	}

	// Create and sign the token with the JWT secret
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString([]byte(ac.jwtSecret))
	if err != nil {
		return "", fmt.Errorf("failed to sign JWT: %w", err)
	}

	return signedToken, nil
}
