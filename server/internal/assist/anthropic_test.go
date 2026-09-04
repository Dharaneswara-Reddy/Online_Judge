package assist

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testKey = "sk-ant-THIS-MUST-NEVER-BE-LOGGED"

func testPrompt() Prompt {
	return Prompt{
		System:      "You are a tutor.",
		User:        "Give me a nudge.",
		MaxTokens:   256,
		Temperature: 0.2,
	}
}

// TestAnthropicProviderSendsTheDocumentedRequest pins the wire format,
// because getting it wrong shows up only as a 400 from a service the
// test suite is never allowed to call.
func TestAnthropicProviderSendsTheDocumentedRequest(t *testing.T) {
	var (
		gotMethod  string
		gotPath    string
		gotHeaders http.Header
		gotBody    map[string]any
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotHeaders = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"Scan once and keep a running minimum."}]}`)
	}))
	defer srv.Close()

	p := newAnthropicProvider(testKey, "claude-sonnet-5", srv.Client(), srv.URL+"/v1/messages")

	got, err := p.Complete(context.Background(), testPrompt())
	if err != nil {
		t.Fatalf("Complete = %v", err)
	}
	if got != "Scan once and keep a running minimum." {
		t.Fatalf("text = %q", got)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/v1/messages" {
		t.Errorf("path = %s, want /v1/messages", gotPath)
	}
	if gotHeaders.Get("x-api-key") != testKey {
		t.Errorf("x-api-key header = %q", gotHeaders.Get("x-api-key"))
	}
	if gotHeaders.Get("anthropic-version") != anthropicVersion {
		t.Errorf("anthropic-version = %q, want %q", gotHeaders.Get("anthropic-version"), anthropicVersion)
	}
	if ct := gotHeaders.Get("content-type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q", ct)
	}

	if gotBody["model"] != "claude-sonnet-5" {
		t.Errorf("model = %v", gotBody["model"])
	}
	if gotBody["system"] != "You are a tutor." {
		t.Errorf("system = %v", gotBody["system"])
	}
	if gotBody["max_tokens"] != float64(256) {
		t.Errorf("max_tokens = %v", gotBody["max_tokens"])
	}
	msgs, ok := gotBody["messages"].([]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("messages = %v", gotBody["messages"])
	}
	msg := msgs[0].(map[string]any)
	if msg["role"] != "user" || msg["content"] != "Give me a nudge." {
		t.Errorf("message = %v", msg)
	}
}

func TestAnthropicProviderDefaultsTheModel(t *testing.T) {
	var gotModel any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		gotModel = body["model"]
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"ok"}]}`)
	}))
	defer srv.Close()

	p := newAnthropicProvider(testKey, "", srv.Client(), srv.URL)
	if _, err := p.Complete(context.Background(), testPrompt()); err != nil {
		t.Fatalf("Complete = %v", err)
	}
	if gotModel != DefaultModel {
		t.Fatalf("model = %v, want the package default %q", gotModel, DefaultModel)
	}
}

// TestAnthropicProviderNeverLeaksTheKey is a hard project rule: an API
// key that reaches a log line or an error string is a key that has to be
// rotated.
func TestAnthropicProviderNeverLeaksTheKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		// The real API echoes nothing sensitive, but assume the worst.
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key `+testKey+`"}}`)
	}))
	defer srv.Close()

	p := newAnthropicProvider(testKey, "m", srv.Client(), srv.URL)
	_, err := p.Complete(context.Background(), testPrompt())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Complete = %v, want ErrUnavailable", err)
	}
	if strings.Contains(err.Error(), testKey) {
		t.Fatalf("the API key appears in the error string: %v", err)
	}
}

func TestAnthropicProviderMapsFailuresToUnavailable(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		close   bool // shut the server down before the call
		wantErr error
	}{
		{name: "server error", status: 500, body: `{"error":"boom"}`, wantErr: ErrUnavailable},
		{name: "rate limited", status: 429, body: `{}`, wantErr: ErrUnavailable},
		{name: "malformed json", status: 200, body: `{"content":[{`, wantErr: ErrUnavailable},
		{name: "no content blocks", status: 200, body: `{"content":[]}`, wantErr: ErrUnavailable},
		{name: "empty text", status: 200, body: `{"content":[{"type":"text","text":""}]}`, wantErr: ErrUnavailable},
		{name: "transport failure", close: true, wantErr: ErrUnavailable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				_, _ = io.WriteString(w, tc.body)
			}))
			client := srv.Client()
			url := srv.URL
			if tc.close {
				srv.Close()
			} else {
				defer srv.Close()
			}

			p := newAnthropicProvider(testKey, "m", client, url)
			if _, err := p.Complete(context.Background(), testPrompt()); !errors.Is(err, tc.wantErr) {
				t.Fatalf("Complete = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestAnthropicProviderSkipsNonTextBlocks keeps the reader working when
// the reply leads with something other than prose.
func TestAnthropicProviderSkipsNonTextBlocks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"the answer"}]}`)
	}))
	defer srv.Close()

	p := newAnthropicProvider(testKey, "m", srv.Client(), srv.URL)
	got, err := p.Complete(context.Background(), testPrompt())
	if err != nil {
		t.Fatalf("Complete = %v", err)
	}
	if got != "the answer" {
		t.Fatalf("text = %q", got)
	}
}

// TestNewAnthropicProviderSuppliesATimeout: a hung upstream must not
// pin an API goroutine and its request forever.
func TestNewAnthropicProviderSuppliesATimeout(t *testing.T) {
	p, ok := NewAnthropicProvider(testKey, "m", nil).(*anthropicProvider)
	if !ok {
		t.Fatal("NewAnthropicProvider returned an unexpected concrete type")
	}
	if p.http == nil {
		t.Fatal("nil http.Client was not replaced")
	}
	if p.http.Timeout <= 0 {
		t.Fatal("the substituted client has no timeout")
	}
	if p.endpoint != anthropicMessagesURL {
		t.Fatalf("endpoint = %q, want %q", p.endpoint, anthropicMessagesURL)
	}
}

func TestAnthropicProviderHonoursContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := newAnthropicProvider(testKey, "m", srv.Client(), srv.URL)
	if _, err := p.Complete(ctx, testPrompt()); err == nil {
		t.Fatal("Complete on a cancelled context succeeded")
	}
}
