package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// AdminOnly returns a Gin middleware that checks the authenticated
// user's role is "admin". It must be placed after AuthMiddleware
// in the handler chain so that c.Get("role") is already set.
func AdminOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		// The assertion is checked rather than bare: a role that is not
		// a string is a bug somewhere upstream, and the safe reading of
		// "I cannot tell what this is" is "not an admin" — not a panic.
		role, exists := c.Get("role")
		if roleStr, ok := role.(string); !exists || !ok || roleStr != "admin" {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "Admin access required",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}
