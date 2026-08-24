// Package middleware provides HTTP middleware functions for the
// Gin router. Middleware runs before the route handler and can
// modify the request context, reject unauthorized requests, or
// add logging/metrics.
package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// AuthMiddleware returns a Gin middleware function that validates
// the JWT token stored in the HTTP-only "token" cookie.
//
// If the cookie is missing or the token is invalid/expired, the
// middleware rejects the request with a 401 Unauthorized response.
//
// If the token is valid, the middleware extracts the user claims
// (userID, username, role) and sets them on the Gin context so
// downstream handlers can access them via c.Get("userID"), etc.
//
// The token itself is never logged or echoed back. It is a bearer
// credential, so a copy of it in a log line is a copy of the account.
func AuthMiddleware(jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Steps to follow while validating the auth token
		// =================================================

		// 1. Read the "token" cookie from the request
		tokenString, err := c.Cookie("token")
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Authentication required — no token provided",
			})
			c.Abort()
			return
		}

		// 2. Verify the signature and decode the claims. The parser pins
		//    the algorithm, requires an expiry, and decodes into typed
		//    fields so no claim needs an unchecked type assertion — see
		//    parseSessionToken for why each of those matters.
		claims, err := parseSessionToken(tokenString, jwtSecret)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Invalid or expired token",
			})
			c.Abort()
			return
		}

		// 3. Set user information on the Gin context for downstream handlers
		//    Controllers can access these with c.Get("userID"), c.Get("username"), etc.
		c.Set("userID", claims.UserID())
		c.Set("username", claims.Username)
		c.Set("role", claims.Role)

		// 4. Continue to the next handler in the chain
		c.Next()
	}
}
