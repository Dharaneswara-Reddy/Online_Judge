package assist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// The only place in this package that opens a socket.
//
// Where inference runs is an architectural decision, not a deployment
// detail. The production host is a 2 vCPU, 2 GiB box with both cores
// already committed to judging under a hard sandbox ceiling; there is no
// room for a model on it, and there should not be. The same reasoning
// that keeps the Docker socket away from the API applies — inference is
// a call to something slow and outside our control, so it goes on the
// side of the boundary that already talks to slow, untrusted things.

const (
	// anthropicMessagesURL is the real endpoint. The tests never reach
	// it: newAnthropicProvider takes the endpoint as a parameter so the
	// suite can point at an httptest server.
	anthropicMessagesURL = "https://api.anthropic.com/v1/messages"

	// anthropicVersion is the API version header the Messages API
	// requires. It is a date, and pinning it is the point.
	anthropicVersion = "2023-06-01"

	// DefaultModel is used when no ASSIST_MODEL is configured.
	DefaultModel = "claude-sonnet-5"

	// providerTimeout bounds one completion. A hint that arrives after
	// half a minute has missed the moment it existed for, and an API
	// goroutine blocked on a hung upstream is worth more than the hint.
	providerTimeout = 25 * time.Second

	// maxResponseBytes caps what is read back. The reply is a few
	// hundred tokens of prose; anything at this size is a malfunction,
	// and reading it into memory would be the malfunction spreading.
	maxResponseBytes = 1 << 20
)

// anthropicProvider is a Provider backed by the Anthropic Messages API.
type anthropicProvider struct {
	apiKey   string
	model    string
	http     *http.Client
	endpoint string
}

// NewAnthropicProvider returns a Provider for the real API.
//
// A nil client is replaced with one that has a timeout, because
// http.DefaultClient has none and a provider without one turns a stalled
// upstream into a stalled API.
func NewAnthropicProvider(apiKey, model string, hc *http.Client) Provider {
	return newAnthropicProvider(apiKey, model, hc, anthropicMessagesURL)
}

// newAnthropicProvider is NewAnthropicProvider with the endpoint
// injected, so no test in this package has to reach the internet to
// check the wire format.
func newAnthropicProvider(apiKey, model string, hc *http.Client, endpoint string) *anthropicProvider {
	if model == "" {
		model = DefaultModel
	}
	if hc == nil {
		hc = &http.Client{Timeout: providerTimeout}
	}
	return &anthropicProvider{
		apiKey:   apiKey,
		model:    model,
		http:     hc,
		endpoint: endpoint,
	}
}

// messagesRequest is the wire format of one completion.
type messagesRequest struct {
	Model       string        `json:"model"`
	MaxTokens   int           `json:"max_tokens"`
	Temperature float64       `json:"temperature"`
	System      string        `json:"system,omitempty"`
	Messages    []wireMessage `json:"messages"`
}

type wireMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// messagesResponse is the part of the reply this package reads. The
// blocks carry a type because a reply may lead with something other than
// prose, and skipping those is cheaper than pinning a model's output
// shape.
type messagesResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// Complete sends one prompt and returns the first block of text.
//
// Every failure — transport, status, malformed body, no usable text —
// becomes ErrUnavailable, because they are the same event to a caller:
// there is no hint this time, and the feature should disappear rather
// than produce an error the student has to interpret.
//
// No error returned from here contains the API key. The upstream is
// entitled to echo a bad key back in its own error body, so response
// bodies are never interpolated into an error string; the status code is
// all a log needs, and a key that reaches a log is a key to rotate.
func (p *anthropicProvider) Complete(ctx context.Context, prompt Prompt) (string, error) {
	body, err := json.Marshal(messagesRequest{
		Model:       p.model,
		MaxTokens:   prompt.MaxTokens,
		Temperature: prompt.Temperature,
		System:      prompt.System,
		Messages:    []wireMessage{{Role: "user", Content: prompt.User}},
	})
	if err != nil {
		return "", fmt.Errorf("%w: encoding request", ErrUnavailable)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("%w: building request", ErrUnavailable)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := p.http.Do(req)
	if err != nil {
		// The transport error may name the URL but never the key, which
		// travels in a header.
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", fmt.Errorf("%w: reading response", ErrUnavailable)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Deliberately no body: it is the one place an echoed key could
		// reach a log line.
		return "", fmt.Errorf("%w: upstream returned %d", ErrUnavailable, resp.StatusCode)
	}

	var parsed messagesResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("%w: malformed response body", ErrUnavailable)
	}

	for _, block := range parsed.Content {
		if block.Type != "" && block.Type != "text" {
			continue
		}
		if block.Text != "" {
			return block.Text, nil
		}
	}

	return "", fmt.Errorf("%w: response carried no text", ErrUnavailable)
}
