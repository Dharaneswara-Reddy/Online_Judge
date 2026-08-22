package middleware

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/toji339/online-judge/internal/ratelimit"
)

// SecurityHeaders sets the response headers that cost nothing and close
// off whole classes of browser-side attack.
//
// This is a JSON API consumed by a separate SPA origin, so the headers
// that matter here are the ones about how a browser interprets a
// response, not a full content policy — the CSP that matters belongs on
// whatever serves the frontend bundle.
func SecurityHeaders(forceHTTPS bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Never let a browser second-guess our declared content type; a
		// JSON error body must not be sniffed as HTML and executed.
		c.Header("X-Content-Type-Options", "nosniff")
		// The API has no UI, so it should never be framed.
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		// Nothing here needs the powerful browser features.
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")

		// HSTS only makes sense once TLS is actually terminated in front
		// of this process; sending it over plain HTTP would lock users out
		// of a development server for a year.
		if forceHTTPS {
			c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		c.Next()
	}
}

// MaxBodySize rejects request bodies larger than the given limit.
//
// Without it every handler decodes an arbitrarily large body into memory
// before any validation runs, so even a request that will be rejected
// still costs the server its full size in allocation.
func MaxBodySize(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		c.Next()
	}
}

// RateLimitByIP throttles by client address rather than by user.
//
// The per-user limiter cannot protect login or registration: there is no
// authenticated user yet, so it lets every request through. Brute-force
// and mass-signup defence therefore has to key off the caller's address.
//
// Unlike the per-user spam limiter, this one fails closed when the
// counter is unavailable — an unavailable brute-force control is not a
// reason to allow unlimited password guessing.
func RateLimitByIP(limiter ratelimit.Limiter, name string, limit int, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Steps to follow while rate limiting by address
		// ===============================================

		// 1. Identify the caller by address.
		//
		//    c.ClientIP() is only as trustworthy as the engine's trusted
		//    proxy list. It is safe here because ApplyTrustedProxies has
		//    narrowed that list to the private ranges our reverse proxy
		//    lives in, so a forwarding header is ignored unless the peer
		//    that sent it is one of our own hops — and nginx replaces
		//    X-Forwarded-For with the address it observed rather than
		//    appending to whatever the client claimed. Remove either half
		//    and this key becomes attacker-chosen, which makes the limit
		//    below decorative.
		key := fmt.Sprintf("%s:%s", name, c.ClientIP())

		// 2. Ask the limiter whether this attempt fits in the window
		allowed, retryAfter := limiter.Allow(c.Request.Context(), key, limit, window)
		if allowed {
			c.Next()
			return
		}

		// 3. Reject, telling the client when it may try again
		seconds := int(retryAfter.Seconds()) + 1
		c.Header("Retry-After", fmt.Sprintf("%d", seconds))
		c.JSON(http.StatusTooManyRequests, gin.H{
			"success": false,
			"message": fmt.Sprintf("Too many attempts. Try again in %d seconds.", seconds),
		})
		c.Abort()
	}
}
