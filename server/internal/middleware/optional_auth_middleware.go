package middleware

import (
	"github.com/gin-gonic/gin"
)

// OptionalAuth identifies the caller when they are signed in, and lets
// the request through untouched when they are not.
//
// It exists for endpoints that are public but render differently for a
// known user — a discussion thread showing which comments you upvoted,
// or a company tag widget that stops asking a question you answered.
// Anything that must reject anonymous callers uses AuthMiddleware
// instead; this one never rejects anybody.
//
// "Never rejects" is not the same as "believes anything". It verifies
// the token exactly as strictly as AuthMiddleware does — same pinned
// algorithm, same mandatory expiry, same typed claims — and simply
// treats a failure as anonymous rather than as an error. A token this
// middleware accepted wrongly would let an attacker render a page as
// somebody else.
func OptionalAuth(jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Steps to follow while optionally identifying a caller
		// =======================================================

		// 1. No cookie simply means an anonymous reader
		tokenString, err := c.Cookie("token")
		if err != nil {
			c.Next()
			return
		}

		// 2. Verify the token. An invalid one is treated as anonymous
		//    rather than an error, since this route does not need auth.
		claims, err := parseSessionToken(tokenString, jwtSecret)
		if err != nil {
			c.Next()
			return
		}

		// 3. Publish the identity for the handler to use
		c.Set("userID", claims.UserID())
		c.Set("username", claims.Username)
		c.Set("role", claims.Role)

		c.Next()
	}
}
