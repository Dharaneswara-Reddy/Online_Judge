package rabbitmq

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestHandlerBudget_BoundsEveryInvocation. The responder used to hand
// the handler the worker's root context, which lives as long as the
// process — so one request could hold a concurrency slot, and the CPU
// behind it, for as long as it liked.
func TestHandlerBudget_BoundsEveryInvocation(t *testing.T) {
	assert.Equal(t, maxHandlerRuntime, handlerBudget(""),
		"a message with no expiry still gets a ceiling")
	assert.LessOrEqual(t, maxHandlerRuntime, time.Minute,
		"the ceiling must be short: it is a person waiting on a spinner")
}

// TestHandlerBudget_NeverOutlivesTheCaller. The publisher sets the
// message expiry from its own deadline, so a shorter one means the
// caller will already have given up — computing past that is waste.
func TestHandlerBudget_NeverOutlivesTheCaller(t *testing.T) {
	assert.Equal(t, 5*time.Second, handlerBudget("5000"))
}

// TestHandlerBudget_DoesNotTrustThePublisher. The expiry arrives over
// the broker from an untrusted publisher, so it may shorten the budget
// but never extend it — and nonsense must fall back to the ceiling
// rather than to zero, which would fail every request.
func TestHandlerBudget_DoesNotTrustThePublisher(t *testing.T) {
	assert.Equal(t, maxHandlerRuntime, handlerBudget("999999999"))
	assert.Equal(t, maxHandlerRuntime, handlerBudget("-1"))
	assert.Equal(t, maxHandlerRuntime, handlerBudget("0"))
	assert.Equal(t, maxHandlerRuntime, handlerBudget("not-a-number"))
}
