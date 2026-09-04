package assist

import (
	"context"
	"fmt"
	"os"
	"testing"
)

// A live diagnostic against the configured provider, skipped unless
// GROQ_LIVE=1 because it makes real, billed calls and prints raw model
// output.
//
// It exists because two defects here were invisible to every other test
// in this package. The model wrapped a quoted expression in a markdown
// fence, which RejectCode discards in full; and the token ceilings were
// sized for the prose rather than for a reasoning model, so half the
// replies came back empty and the rest stopped mid-sentence. Neither is
// reachable with a fake Provider, and both are the first thing to check
// when the model id changes:
//
//	GROQ_LIVE=1 GROQ_API_KEY=... go test ./internal/assist/ -run Live -v
func TestLiveModelDiagnostic(t *testing.T) {
	key := os.Getenv("GROQ_API_KEY")
	if os.Getenv("GROQ_LIVE") != "1" || key == "" {
		t.Skip("live diagnostic disabled")
	}

	p := NewGroqProvider(key, DefaultGroqModel, nil)

	req := ExplainRequest{
		Problem: ProblemContext{
			Title:         "Best Time to Buy and Sell Stock",
			Statement:     "Given prices, return the maximum profit from one buy and one later sell, or 0.",
			Difficulty:    "easy",
			Tags:          []string{"array", "greedy"},
			TimeLimitMS:   1000,
			MemoryLimitMB: 256,
		},
		Language: "python",
		Code: "import sys\ndata = sys.stdin.read().split()\nn = int(data[0])\n" +
			"prices = list(map(int, data[1:1+n]))\nprint(max(prices) - min(prices))\n",
		Status: "wrong_answer", FailedCase: 0, TotalCases: 5, RuntimeMS: 20, MemoryKB: 4096,
	}

	hint := HintRequest{Rung: RungShape, Problem: req.Problem, Language: "python", Code: req.Code}

	for i := 0; i < 4; i++ {
		pr := buildHintPrompt(hint, DefaultMaxCodeBytes)
		raw, err := p.Complete(context.Background(), pr)
		fmt.Printf("\n───── RUNG 2 SAMPLE %d (max_tokens=%d) ─────\n", i+1, pr.MaxTokens)
		if err != nil {
			fmt.Printf("ERROR: %v\n", err)
			continue
		}
		fmt.Printf("len=%d\n%s\n───── RejectCode: %v\n", len(raw), raw, RejectCode(raw))
	}
}
