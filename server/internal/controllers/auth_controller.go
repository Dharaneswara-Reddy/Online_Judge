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
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"github.com/toji339/online-judge/internal/models"
	"github.com/toji339/online-judge/internal/session"

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
	// secureCookies marks the session cookie Secure. It is configuration
	// rather than a constant so local development over plain HTTP still
	// works, while deployments default to the safe setting.
	secureCookies bool

	// sessions records revoked sessions. Nil means revocation is not
	// configured; logout then only clears the cookie, as it used to.
	sessions session.Revoker
}

// NewAuthController creates a new AuthController with the given
// database and JWT secret. This is called once at startup and
// the controller is then passed to the route setup.
func NewAuthController(db *mongo.Database, jwtSecret string, secureCookies bool, sessions session.Revoker) *AuthController {
	return &AuthController{
		db:            db,
		jwtSecret:     jwtSecret,
		secureCookies: secureCookies,
		sessions:      sessions,
	}
}

// setSessionCookie writes the auth cookie with the flags that make it a
// safe session credential.
//
// SameSite=Lax is the one that matters most: Gin's default leaves the
// attribute off entirely, and a cookie with no SameSite is still sent on
// cross-site POSTs by browsers that have not adopted Lax-by-default,
// which makes every state-changing endpoint CSRF-able. Setting it
// explicitly closes that. Lax rather than Strict keeps ordinary
// navigation into the app working.
//
// The same flags must be used when clearing the cookie, since a browser
// only replaces a cookie whose name, path and attributes all match.
func (ac *AuthController) setSessionCookie(c *gin.Context, token string, maxAge int) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("token", token, maxAge, "/", "", ac.secureCookies, true)
}

// emailRegex is a simple regex pattern for validating email format.
// It checks for the basic structure: something@something.something
var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

// sessionTTL is how long a session token is valid, and therefore how
// long a revocation record for it has to survive. The two must agree: a
// revocation that expires first would let a still-valid token back in.
const sessionTTL = 24 * time.Hour

// Password bounds.
//
// The minimum is a policy choice: six characters is inside the reach of
// an offline dictionary attack, and eight is the floor most guidance now
// starts from.
//
// The maximum is not a policy choice at all — bcrypt refuses any input
// over 72 bytes, and the old code passed the password straight through,
// so a long passphrase came back as a 500 with no explanation. It is a
// byte count, not a character count: a password of accented or CJK
// characters hits it in far fewer than 72 keystrokes.
const (
	minPasswordLength = 8
	maxPasswordBytes  = 72
)

// validateRegistration checks a registration payload and returns one
// message per problem, so the caller sees every fix at once rather than
// discovering them one submission at a time.
//
// It is separate from the handler because it must run before anything
// touches bcrypt or the database.
func validateRegistration(input models.RegisterInput) []string {
	var errs []string

	// 1. Required fields
	if strings.TrimSpace(input.FullName) == "" {
		errs = append(errs, "Full name is required")
	}
	if strings.TrimSpace(input.Username) == "" {
		errs = append(errs, "Username is required")
	}
	if strings.TrimSpace(input.Email) == "" {
		errs = append(errs, "Email is required")
	}
	if strings.TrimSpace(input.Password) == "" {
		errs = append(errs, "Password is required")
	}
	if len(errs) > 0 {
		// Format checks on empty values would only add noise.
		return errs
	}

	// 2. Format and length rules
	if !emailRegex.MatchString(input.Email) {
		errs = append(errs, "Email format is invalid")
	}
	if len(input.Username) < 3 {
		errs = append(errs, "Username must be at least 3 characters")
	}
	if len(input.Password) < minPasswordLength {
		errs = append(errs, fmt.Sprintf("Password must be at least %d characters", minPasswordLength))
	}
	if len(input.Password) > maxPasswordBytes {
		errs = append(errs, fmt.Sprintf(
			"Password must be at most %d bytes — note that accented and non-Latin characters count as more than one byte each",
			maxPasswordBytes))
	}

	return errs
}

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

	// 2. Check every field before touching bcrypt or the database:
	//    required values, email format, username length, and the
	//    password bounds. The upper bound is load-bearing — bcrypt
	//    rejects anything over 72 bytes, and letting that reach the
	//    hasher turned a long passphrase into a 500.
	if validationErrors := validateRegistration(input); len(validationErrors) > 0 {
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
		// A collision on either unique field answers identically.
		//
		// Naming the field would turn this endpoint into an account
		// lookup: submit a throwaway username with a target's email and
		// the wording tells you whether that person has an account. One
		// message for both, and no echo of the submitted value, means a
		// probe learns only "one of these two is taken" — which it
		// already had to guess between.
		//
		// This narrows the oracle, it does not close it: registration
		// that confirms nothing at all requires email-confirmation
		// signup, which this product does not have yet. Until then the
		// remaining exposure is bounded by the per-address limit on this
		// route, which now fails closed. See the report to the owner.
		//
		// The wording still tells a genuine user what to do — pick
		// different details — which is the UX this endpoint exists for.
		if strings.Contains(err.Error(), "already exists") {
			c.JSON(http.StatusConflict, gin.H{
				"success": false,
				"message": "Those account details are already in use",
				"errors":  []string{"An account already exists with that email or username. Try different details, or sign in instead."},
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
		// Hash against a dummy before answering. The response text is
		// already identical for both failures, but returning here without
		// hashing would make a missing account answer ~100x faster than a
		// wrong password, which is a usable account-enumeration oracle.
		models.BurnPasswordComparison(input.Password)

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
	ac.setSessionCookie(c, token, 86400)

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

	// 1. Revoke the session this request presented.
	//
	//    Clearing the cookie only asks the browser to forget the token.
	//    Anyone who copied it — from a shared machine, a proxy log, a
	//    stolen backup — could keep using it until it expired. Recording
	//    the revocation is what actually ends the session.
	//
	//    The record expires with the token, so this cannot grow without
	//    bound: a revocation for a token that has already expired
	//    protects nothing.
	if ac.sessions != nil {
		if sid, ok := c.Get("sessionID"); ok {
			if s, _ := sid.(string); s != "" {
				expiry := time.Now().Add(sessionTTL)
				if err := ac.sessions.RevokeSession(c.Request.Context(), s, expiry); err != nil {
					// The cookie is still cleared, so the ordinary case
					// still logs out. The token itself is never logged.
					log.Printf("auth: could not revoke session on logout: %v", err)
				}
			}
		}
	}

	// 2. Clear the JWT cookie by setting MaxAge to -1
	//    This tells the browser to delete the cookie immediately
	ac.setSessionCookie(c, "", -1)

	// 3. Send a success response
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
	// Every token carries its own session id. Without one, a token can
	// only be distinguished by its bytes, so "end this session" has
	// nothing to name — which is why logout used to clear the cookie and
	// leave the credential itself valid for the rest of its 24 hours.
	sessionID, err := session.NewSessionID()
	if err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}

	claims := jwt.MapClaims{
		"sub":      user.ID.Hex(),                     // Subject — the user's MongoDB ObjectID
		"username": user.Username,                     // Username for quick access without DB lookup
		"role":     user.Role,                         // Role for authorization checks
		"jti":      sessionID,                         // Session id — what revocation names
		"exp":      time.Now().Add(sessionTTL).Unix(), // Expires in 24 hours
		"iat":      time.Now().Unix(),                 // Issued at
	}

	// Create and sign the token with the JWT secret
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString([]byte(ac.jwtSecret))
	if err != nil {
		return "", fmt.Errorf("failed to sign JWT: %w", err)
	}

	return signedToken, nil
}
