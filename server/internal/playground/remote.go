package playground

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/toji339/online-judge/internal/queue"
)

// RemoteTimeout bounds how long the API waits for a worker to answer.
//
// It has to clear the worst legitimate case — the maximum execution
// limit plus compiling a language like Java or Go, plus container
// startup — while still failing well inside the browser's patience.
const RemoteTimeout = 45 * time.Second

// RemoteRunner satisfies Runner by asking a judge worker to do the work.
// This is what the API uses in production, where it has no Docker access
// of its own.
type RemoteRunner struct {
	caller  queue.Caller
	timeout time.Duration
}

// NewRemoteRunner builds a runner over a synchronous queue caller.
func NewRemoteRunner(caller queue.Caller) *RemoteRunner {
	return &RemoteRunner{caller: caller, timeout: RemoteTimeout}
}

// Run sends the request to a worker and waits for the reply.
//
// Validation happens here as well as on the worker. Rejecting oversized
// code before it reaches the broker saves carrying a payload that is
// certain to be refused, but the worker repeats the check because it
// must not trust the publisher.
func (r *RemoteRunner) Run(ctx context.Context, req Request) (Response, error) {
	if len(req.Code) > MaxCodeBytes {
		return Response{}, fmt.Errorf("playground: code exceeds %d bytes", MaxCodeBytes)
	}
	if len(req.TestCases) > MaxTestCases {
		return Response{}, fmt.Errorf("playground: at most %d test cases may be run at once", MaxTestCases)
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return Response{}, fmt.Errorf("playground: encode request: %w", err)
	}

	callCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	body, err := r.caller.Call(callCtx, payload)
	if err != nil {
		return Response{}, err
	}

	var resp Response
	if err := json.Unmarshal(body, &resp); err != nil {
		return Response{}, fmt.Errorf("playground: decode reply: %w", err)
	}
	return resp, nil
}

// Handler adapts a Runner into the queue's RPC handler shape, so a judge
// worker can serve playground calls. Workers pass a LocalRunner.
func Handler(runner Runner) queue.RPCHandler {
	return func(ctx context.Context, payload []byte) ([]byte, error) {
		var req Request
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("playground: decode request: %w", err)
		}

		resp, err := runner.Run(ctx, req)
		if err != nil {
			return nil, err
		}
		return json.Marshal(resp)
	}
}
