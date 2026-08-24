// Package middleware provides HTTP middleware functions for the
// Gin router. Middleware runs before the route handler and can
// modify the request context, reject unauthorized requests, or
// add logging/metrics.
package middleware

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/toji339/online-judge/internal/session"
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
// revocations may be nil, which means session revocation is not
// configured. Authentication then behaves exactly as it did before: the
// alternative — failing shut — would turn a missing dependency into a
// site-wide lockout, and the token is still signed, unexpired and
// algorithm-pinned regardless.
func AuthMiddleware(jwtSecret string, revocations session.Checker) gin.HandlerFunc {
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

		// 3. Reject a session that has been revoked.
		//
		//    A signature check alone can only say the token was issued by
		//    us and has not expired. It cannot say the user has since
		//    logged out or had their sessions ended, because a JWT is a
		//    bearer credential that stays valid until it expires. This is
		//    the step that makes logout and "log out everywhere" mean
		//    something, and it is why the token carries a jti.
		//
		//    A store error is treated as revoked rather than allowed: the
		//    check exists for the moment an account is known to be
		//    compromised, and failing open there would defeat it. The
		//    error itself is logged without the token.
		if revocations != nil {
			revoked, err := revocations.IsRevoked(c.Request.Context(),
				claims.SessionID(), claims.UserID(), claims.IssuedAtTime())
			if err != nil {
				log.Printf("auth: could not check session revocation: %v", err)
				c.JSON(http.StatusUnauthorized, gin.H{
					"success": false,
					"message": "Could not verify session",
				})
				c.Abort()
				return
			}
			if revoked {
				c.JSON(http.StatusUnauthorized, gin.H{
					"success": false,
					"message": "Session has ended — please sign in again",
				})
				c.Abort()
				return
			}
		}

		// 4. Set user information on the Gin context for downstream handlers
		//    Controllers can access these with c.Get("userID"), c.Get("username"), etc.
		c.Set("userID", claims.UserID())
		// The session id is what logout revokes. It is an opaque random
		// identifier, not the token, so putting it on the context does
		// not spread the credential.
		c.Set("sessionID", claims.SessionID())
		c.Set("username", claims.Username)
		c.Set("role", claims.Role)

		// 5. Continue to the next handler in the chain
		c.Next()
	}
}
