package assist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// The second provider, speaking the OpenAI chat-completions dialect.
//
// It exists because the assistant runs on Groq's free tier rather than a
// metered API: this is a personal project on a small box, and a feature
// that bills per hint is a feature that gets switched off. Groq serves
// open-weight models over an OpenAI-compatible endpoint, and so do most
// of the alternatives worth keeping open — Together, OpenRouter, a local
// llama.cpp or vLLM server — so the endpoint is a parameter and Groq is
// simply the default one.
//
// Two consequences follow from running a smaller open-weight model, and
// both are already handled elsewhere in this package rather than here:
//
//   - Instruction-following is weaker. A frontier model asked not to
//     emit code almost always complies; a 70B open model complies less
//     often. That does not change the design, because the design never
//     relied on compliance — RejectCode is the control and the prompt
//     is only the request. It does mean the filter has stopped being
//     defensive and become load-bearing.
//   - Replies are chattier and more likely to be wrapped in preamble.
//     That is the caller's problem, not the transport's; nothing is
//     stripped here beyond whitespace.

const (
	// groqCompletionsURL is Groq's OpenAI-compatible endpoint.
	groqCompletionsURL = "https://api.groq.com/openai/v1/chat/completions"

	// DefaultGroqModel is used when no ASSIST_MODEL is configured.
	//
	// The constant matters less than the setting: identifiers on a free
	// tier are retired on a schedule, and this one has already been
	// changed once — llama-3.3-70b-versatile was the default until Groq
	// announced its shutdown. Prefer changing ASSIST_MODEL to editing
	// this line, and expect to change it again.
	//
	// gpt-oss-120b is the current choice because Groq lists it as a
	// production model and recommends it for complex coding work, which
	// is the shape of every prompt this package builds.
	DefaultGroqModel = "openai/gpt-oss-120b"
)

// openAICompatProvider is a Provider for any host speaking the OpenAI
// chat-completions API.
type openAICompatProvider struct {
	apiKey   string
	model    string
	http     *http.Client
	endpoint string
}

// NewGroqProvider returns a Provider backed by Groq.
func NewGroqProvider(apiKey, model string, hc *http.Client) Provider {
	if model == "" {
		model = DefaultGroqModel
	}
	return newOpenAICompatProvider(groqCompletionsURL, apiKey, model, hc)
}

// NewOpenAICompatProvider returns a Provider for an arbitrary
// OpenAI-compatible endpoint, which is what makes a self-hosted or
// third-party model a configuration change rather than a code change.
func NewOpenAICompatProvider(endpoint, apiKey, model string, hc *http.Client) Provider {
	if endpoint == "" {
		endpoint = groqCompletionsURL
	}
	if model == "" {
		model = DefaultGroqModel
	}
	return newOpenAICompatProvider(endpoint, apiKey, model, hc)
}

// newOpenAICompatProvider is the unexported constructor the tests use,
// so no test in this package has to reach the internet.
func newOpenAICompatProvider(endpoint, apiKey, model string, hc *http.Client) *openAICompatProvider {
	if hc == nil {
		hc = &http.Client{Timeout: providerTimeout}
	}
	return &openAICompatProvider{
		apiKey:   apiKey,
		model:    model,
		http:     hc,
		endpoint: endpoint,
	}
}

// chatRequest is the wire format. The system prompt is a message with a
// role here, not a top-level field as it is in the Anthropic dialect —
// getting that wrong drops every instruction in it without any error.
type chatRequest struct {
	Model       string        `json:"model"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature"`
	Messages    []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatResponse is the part of the reply this package reads. Message is a
// pointer because a choice can carry a null message — a refusal, or a
// gateway padding the list — and a value type would read that as an
// empty string and report the wrong failure.
type chatResponse struct {
	Choices []struct {
		Message *struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Complete sends one prompt and returns the first non-empty message.
//
// Failure handling matches the Anthropic provider exactly, and for the
// same reasons: everything becomes ErrUnavailable because they are the
// same event to a caller, and no error string may carry the key, since
// an upstream is entitled to echo a bad one back in its own error body.
func (p *openAICompatProvider) Complete(ctx context.Context, prompt Prompt) (string, error) {
	messages := make([]chatMessage, 0, 2)
	if strings.TrimSpace(prompt.System) != "" {
		messages = append(messages, chatMessage{Role: "system", Content: prompt.System})
	}
	messages = append(messages, chatMessage{Role: "user", Content: prompt.User})

	body, err := json.Marshal(chatRequest{
		Model:       p.model,
		MaxTokens:   prompt.MaxTokens,
		Temperature: prompt.Temperature,
		Messages:    messages,
	})
	if err != nil {
		return "", fmt.Errorf("%w: encoding request", ErrUnavailable)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("%w: building request", ErrUnavailable)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

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

	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("%w: malformed response body", ErrUnavailable)
	}

	for _, choice := range parsed.Choices {
		if choice.Message != nil && choice.Message.Content != "" {
			return choice.Message.Content, nil
		}
	}

	return "", fmt.Errorf("%w: response carried no text", ErrUnavailable)
}
