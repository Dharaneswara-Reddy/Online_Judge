package assist

import (
	"context"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
)

// Service is the feature. It owns three decisions and delegates
// everything else: what may be sent to the model, what may be cached,
// and what may be returned.
//
// It holds no repository and opens no connection. Everything it needs
// arrives in the request struct, which is why the whole package can be
// tested without a database, a broker, or a network — and why the same
// Service can be driven from the API process today and from a worker
// behind the queue later without any change here.
type Service struct {
	provider     Provider
	cache        Cache
	maxCodeBytes int
}

// Options configures a Service. Every field is optional.
type Options struct {
	// Cache is where generations that may be shared between students are
	// kept. A nil Cache disables sharing; it never disables the feature.
	Cache Cache

	// MaxCodeBytes caps how much of a submission reaches the model.
	// Zero means DefaultMaxCodeBytes.
	MaxCodeBytes int
}

// NewService returns a Service over a provider.
//
// A nil provider is a supported, ordinary configuration: it is what a
// deployment with no ANTHROPIC_API_KEY gets, and it produces a Service
// whose every method reports ErrDisabled. This mirrors how the broker,
// Redis and the sandbox are treated everywhere else in this codebase —
// the feature disappears, the process still serves.
func NewService(p Provider, opts Options) *Service {
	maxCode := opts.MaxCodeBytes
	if maxCode <= 0 {
		maxCode = DefaultMaxCodeBytes
	}
	return &Service{
		provider:     p,
		cache:        opts.Cache,
		maxCodeBytes: maxCode,
	}
}

// Enabled reports whether a provider is wired.
//
// The nil-receiver case is deliberate: the wiring may hold a *Service
// that was never constructed, and making that answer "disabled" rather
// than panic keeps the nil checks out of every call site.
func (s *Service) Enabled() bool {
	return s != nil && s.provider != nil
}

// Hint returns one rung of the ladder.
//
// The rung decides everything: which system prompt is used, whether the
// student's code is sent at all, whether the failing case is sent, and
// whether the answer may be shared with the next student. A response
// that contains code, or that echoes a hidden case it was asked to
// describe, is discarded rather than returned.
func (s *Service) Hint(ctx context.Context, req HintRequest) (Hint, error) {
	if !s.Enabled() {
		return Hint{}, ErrDisabled
	}
	if !req.Rung.Valid() {
		return Hint{}, fmt.Errorf("%w: %d", ErrInvalidRung, req.Rung)
	}

	// Rungs 1 and 2 answer a question about the problem rather than
	// about this student, so one generation serves everybody who asks.
	// Rungs 3 and 4 are grounded in this submission and are never shared.
	var key string
	if req.Rung <= RungShape {
		key = "hint:" + req.Rung.String() + ":" + problemKey(req.Problem)
	}

	if cached, ok := s.lookup(key); ok {
		return Hint{Rung: req.Rung, Text: cached, Cached: true}, nil
	}

	text, err := s.generate(ctx, buildHintPrompt(req, s.maxCodeBytes))
	if err != nil {
		return Hint{}, err
	}

	if err := RejectCode(text); err != nil {
		return Hint{}, err
	}
	// Only rung 3 was given a case to leak, so only rung 3 is checked
	// against one. Checking the others would be theatre.
	if req.Rung == RungFailing && req.Failing != nil {
		if err := RejectLeak(text, *req.Failing); err != nil {
			return Hint{}, err
		}
	}

	s.store(key, text)
	return Hint{Rung: req.Rung, Text: text}, nil
}

// ExplainVerdict turns a bare verdict into a diagnosis.
//
// This is the highest-traffic path and the one the cost model rests on,
// which is why the key is (problem, status, failing case) and not the
// student's code: ten people failing case 12 of the same problem for the
// same reason need one explanation between them, not ten.
func (s *Service) ExplainVerdict(ctx context.Context, req ExplainRequest) (Explanation, error) {
	if !s.Enabled() {
		return Explanation{}, ErrDisabled
	}

	// A compile error is a fact about one student's source. A shared
	// answer would describe someone else's mistake to them with total
	// confidence, so this verdict opts out of the cache entirely.
	var key string
	if req.Status != statusCompileError {
		key = strings.Join([]string{
			"explain", problemKey(req.Problem), req.Status, strconv.Itoa(req.FailedCase),
		}, ":")
	}

	if cached, ok := s.lookup(key); ok {
		return Explanation{Text: cached, Cached: true}, nil
	}

	text, err := s.generate(ctx, buildExplainPrompt(req, s.maxCodeBytes))
	if err != nil {
		return Explanation{}, err
	}
	if err := RejectCode(text); err != nil {
		return Explanation{}, err
	}

	s.store(key, text)
	return Explanation{Text: text}, nil
}

// ReviewSolution critiques a submission that already passed.
//
// It never caches: a review is about one person's code, and there is no
// second reader it could be correct for.
func (s *Service) ReviewSolution(ctx context.Context, req ReviewRequest) (Review, error) {
	if !s.Enabled() {
		return Review{}, ErrDisabled
	}

	text, err := s.generate(ctx, buildReviewPrompt(req, s.maxCodeBytes))
	if err != nil {
		return Review{}, err
	}
	if err := RejectCode(text); err != nil {
		return Review{}, err
	}

	return Review{Text: text}, nil
}

// generate calls the provider and normalises what comes back.
//
// An empty completion is reported as ErrUnavailable rather than returned
// as an empty hint: from the student's side a blank panel and a failed
// request are the same event, and only one of them retries.
func (s *Service) generate(ctx context.Context, p Prompt) (string, error) {
	// Checked here rather than left to the provider, because a Provider
	// is an interface anyone may implement and not every implementation
	// will honour cancellation.
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("assist: %w", err)
	}

	raw, err := s.provider.Complete(ctx, p)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}

	text := strings.TrimSpace(raw)
	if text == "" {
		return "", fmt.Errorf("%w: empty completion", ErrUnavailable)
	}
	return text, nil
}

// lookup reads through the cache, tolerating both a nil cache and an
// empty key. An empty key means the caller decided this generation is
// not shareable, which is a safety decision and is spelled out at each
// call site rather than inferred here.
func (s *Service) lookup(key string) (string, bool) {
	if s.cache == nil || key == "" {
		return "", false
	}
	return s.cache.Get(key)
}

// store is lookup's counterpart. Nothing that failed a filter ever
// reaches it: a withheld generation must not be served to the next
// student, so the callers store only after every check has passed.
func (s *Service) store(key, value string) {
	if s.cache == nil || key == "" {
		return
	}
	s.cache.Set(key, value)
}

// problemKey identifies a problem for caching without carrying its text
// around in a map key.
//
// The statement is hashed along with the title on purpose: an admin who
// edits a problem invalidates every cached hint and explanation about
// it, which is the behaviour you want the moment a statement is
// corrected.
func problemKey(p ProblemContext) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(p.Title))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(p.Statement))
	return strconv.FormatUint(h.Sum64(), 36)
}
