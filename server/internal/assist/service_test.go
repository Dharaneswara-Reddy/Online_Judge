package assist

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeProvider is the whole reason Provider has one method: a test
// double is three lines and no test in this package opens a socket.
type fakeProvider struct {
	mu     sync.Mutex
	reply  string
	err    error
	calls  int
	last   Prompt
	replay []string // when set, consumed one per call before falling back to reply
}

func (f *fakeProvider) Complete(_ context.Context, p Prompt) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.last = p
	if len(f.replay) > 0 {
		reply := f.replay[0]
		f.replay = f.replay[1:]
		return reply, f.err
	}
	return f.reply, f.err
}

func (f *fakeProvider) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeProvider) prompt() Prompt {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}

const goodHint = "Keep track of the smallest value you have seen so far and update the answer as you scan."

func newTestService(p Provider) *Service {
	return NewService(p, Options{Cache: NewMemoryCache(time.Minute, 64)})
}

func hintReq(r Rung) HintRequest {
	return HintRequest{
		Rung:     r,
		Problem:  sampleProblem(),
		Language: "python",
		Code:     "def solve(): pass",
	}
}

// --- disabled mode -------------------------------------------------------

// TestDisabledServiceIsUsable is the whole optional-dependency
// discipline in one test: no key configured must degrade, not crash.
func TestDisabledServiceIsUsable(t *testing.T) {
	s := NewService(nil, Options{})

	if s == nil {
		t.Fatal("NewService(nil, ...) returned nil; a disabled service must still be usable")
	}
	if s.Enabled() {
		t.Fatal("Enabled() = true with no provider")
	}

	ctx := context.Background()
	if _, err := s.Hint(ctx, hintReq(RungConstraint)); !errors.Is(err, ErrDisabled) {
		t.Errorf("Hint = %v, want ErrDisabled", err)
	}
	if _, err := s.ExplainVerdict(ctx, ExplainRequest{}); !errors.Is(err, ErrDisabled) {
		t.Errorf("ExplainVerdict = %v, want ErrDisabled", err)
	}
	if _, err := s.ReviewSolution(ctx, ReviewRequest{}); !errors.Is(err, ErrDisabled) {
		t.Errorf("ReviewSolution = %v, want ErrDisabled", err)
	}
}

// TestNilServiceIsSafe lets the wiring hold a *Service that was never
// constructed without every call site needing a nil check.
func TestNilServiceIsSafe(t *testing.T) {
	var s *Service

	if s.Enabled() {
		t.Fatal("a nil *Service reported itself enabled")
	}
	if _, err := s.Hint(context.Background(), hintReq(RungConstraint)); !errors.Is(err, ErrDisabled) {
		t.Fatalf("Hint on nil service = %v, want ErrDisabled", err)
	}
}

// --- hints ---------------------------------------------------------------

func TestHintHappyPath(t *testing.T) {
	fp := &fakeProvider{reply: goodHint}
	s := newTestService(fp)

	got, err := s.Hint(context.Background(), hintReq(RungOutline))
	if err != nil {
		t.Fatalf("Hint = %v", err)
	}
	if got.Text != goodHint {
		t.Errorf("Text = %q, want %q", got.Text, goodHint)
	}
	if got.Rung != RungOutline {
		t.Errorf("Rung = %d, want %d", got.Rung, RungOutline)
	}
	if got.Cached {
		t.Error("the first call reported a cache hit")
	}
}

func TestHintRejectsAnInvalidRung(t *testing.T) {
	s := newTestService(&fakeProvider{reply: goodHint})

	for _, r := range []Rung{0, 5, -1} {
		if _, err := s.Hint(context.Background(), hintReq(r)); !errors.Is(err, ErrInvalidRung) {
			t.Errorf("Hint(rung %d) = %v, want ErrInvalidRung", r, err)
		}
	}
}

