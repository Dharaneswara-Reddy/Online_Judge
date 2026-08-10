package middleware

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/toji339/online-judge/internal/ratelimit"
)

// RateLimit returns middleware that caps how often one user may call the
// routes it guards.
//
// The counter is keyed by authenticated user rather than IP address, so
// it must run after AuthMiddleware. name separates one route group's
// budget from another's, letting posting a comment and tagging a company
// have independent limits.
func RateLimit(limiter ratelimit.Limiter, name string, limit int, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Steps to follow while rate limiting a request
		// ===============================================

		// 1. Identify the caller. Without a user we have nothing stable to
		//    count against, so let the auth middleware reject it instead.
		userID := c.GetString("userID")
		if userID == "" {
			c.Next()
			return
		}

		// 2. Ask the limiter whether this action fits in the window
		allowed, retryAfter := limiter.Allow(c.Request.Context(), fmt.Sprintf("%s:%s", name, userID), limit, window)
		if allowed {
			c.Next()
			return
		}

		// 3. Reject with the standard header telling the client when to retry
		seconds := int(retryAfter.Seconds()) + 1
		c.Header("Retry-After", fmt.Sprintf("%d", seconds))
		c.JSON(http.StatusTooManyRequests, gin.H{
			"success": false,
			"message": fmt.Sprintf("You're doing that too often. Try again in %d seconds.", seconds),
		})
		c.Abort()
	}
}
