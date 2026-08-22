package middleware

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/toji339/online-judge/internal/ratelimit"
)

// failClosedRetryAfter is how long a client is asked to wait when the
// rate-limit counter itself is unavailable. Short, because the outage is
// expected to be brief and the endpoint is one a real user is actively
// waiting on.
const failClosedRetryAfter = 15 * time.Second

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
// The per-user limiter cannot protect an anonymous endpoint: there is no
// authenticated user yet, so it lets every request through. Defence for
// those endpoints therefore has to key off the caller's address.
//
// This variant degrades open: if the counter itself fails, the request
// is served. That is the right trade for a public read path, where a
// Redis blip turning into a site-wide 503 would be a worse outage than
// the traffic the limit was shaping. Use RateLimitAuthByIP instead when
// losing the limit is the actual risk.
func RateLimitByIP(limiter ratelimit.Limiter, name string, limit int, window time.Duration) gin.HandlerFunc {
	return rateLimitByIP(limiter, name, limit, window, false)
}

// RateLimitAuthByIP is RateLimitByIP that fails closed.
//
// It guards login and registration, where the limit *is* the control:
// silently allowing unlimited password guessing for the duration of a
// Redis outage hands an attacker exactly the window they need. A 503
// with Retry-After is the honest answer — we cannot say whether this
// attempt is the tenth or the ten-thousandth.
//
// "Fails closed" applies only to a counter that is configured and
// erroring. When Redis was never configured at all the API runs with
// ratelimit.AllowAll, which is not a ratelimit.FallibleLimiter and so
// never triggers this path — that deployment mode is documented and
// stays working, unthrottled.
func RateLimitAuthByIP(limiter ratelimit.Limiter, name string, limit int, window time.Duration) gin.HandlerFunc {
	return rateLimitByIP(limiter, name, limit, window, true)
}

func rateLimitByIP(limiter ratelimit.Limiter, name string, limit int, window time.Duration, failClosed bool) gin.HandlerFunc {
	// Only a limiter that talks to a store can fail; anything else is
	// either exact or an intentional no-op.
	fallible, canFail := limiter.(ratelimit.FallibleLimiter)

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
		var (
			allowed    bool
			retryAfter time.Duration
		)
		if canFail && failClosed {
			var err error
			allowed, retryAfter, err = fallible.AllowWithError(c.Request.Context(), key, limit, window)
			if err != nil {
				// 2a. The counter is broken. Refuse rather than guess.
				log.Printf("ratelimit: refusing %s for %s — counter unavailable: %v",
					name, c.ClientIP(), err)
				c.Header("Retry-After", fmt.Sprintf("%d", int(failClosedRetryAfter.Seconds())))
				c.JSON(http.StatusServiceUnavailable, gin.H{
					"success": false,
					"message": "Service temporarily unavailable. Please try again shortly.",
					"errors":  []string{"Rate limiting is unavailable, so this request cannot be served safely"},
				})
				c.Abort()
				return
			}
		} else {
			allowed, retryAfter = limiter.Allow(c.Request.Context(), key, limit, window)
		}
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
			"errors":  []string{fmt.Sprintf("Rate limit exceeded. Try again in %d seconds.", seconds)},
		})
		c.Abort()
	}
}