// TestHintCachesTheLowRungs: rungs 1 and 2 answer a question about the
// problem, not about this student, so ten students share one generation.
func TestHintCachesTheLowRungs(t *testing.T) {
	for _, r := range []Rung{RungConstraint, RungShape} {
		fp := &fakeProvider{reply: goodHint}
		s := newTestService(fp)

		first, err := s.Hint(context.Background(), hintReq(r))
		if err != nil {
			t.Fatalf("rung %d first call: %v", r, err)
		}

		// A different student, different code, same problem and rung.
		second := hintReq(r)
		second.Code = "class Foo: pass  # someone else entirely"
		got, err := s.Hint(context.Background(), second)
		if err != nil {
			t.Fatalf("rung %d second call: %v", r, err)
		}

		if fp.count() != 1 {
			t.Errorf("rung %d: provider called %d times, want 1", r, fp.count())
		}
		if !got.Cached {
			t.Errorf("rung %d: second call did not report Cached", r)
		}
		if got.Text != first.Text {
			t.Errorf("rung %d: cached text differs from the original", r)
		}
	}
}

// TestHintDoesNotCacheTheHighRungs: rungs 3 and 4 are about this
// student's code, so a shared answer would be both wrong and a leak of
// one student's situation to another.
func TestHintDoesNotCacheTheHighRungs(t *testing.T) {
	for _, r := range []Rung{RungFailing, RungOutline} {
		fp := &fakeProvider{reply: goodHint}
		s := newTestService(fp)

		req := hintReq(r)
		if r == RungFailing {
			req.Failing = &HiddenCase{Input: "1 2 3 4 5 6 7 8", ExpectedOutput: "36"}
		}

		if _, err := s.Hint(context.Background(), req); err != nil {
			t.Fatalf("rung %d first call: %v", r, err)
		}
		got, err := s.Hint(context.Background(), req)
		if err != nil {
			t.Fatalf("rung %d second call: %v", r, err)
		}

		if fp.count() != 2 {
			t.Errorf("rung %d: provider called %d times, want 2", r, fp.count())
		}
		if got.Cached {
			t.Errorf("rung %d reported a cache hit", r)
		}
	}
}

// TestHintWithholdsCode is the safety-critical path end to end: the
// model ignored the instructions, so the student gets nothing.
func TestHintWithholdsCode(t *testing.T) {
	fp := &fakeProvider{reply: "Sure:\n```python\ndef solve():\n    return 42\n```"}
	s := newTestService(fp)

	got, err := s.Hint(context.Background(), hintReq(RungOutline))
	if !errors.Is(err, ErrFiltered) {
		t.Fatalf("Hint = %v, want ErrFiltered", err)
	}
	if got.Text != "" {
		t.Fatalf("the filtered text was returned anyway: %q", got.Text)
	}
}

// TestHintWithholdsALeakedCase: rung 3 is the only rung that sees a
// hidden case, and it is the only rung that can leak one.
func TestHintWithholdsALeakedCase(t *testing.T) {
	hidden := HiddenCase{Input: "8\n3 1 4 1 5 9 2 6\n", ExpectedOutput: "31415926\n"}
	fp := &fakeProvider{reply: "Your code mishandles 3 1 4 1 5 9 2 6 because of the duplicate."}
	s := newTestService(fp)

	req := hintReq(RungFailing)
	req.Failing = &hidden

	got, err := s.Hint(context.Background(), req)
	if !errors.Is(err, ErrLeak) {
		t.Fatalf("Hint = %v, want ErrLeak", err)
	}
	if got.Text != "" {
		t.Fatalf("the leaking text was returned anyway: %q", got.Text)
	}
}

// TestHintDoesNotCacheAFilteredResponse: a withheld generation must not
// poison the cache for the next student.
func TestHintDoesNotCacheAFilteredResponse(t *testing.T) {
	fp := &fakeProvider{replay: []string{"```\ncode\n```"}, reply: goodHint}
	s := newTestService(fp)

	if _, err := s.Hint(context.Background(), hintReq(RungConstraint)); !errors.Is(err, ErrFiltered) {
		t.Fatalf("first call = %v, want ErrFiltered", err)
	}
	got, err := s.Hint(context.Background(), hintReq(RungConstraint))
	if err != nil {
		t.Fatalf("second call = %v", err)
	}
	if got.Cached {
		t.Fatal("a filtered response was served from the cache")
	}
	if got.Text != goodHint {
		t.Fatalf("Text = %q, want the good hint", got.Text)
	}
}

