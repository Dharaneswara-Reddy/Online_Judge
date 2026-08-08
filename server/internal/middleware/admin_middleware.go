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
		role, exists := c.Get("role")
		if !exists || role.(string) != "admin" {
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
