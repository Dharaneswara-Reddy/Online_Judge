package playground

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/toji339/online-judge/internal/judge"
	"github.com/toji339/online-judge/internal/queue"
)

// fakeCaller stands in for the broker.
type fakeCaller struct {
	gotPayload  []byte
	reply       []byte
	err         error
	hadDeadline bool
}

func (f *fakeCaller) Call(ctx context.Context, payload []byte) ([]byte, error) {
	f.gotPayload = payload
	_, f.hadDeadline = ctx.Deadline()
	return f.reply, f.err
}

func TestRemoteRunnerRoundTripsARequest(t *testing.T) {
	reply, _ := json.Marshal(Response{Stdout: "olleh", RuntimeMS: 9})
	caller := &fakeCaller{reply: reply}

	got, err := NewRemoteRunner(caller).Run(context.Background(), Request{
		Mode: ModeRaw, Language: "python", Code: "print(input()[::-1])", Stdin: "hello",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got.Stdout != "olleh" {
		t.Errorf("stdout = %q, want %q", got.Stdout, "olleh")
	}

	var sent Request
	if err := json.Unmarshal(caller.gotPayload, &sent); err != nil {
		t.Fatalf("payload was not valid JSON: %v", err)
	}
	if sent.Stdin != "hello" || sent.Language != "python" {
		t.Errorf("request lost fields in transit: %+v", sent)
	}
}

// The caller must never wait forever on a worker that may not exist.
func TestRemoteRunnerAlwaysBoundsTheWait(t *testing.T) {
	reply, _ := json.Marshal(Response{})
	caller := &fakeCaller{reply: reply}

	if _, err := NewRemoteRunner(caller).Run(context.Background(), Request{
		Mode: ModeRaw, Language: "python", Code: "x",
	}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !caller.hadDeadline {
		t.Error("Call was made without a deadline")
	}
}

func TestRemoteRunnerSurfacesNoWorker(t *testing.T) {
	caller := &fakeCaller{err: queue.ErrNoWorker}

	_, err := NewRemoteRunner(caller).Run(context.Background(), Request{
		Mode: ModeRaw, Language: "python", Code: "x",
	})
	if !errors.Is(err, queue.ErrNoWorker) {
		t.Fatalf("error = %v, want ErrNoWorker", err)
	}
}

func TestRemoteRunnerRejectsOversizedCodeWithoutCallingTheBroker(t *testing.T) {
	caller := &fakeCaller{}

	_, err := NewRemoteRunner(caller).Run(context.Background(), Request{
		Mode: ModeRaw, Language: "python", Code: strings.Repeat("x", MaxCodeBytes+1),
	})
	if err == nil {
		t.Fatal("expected oversized code to be rejected")
	}
	if caller.gotPayload != nil {
		t.Error("oversized code was published to the broker anyway")
	}
}

func TestHandlerRunsTheRequestAndEncodesTheReply(t *testing.T) {
	sb := &fakeSandbox{run: judge.ExecuteResult{Stdout: "olleh"}}
	h := Handler(NewLocalRunner(sb))

	req, _ := json.Marshal(Request{Mode: ModeRaw, Language: "python", Code: "x", Stdin: "hello"})
	body, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	var resp Response
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("reply was not valid JSON: %v", err)
	}
	if resp.Stdout != "olleh" {
		t.Errorf("stdout = %q, want %q", resp.Stdout, "olleh")
	}
	if sb.gotStdin != "hello" {
		t.Errorf("stdin = %q, want it carried across the broker", sb.gotStdin)
	}
}

func TestHandlerRejectsMalformedPayload(t *testing.T) {
	h := Handler(NewLocalRunner(&fakeSandbox{}))

	if _, err := h(context.Background(), []byte("{not json")); err == nil {
		t.Fatal("expected malformed payload to be rejected")
	}
}

// End-to-end through the transport shape: a request encoded by the
// RemoteRunner must be decodable by the worker's handler.
func TestRemoteRequestIsUnderstoodByTheWorkerHandler(t *testing.T) {
	sb := &fakeSandbox{run: judge.ExecuteResult{Stdout: "ok"}}
	worker := Handler(NewLocalRunner(sb))

	caller := &fakeCaller{}
	caller.reply = []byte("{}")
	_, _ = NewRemoteRunner(caller).Run(context.Background(), Request{
		Mode: ModeRaw, Language: "python", Code: "print(1)", Stdin: "in",
		TimeLimitMs: 5000, MemoryLimitMB: 128,
	})

	if _, err := worker(context.Background(), caller.gotPayload); err != nil {
		t.Fatalf("worker could not handle what the API published: %v", err)
	}
	if sb.gotStdin != "in" {
		t.Errorf("stdin = %q, want %q", sb.gotStdin, "in")
	}
	if sb.gotLimits.MemoryLimitMB != 128 {
		t.Errorf("memory limit = %d, want 128 carried across", sb.gotLimits.MemoryLimitMB)
	}
}