func TestHintProviderFailureIsUnavailable(t *testing.T) {
	fp := &fakeProvider{err: errors.New("dial tcp: connection refused")}
	s := newTestService(fp)

	if _, err := s.Hint(context.Background(), hintReq(RungConstraint)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Hint = %v, want ErrUnavailable", err)
	}
}

func TestHintEmptyResponseIsUnavailable(t *testing.T) {
	s := newTestService(&fakeProvider{reply: "   \n  "})

	if _, err := s.Hint(context.Background(), hintReq(RungConstraint)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Hint = %v, want ErrUnavailable", err)
	}
}

// --- verdict explanations ------------------------------------------------

// TestExplainCachesAcrossStudents is the point of the cache key: ten
// students failing case 12 of the same problem need one explanation
// between them, and their code is deliberately not part of the key.
func TestExplainCachesAcrossStudents(t *testing.T) {
	fp := &fakeProvider{reply: "Case 12 has a value larger than your accumulator can hold."}
	s := newTestService(fp)

	a := ExplainRequest{
		Problem: sampleProblem(), Language: "python", Code: "student A's code",
		Status: "wrong_answer", FailedCase: 12, TotalCases: 40,
	}
	b := a
	b.Code = "student B's completely different code"
	b.Language = "cpp"
	b.RuntimeMS = 999

	if _, err := s.ExplainVerdict(context.Background(), a); err != nil {
		t.Fatalf("first: %v", err)
	}
	got, err := s.ExplainVerdict(context.Background(), b)
	if err != nil {
		t.Fatalf("second: %v", err)
	}

	if fp.count() != 1 {
		t.Errorf("provider called %d times, want 1", fp.count())
	}
	if !got.Cached {
		t.Error("second call did not report Cached")
	}
}

func TestExplainKeyDistinguishesCaseAndStatus(t *testing.T) {
	fp := &fakeProvider{reply: "an explanation"}
	s := newTestService(fp)

	req := ExplainRequest{
		Problem: sampleProblem(), Status: "wrong_answer", FailedCase: 12, TotalCases: 40,
	}
	other := req
	other.FailedCase = 13
	another := req
	another.Status = "time_limit_exceeded"

	ctx := context.Background()
	for _, r := range []ExplainRequest{req, other, another} {
		if _, err := s.ExplainVerdict(ctx, r); err != nil {
			t.Fatalf("ExplainVerdict: %v", err)
		}
	}
	if fp.count() != 3 {
		t.Fatalf("provider called %d times, want 3 — the key is too coarse", fp.count())
	}
}

// TestExplainSkipsTheCacheForCompileErrors: a compile error is a fact
// about one student's source. Serving a cached one to the next student
// would answer the wrong question and quietly describe someone else's
// code to them.
func TestExplainSkipsTheCacheForCompileErrors(t *testing.T) {
	fp := &fakeProvider{reply: "The compiler could not find that name in scope."}
	s := newTestService(fp)

	req := ExplainRequest{
		Problem: sampleProblem(), Language: "cpp", Code: "int x = y;",
		Status: "compile_error", CompileError: "error: 'y' was not declared in this scope",
	}

	ctx := context.Background()
	if _, err := s.ExplainVerdict(ctx, req); err != nil {
		t.Fatalf("first: %v", err)
	}
	got, err := s.ExplainVerdict(ctx, req)
	if err != nil {
		t.Fatalf("second: %v", err)
	}

	if fp.count() != 2 {
		t.Errorf("provider called %d times, want 2", fp.count())
	}
	if got.Cached {
		t.Error("a compile-error explanation was served from the cache")
	}
}

