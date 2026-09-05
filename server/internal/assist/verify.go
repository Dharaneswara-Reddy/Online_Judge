package assist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Checking at startup that the configured model exists.
//
// Without this, a typo or a retired identifier surfaces as an opaque 502
// on the first student who asks for a hint, hours after the deploy that
// caused it. Free-tier model ids are retired on a schedule — this
// package has already had to change its default once — so the failure is
// routine rather than exotic, and it deserves to be found by the process
// that starts rather than by the person using it.
//
// The distinction the whole file turns on is between "we asked and it is
// not there" and "we could not ask". They call for opposite responses:
// the first means change ASSIST_MODEL, the second means look at the
// network or the credential. A check that reported a firewall as a bad
// model id would send an operator to edit a setting that was correct.

// ErrModelUnavailable means the provider answered, and the configured
// model was not in what it offers.
var ErrModelUnavailable = errors.New("assist: configured model is not available")

// Verifier is implemented by providers that can check their own model
// exists. It is deliberately separate from Provider: verification is a
// nicety, generation is the contract, and a provider that cannot check
// itself must still be usable.
type Verifier interface {
	VerifyModel(ctx context.Context) error
}

// completionsSuffix is the path this package posts to. The model listing
// sits beside it, which is the only structural assumption made about an
// OpenAI-compatible host.
const completionsSuffix = "/chat/completions"

// VerifyModel reports whether the configured model appears in the
// provider's model listing.
//
// The listing URL is derived from the completions endpoint rather than
// configured separately, so there is no second setting to get wrong. An
// endpoint that does not look like a chat-completions URL leaves nowhere
// to derive one from, and that is reported as "could not check" — a
// self-hosted server exposing no listing is a supported deployment, not
// a misconfiguration.
func (p *openAICompatProvider) VerifyModel(ctx context.Context) error {
	if !strings.HasSuffix(p.endpoint, completionsSuffix) {
		return fmt.Errorf("%w: %s exposes no model listing to check against",
			ErrUnavailable, p.endpoint)
	}
	listURL := strings.TrimSuffix(p.endpoint, completionsSuffix) + "/models"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return fmt.Errorf("%w: building the listing request", ErrUnavailable)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.http.Do(req)
	if err != nil {
		// The transport error can name the URL but never the key, which
		// travels in a header.
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("%w: reading the model listing", ErrUnavailable)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// No body in the error: an upstream rejecting a bad key is
		// entitled to echo it back, and this string reaches a log.
		return fmt.Errorf("%w: model listing returned %d", ErrUnavailable, resp.StatusCode)
	}

	var listing struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &listing); err != nil {
		return fmt.Errorf("%w: malformed model listing", ErrUnavailable)
	}

	available := make([]string, 0, len(listing.Data))
	for _, m := range listing.Data {
		if m.ID == p.model {
			return nil
		}
		available = append(available, m.ID)
	}

	// The alternatives are the point. A warning that says only "not
	// available" tells the reader to go and find someone else's
	// documentation; one that lists what the key can actually reach is
	// the whole fix.
	return fmt.Errorf("%w: %q not offered; available: %s",
		ErrModelUnavailable, p.model, strings.Join(available, ", "))
}
