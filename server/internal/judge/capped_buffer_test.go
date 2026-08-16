package judge

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCappedBuffer_KeepsOutputUnderTheLimitIntact(t *testing.T) {
	buf := newCappedBuffer(100)

	n, err := buf.Write([]byte("hello"))

	assert.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, "hello", buf.String())
	assert.False(t, buf.Truncated())
}

func TestCappedBuffer_TruncatesAtTheLimit(t *testing.T) {
	buf := newCappedBuffer(10)

	_, err := buf.Write([]byte("0123456789ABCDEF"))

	assert.NoError(t, err)
	assert.Equal(t, "0123456789", buf.String(), "only the first 10 bytes are kept")
	assert.True(t, buf.Truncated())
}

// TestCappedBuffer_ReportsFullWrites is the property that keeps the
// container from hanging: a short write would make stdcopy stop with an
// error and leave the output pipe blocked.
func TestCappedBuffer_ReportsFullWrites(t *testing.T) {
	buf := newCappedBuffer(4)

	for range 3 {
		n, err := buf.Write([]byte("aaaaaaaa"))
		assert.NoError(t, err)
		assert.Equal(t, 8, n, "the writer must always claim to have consumed everything")
	}

	assert.Len(t, buf.String(), 4)
	assert.True(t, buf.Truncated())
}

// TestCappedBuffer_BoundsAFloodOfOutput is the case that motivated the
// cap: output leaves the container and is buffered in the judge process,
// outside the container's memory cgroup.
func TestCappedBuffer_BoundsAFloodOfOutput(t *testing.T) {
	buf := newCappedBuffer(maxOutputBytes)
	chunk := []byte(strings.Repeat("x", 64*1024))

	// 16MB written; the buffer must still hold only its cap.
	for range 256 {
		_, _ = buf.Write(chunk)
	}

	assert.Len(t, buf.String(), maxOutputBytes)
	assert.True(t, buf.Truncated())
}

func TestJudge_OutputLimitExceededIsNotComparedAgainstExpected(t *testing.T) {
	// A truncated stdout must never be compared: the prefix could match
	// the expected output by accident and wrongly earn an accept.
	sub := &fakeSubmission{runResults: []ExecuteResult{
		{Stdout: "6\n", ExitCode: 0, OutputTruncated: true},
	}}
	j := NewJudge(&fakeSandbox{submission: sub})

	result, err := j.Evaluate(t.Context(), "python", "print(6)",
		[]TestCase{{Input: "", ExpectedOutput: "6\n"}}, Limits{})

	assert.NoError(t, err)
	assert.Equal(t, VerdictOutputLimitExceeded, result.Verdict)
	assert.Equal(t, 0, result.FailedCase)
}