func TestExplainFiltersCode(t *testing.T) {
	s := newTestService(&fakeProvider{reply: "Replace it with:\n```cpp\nint main(){}\n```"})

	got, err := s.ExplainVerdict(context.Background(), ExplainRequest{
		Problem: sampleProblem(), Status: "wrong_answer", FailedCase: 1,
	})
	if !errors.Is(err, ErrFiltered) {
		t.Fatalf("ExplainVerdict = %v, want ErrFiltered", err)
	}
	if got.Text != "" {
		t.Fatalf("filtered text returned: %q", got.Text)
	}
}

// --- reviews -------------------------------------------------------------

// TestReviewNeverCaches: a review is about exactly one submission, and
// the controller only reaches it for an accepted one.
func TestReviewNeverCaches(t *testing.T) {
	fp := &fakeProvider{reply: "Your solution is linear in time and constant in space."}
	s := newTestService(fp)

	req := ReviewRequest{Problem: sampleProblem(), Language: "go", Code: "func main() {}"}
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if _, err := s.ReviewSolution(ctx, req); err != nil {
			t.Fatalf("ReviewSolution: %v", err)
		}
	}
	if fp.count() != 2 {
		t.Fatalf("provider called %d times, want 2", fp.count())
	}
}

func TestReviewFiltersCode(t *testing.T) {
	s := newTestService(&fakeProvider{reply: "Better:\ntotal = 0\nfor x in a:\n    total += x"})

	got, err := s.ReviewSolution(context.Background(), ReviewRequest{
		Problem: sampleProblem(), Language: "python", Code: "pass",
	})
	if !errors.Is(err, ErrFiltered) {
		t.Fatalf("ReviewSolution = %v, want ErrFiltered", err)
	}
	if got.Text != "" {
		t.Fatalf("filtered text returned: %q", got.Text)
	}
}

// --- code handling -------------------------------------------------------

func TestServiceTruncatesCodeToTheConfiguredCap(t *testing.T) {
	fp := &fakeProvider{reply: goodHint}
	s := NewService(fp, Options{MaxCodeBytes: 64})

	req := hintReq(RungOutline)
	req.Code = strings.Repeat("z", 200) + "TAIL_MARKER"

	if _, err := s.Hint(context.Background(), req); err != nil {
		t.Fatalf("Hint: %v", err)
	}
	if strings.Contains(fp.prompt().User, "TAIL_MARKER") {
		t.Fatal("code beyond MaxCodeBytes reached the provider")
	}
}

func TestServiceDefaultsMaxCodeBytes(t *testing.T) {
	fp := &fakeProvider{reply: goodHint}
	s := NewService(fp, Options{})

	req := hintReq(RungOutline)
	req.Code = strings.Repeat("z", DefaultMaxCodeBytes+50) + "TAIL_MARKER"

	if _, err := s.Hint(context.Background(), req); err != nil {
		t.Fatalf("Hint: %v", err)
	}
	if strings.Contains(fp.prompt().User, "TAIL_MARKER") {
		t.Fatal("the default cap was not applied")
	}
}

// TestServiceWorksWithoutACache: Options.Cache is optional, and a
// missing one must not turn every call into a nil dereference.
func TestServiceWorksWithoutACache(t *testing.T) {
	fp := &fakeProvider{reply: goodHint}
	s := NewService(fp, Options{})

	ctx := context.Background()
	if _, err := s.Hint(ctx, hintReq(RungConstraint)); err != nil {
		t.Fatalf("Hint: %v", err)
	}
	if _, err := s.Hint(ctx, hintReq(RungConstraint)); err != nil {
		t.Fatalf("Hint (second): %v", err)
	}
}

func TestServiceHonoursContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s := newTestService(&fakeProvider{reply: goodHint})
	if _, err := s.Hint(ctx, hintReq(RungConstraint)); err == nil {
		t.Fatal("Hint on a cancelled context succeeded")
	}
}
