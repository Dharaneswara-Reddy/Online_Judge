package assist

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The point of these tests is one distinction: "we asked and the model
// is not there" and "we could not ask" are different facts, and a
// startup log that conflates them sends an operator to change
// ASSIST_MODEL when the real problem was a firewall.

const verifyKey = "gsk-VERIFY-MUST-NEVER-BE-LOGGED"

// modelsListServer serves an OpenAI-style model listing at
// /openai/v1/models and records what it was asked.
func modelsListServer(t *testing.T, ids []string) (*httptest.Server, *http.Request) {
	t.Helper()
	seen := &http.Request{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = *r
		if r.URL.Path != "/openai/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body := strings.Builder{}
		body.WriteString(`{"object":"list","data":[`)
		for i, id := range ids {
			if i > 0 {
				body.WriteString(",")
			}
			body.WriteString(`{"id":"` + id + `","object":"model","owned_by":"x"}`)
		}
		body.WriteString(`]}`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body.String())
	}))
	t.Cleanup(srv.Close)
	return srv, seen
}

func TestVerifyModelAcceptsAListedModel(t *testing.T) {
	srv, seen := modelsListServer(t, []string{"llama-3.3-70b-versatile", "openai/gpt-oss-120b"})

	p := newOpenAICompatProvider(srv.URL+"/openai/v1/chat/completions", verifyKey, "openai/gpt-oss-120b", srv.Client())
	if err := p.VerifyModel(context.Background()); err != nil {
		t.Fatalf("VerifyModel = %v, want nil", err)
	}

	// The listing lives beside the completions endpoint, not at it.
	if seen.URL.Path != "/openai/v1/models" {
		t.Errorf("asked %q, want /openai/v1/models", seen.URL.Path)
	}
	if seen.Method != http.MethodGet {
		t.Errorf("method = %s, want GET", seen.Method)
	}
	if want := "Bearer " + verifyKey; seen.Header.Get("Authorization") != want {
		t.Errorf("Authorization = %q", seen.Header.Get("Authorization"))
	}
}

// TestVerifyModelNamesTheAlternativesWhenAbsent is what makes the
// startup warning actionable: a log that only says "not available" tells
// an operator to go and read someone else's API docs.
func TestVerifyModelNamesTheAlternativesWhenAbsent(t *testing.T) {
	srv, _ := modelsListServer(t, []string{"llama-3.1-8b-instant", "whisper-large-v3"})

	p := newOpenAICompatProvider(srv.URL+"/openai/v1/chat/completions", verifyKey, "llama-3.3-70b-versatile", srv.Client())
	err := p.VerifyModel(context.Background())

	if !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("VerifyModel = %v, want ErrModelUnavailable", err)
	}
	if !strings.Contains(err.Error(), "llama-3.3-70b-versatile") {
		t.Errorf("error does not name the configured model: %v", err)
	}
	for _, id := range []string{"llama-3.1-8b-instant", "whisper-large-v3"} {
		if !strings.Contains(err.Error(), id) {
			t.Errorf("error does not list %s: %v", id, err)
		}
	}
}

func TestVerifyModelRejectsAnEmptyListing(t *testing.T) {
	srv, _ := modelsListServer(t, nil)

	p := newOpenAICompatProvider(srv.URL+"/openai/v1/chat/completions", verifyKey, "openai/gpt-oss-120b", srv.Client())
	if err := p.VerifyModel(context.Background()); !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("VerifyModel = %v, want ErrModelUnavailable", err)
	}
}

// TestVerifyModelDoesNotConflateUnreachableWithAbsent is the test this
// file exists for. A 401, a 500 or a refused dial says nothing about
// whether the model exists, and reporting it as ErrModelUnavailable
// would print a warning telling an operator to change a setting that is
// correct.
func TestVerifyModelDoesNotConflateUnreachableWithAbsent(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"unauthorised", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"message":"Invalid API Key: `+verifyKey+`"}}`)
		}},
		{"server error", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}},
		{"malformed body", func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `not json`)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()

			p := newOpenAICompatProvider(srv.URL+"/openai/v1/chat/completions", verifyKey, "openai/gpt-oss-120b", srv.Client())
			err := p.VerifyModel(context.Background())

			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("VerifyModel = %v, want ErrUnavailable", err)
			}
			if errors.Is(err, ErrModelUnavailable) {
				t.Fatalf("unreachable reported as ErrModelUnavailable: %v", err)
			}
			if strings.Contains(err.Error(), verifyKey) {
				t.Fatalf("error carried the API key: %v", err)
			}
		})
	}
}

func TestVerifyModelReportsARefusedDialAsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	client := srv.Client()
	endpoint := srv.URL + "/openai/v1/chat/completions"
	srv.Close()

	p := newOpenAICompatProvider(endpoint, verifyKey, "openai/gpt-oss-120b", client)
	err := p.VerifyModel(context.Background())

	if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("VerifyModel = %v, want ErrUnavailable and not ErrModelUnavailable", err)
	}
	if strings.Contains(err.Error(), verifyKey) {
		t.Fatalf("error carried the API key: %v", err)
	}
}

// An endpoint that is not a chat-completions URL leaves nowhere to
// derive a listing from. That is "could not check", not "not there".
func TestVerifyModelCannotCheckAnUnrecognisedEndpoint(t *testing.T) {
	p := newOpenAICompatProvider("https://example.invalid/v1/generate", verifyKey, "m", nil)
	err := p.VerifyModel(context.Background())

	if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("VerifyModel = %v, want ErrUnavailable and not ErrModelUnavailable", err)
	}
}

func TestOpenAICompatProviderSatisfiesVerifier(t *testing.T) {
	var _ Verifier = (*openAICompatProvider)(nil)

	if _, ok := Provider(newOpenAICompatProvider("", "k", "m", nil)).(Verifier); !ok {
		t.Fatal("the Groq provider must be discoverable as a Verifier through the Provider interface")
	}
}

// The Anthropic Messages API publishes no listing this package can use,
// so the provider deliberately does not claim it can check itself. A
// no-op returning nil would log "verified" about a check that never
// happened, which is worse than logging nothing.
func TestAnthropicProviderDoesNotClaimToVerify(t *testing.T) {
	if _, ok := Provider(newAnthropicProvider("k", "m", nil, "https://x")).(Verifier); ok {
		t.Fatal("the Anthropic provider must not implement Verifier")
	}
}

// TestVerifyModelNamesARejectedCredential: a 401 on the listing is still
// "could not check" — it says nothing about which models exist — but an
// operator should not have to guess between a firewall and a bad key,
// because the two have completely different fixes.
func TestVerifyModelNamesARejectedCredential(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"Invalid API Key: `+verifyKey+`"}}`)
	}))
	defer srv.Close()

	p := newOpenAICompatProvider(srv.URL+"/openai/v1/chat/completions", verifyKey, "m", srv.Client())
	err := p.VerifyModel(context.Background())

	if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("VerifyModel = %v, want ErrUnavailable and not ErrModelUnavailable", err)
	}
	if !strings.Contains(err.Error(), "credential was rejected") {
		t.Errorf("error does not say the credential was rejected: %v", err)
	}
	if !strings.Contains(err.Error(), "GROQ_API_KEY") {
		t.Errorf("error does not name the setting to check: %v", err)
	}
	if strings.Contains(err.Error(), verifyKey) {
		t.Fatalf("error carried the API key: %v", err)
	}
}
