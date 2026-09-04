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

const compatKey = "gsk-THIS-MUST-NEVER-BE-LOGGED"

// TestGroqProviderSendsTheChatCompletionsShape pins the wire format.
// Groq speaks the OpenAI chat-completions dialect rather than the
// Anthropic messages one, and the difference is not cosmetic: the
// system prompt is a message with a role rather than a top-level field,
// and getting that wrong silently drops every instruction in it.
func TestGroqProviderSendsTheChatCompletionsShape(t *testing.T) {
	var (
		gotMethod  string
		gotHeaders http.Header
		gotBody    map[string]any
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotHeaders = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"Scan once and keep a running minimum."},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	p := newOpenAICompatProvider(srv.URL, compatKey, "llama-3.3-70b-versatile", srv.Client())

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
	// Bearer, not x-api-key. This is the one header that differs.
	if want := "Bearer " + compatKey; gotHeaders.Get("Authorization") != want {
		t.Errorf("Authorization = %q", gotHeaders.Get("Authorization"))
	}
	if ct := gotHeaders.Get("content-type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q", ct)
	}

	if gotBody["model"] != "llama-3.3-70b-versatile" {
		t.Errorf("model = %v", gotBody["model"])
	}
	if gotBody["max_tokens"] != float64(256) {
		t.Errorf("max_tokens = %v", gotBody["max_tokens"])
	}

	msgs, ok := gotBody["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("messages = %v, want a system and a user message", gotBody["messages"])
	}
	system := msgs[0].(map[string]any)
	if system["role"] != "system" || system["content"] != "You are a tutor." {
		t.Errorf("system message = %v", system)
	}
	user := msgs[1].(map[string]any)
	if user["role"] != "user" || user["content"] != "Give me a nudge." {
		t.Errorf("user message = %v", user)
	}
}

// TestOpenAICompatProviderOmitsAnEmptySystemMessage: a message with no
// content is a 400 from some hosts and noise on the rest.
func TestOpenAICompatProviderOmitsAnEmptySystemMessage(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()

	p := newOpenAICompatProvider(srv.URL, compatKey, "m", srv.Client())
	if _, err := p.Complete(context.Background(), Prompt{User: "hi", MaxTokens: 10}); err != nil {
		t.Fatalf("Complete = %v", err)
	}

	msgs := gotBody["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %v, want only the user message", msgs)
	}
	if msgs[0].(map[string]any)["role"] != "user" {
		t.Fatalf("the surviving message is not the user's: %v", msgs[0])
	}
}

func TestGroqProviderDefaultsTheModelAndEndpoint(t *testing.T) {
	p, ok := NewGroqProvider(compatKey, "", nil).(*openAICompatProvider)
	if !ok {
		t.Fatal("NewGroqProvider returned an unexpected concrete type")
	}
	if p.model != DefaultGroqModel {
		t.Errorf("model = %q, want %q", p.model, DefaultGroqModel)
	}
	if p.endpoint != groqCompletionsURL {
		t.Errorf("endpoint = %q, want %q", p.endpoint, groqCompletionsURL)
	}
	if p.http == nil || p.http.Timeout <= 0 {
		t.Fatal("the substituted client has no timeout")
	}
}

// TestOpenAICompatProviderNeverLeaksTheKey is the same hard rule the
// Anthropic provider is held to: a key that reaches a log line is a key
// that has to be rotated.
func TestOpenAICompatProviderNeverLeaksTheKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"Invalid API Key: `+compatKey+`"}}`)
	}))
	defer srv.Close()

	p := newOpenAICompatProvider(srv.URL, compatKey, "m", srv.Client())
	_, err := p.Complete(context.Background(), testPrompt())

	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Complete = %v, want ErrUnavailable", err)
	}
	if strings.Contains(err.Error(), compatKey) {
		t.Fatalf("the API key appears in the error string: %v", err)
	}
}

func TestOpenAICompatProviderMapsFailuresToUnavailable(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		close  bool
	}{
		{name: "server error", status: 500, body: `{"error":"boom"}`},
		{name: "rate limited", status: 429, body: `{}`},
		{name: "malformed json", status: 200, body: `{"choices":[{`},
		{name: "no choices", status: 200, body: `{"choices":[]}`},
		{name: "empty content", status: 200, body: `{"choices":[{"message":{"content":""}}]}`},
		{name: "null message", status: 200, body: `{"choices":[{"message":null}]}`},
		{name: "transport failure", close: true},
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

			p := newOpenAICompatProvider(url, compatKey, "m", client)
			if _, err := p.Complete(context.Background(), testPrompt()); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("Complete = %v, want ErrUnavailable", err)
			}
		})
	}
}

// TestOpenAICompatProviderSkipsAnEmptyLeadingChoice keeps the reader
// working when a host pads the list, which some OpenAI-compatible
// gateways do.
func TestOpenAICompatProviderSkipsAnEmptyLeadingChoice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":""}},{"message":{"content":"the answer"}}]}`)
	}))
	defer srv.Close()

	p := newOpenAICompatProvider(srv.URL, compatKey, "m", srv.Client())
	got, err := p.Complete(context.Background(), testPrompt())
	if err != nil {
		t.Fatalf("Complete = %v", err)
	}
	if got != "the answer" {
		t.Fatalf("text = %q", got)
	}
}

func TestOpenAICompatProviderHonoursContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := newOpenAICompatProvider(srv.URL, compatKey, "m", srv.Client())
	if _, err := p.Complete(ctx, testPrompt()); err == nil {
		t.Fatal("Complete on a cancelled context succeeded")
	}
}
