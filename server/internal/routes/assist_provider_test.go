package routes

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/toji339/online-judge/internal/assist"
	"github.com/toji339/online-judge/internal/config"
)

// Which provider a deployment gets is a cost decision, not a detail.
// Groq's free tier is what lets this feature stay switched on, so a box
// that has both credentials must not quietly start billing the Anthropic
// one — and the two speak different dialects, so picking the wrong
// branch does not fail loudly either. It sends the system prompt in a
// field the other API ignores, and every instruction in it disappears
// while the endpoint keeps answering 200.

func assistConfig() *config.Config {
	return &config.Config{AssistEnabled: true}
}

// TestAssistProviderIsAbsentWithoutACredential pins the optional
// dependency rule at the wiring level.
func TestAssistProviderIsAbsentWithoutACredential(t *testing.T) {
	if p := newAssistProvider(assistConfig()); p != nil {
		t.Fatalf("provider = %v with no key set, want nil", p)
	}
}

func TestAssistProviderRespectsTheKillSwitch(t *testing.T) {
	cfg := assistConfig()
	cfg.AssistEnabled = false
	cfg.GroqAPIKey = "present-but-ignored"

	if p := newAssistProvider(cfg); p != nil {
		t.Fatalf("provider = %v with ASSIST_ENABLED off, want nil", p)
	}
}

// TestAssistProviderSpeaksTheGroqDialect drives the configured provider
// against a local server and reads the wire. A Bearer header and a
// system message prove the OpenAI dialect; the Anthropic one would send
// x-api-key and a top-level system field.
func TestAssistProviderSpeaksTheGroqDialect(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()

	cfg := assistConfig()
	cfg.GroqAPIKey = "groq-key"
	cfg.AssistBaseURL = srv.URL

	provider := newAssistProvider(cfg)
	if provider == nil {
		t.Fatal("provider = nil with a Groq key set")
	}
	if _, err := provider.Complete(context.Background(), assist.Prompt{
		System: "You are a tutor.", User: "hi", MaxTokens: 16,
	}); err != nil {
		t.Fatalf("Complete = %v", err)
	}

	if gotAuth != "Bearer groq-key" {
		t.Fatalf("Authorization = %q, want the Groq key as a bearer token", gotAuth)
	}
}

// TestGroqWinsWhenBothCredentialsArePresent is the cost guard. It
// compares concrete types rather than making a request, so a regression
// here fails instead of calling a metered API from the test suite.
func TestGroqWinsWhenBothCredentialsArePresent(t *testing.T) {
	groqOnly := assistConfig()
	groqOnly.GroqAPIKey = "groq-key"

	anthropicOnly := assistConfig()
	anthropicOnly.AnthropicAPIKey = "anthropic-key"

	both := assistConfig()
	both.GroqAPIKey = "groq-key"
	both.AnthropicAPIKey = "anthropic-key"

	groqType := reflect.TypeOf(newAssistProvider(groqOnly))
	anthropicType := reflect.TypeOf(newAssistProvider(anthropicOnly))
	bothType := reflect.TypeOf(newAssistProvider(both))

	if groqType == anthropicType {
		t.Fatal("the two providers share a concrete type, so this test proves nothing")
	}
	if bothType != groqType {
		t.Fatalf("with both keys set the provider is %v, want the Groq one (%v)", bothType, groqType)
	}
}

// TestAssistProviderFallsBackToAnthropic keeps the second provider a
// real option rather than dead code.
func TestAssistProviderFallsBackToAnthropic(t *testing.T) {
	cfg := assistConfig()
	cfg.AnthropicAPIKey = "anthropic-key"

	if p := newAssistProvider(cfg); p == nil {
		t.Fatal("provider = nil with only an Anthropic key set")
	}
}
