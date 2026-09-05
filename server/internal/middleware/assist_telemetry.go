package middleware

import (
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Assist requests are the only ones in this API that cost money and can
// fail for reasons the caller cannot see, so they get a record of their
// own. It exists to answer questions that the HTTP status alone cannot:
// how often the output filter withheld a generation, how much of the
// traffic the cache absorbed, and how long a real model actually takes.
//
// One line per request, key=value, so it can be grepped and parsed
// without a log pipeline.
//
// What is deliberately absent is the whole point. No prompt, no hint, no
// student code, no hidden test case, no credential — nothing here is
// derived from the body of the request or the response, only from
// metadata the handler chose to publish. A record that could carry a
// hidden case would put one in a log file where it outlives every
// safeguard the rest of the feature has.

// Context keys the assist handlers use to publish safe metadata. They
// are strings rather than a typed key because gin's context is a
// string-keyed bag already.
const (
	AssistFeatureKey  = "assistFeature"  // hint | explain | review | state
	AssistProviderKey = "assistProvider" // groq | anthropic | openai-compatible
	AssistModelKey    = "assistModel"
	AssistRungKey     = "assistRung"
	AssistProblemKey  = "assistProblem"
	AssistCachedKey   = "assistCached"
	AssistOutcomeKey  = "assistOutcome" // ok | cached | filtered-code | filtered-leak | disabled | unavailable
)

// AssistTelemetry logs one structured record per assist request.
func AssistTelemetry() gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		elapsed := time.Since(started)

		var b strings.Builder
		b.WriteString("assist")
		writeField(&b, "feature", c.GetString(AssistFeatureKey))
		writeField(&b, "provider", c.GetString(AssistProviderKey))
		writeField(&b, "model", c.GetString(AssistModelKey))
		writeField(&b, "outcome", c.GetString(AssistOutcomeKey))
		writeField(&b, "problem", c.GetString(AssistProblemKey))
		if rung := c.GetInt(AssistRungKey); rung > 0 {
			b.WriteString(" rung=")
			b.WriteString(itoa(rung))
		}
		if c.GetBool(AssistCachedKey) {
			b.WriteString(" cached=true")
		}
		b.WriteString(" status=")
		b.WriteString(itoa(c.Writer.Status()))
		b.WriteString(" ms=")
		b.WriteString(itoa(int(elapsed.Milliseconds())))

		log.Println(b.String())
	}
}

// writeField appends key=value, skipping empties so a record stays
// readable, and quoting nothing: every value written here is drawn from
// a fixed vocabulary or an identifier, never from user text.
func writeField(b *strings.Builder, key, value string) {
	if value == "" {
		return
	}
	b.WriteString(" ")
	b.WriteString(key)
	b.WriteString("=")
	b.WriteString(value)
}

// itoa avoids pulling strconv in for two call sites' worth of small
// non-negative integers.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// AssistIdentity stamps the configured provider and model onto every
// assist request, so a record says what answered it without each
// handler having to be told.
func AssistIdentity(provider, model string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(AssistProviderKey, provider)
		c.Set(AssistModelKey, model)
		c.Next()
	}
}
