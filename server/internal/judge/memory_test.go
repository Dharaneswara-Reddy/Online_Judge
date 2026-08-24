package judge

import (
	"context"
	"errors"
	"testing"
)

func readerReturning(out map[string]string) func(context.Context, []string) (ExecuteResult, error) {
	return func(_ context.Context, cmd []string) (ExecuteResult, error) {
		path := cmd[len(cmd)-1]
		v, ok := out[path]
		if !ok {
			return ExecuteResult{ExitCode: 1}, nil
		}
		return ExecuteResult{ExitCode: 0, Stdout: v}, nil
	}
}

// cgroup v2 is the common case on current hosts.
func TestPeakMemoryKB_ReadsCgroupV2(t *testing.T) {
	got, ok := peakMemoryKB(context.Background(), readerReturning(map[string]string{
		"/sys/fs/cgroup/memory.peak": "140046336\n",
	}))
	if !ok {
		t.Fatal("a readable memory.peak must produce a measurement")
	}
	if want := int64(140046336 / 1024); got != want {
		t.Errorf("peak = %d KB, want %d KB — the field is KB, not bytes", got, want)
	}
}

// Older hosts expose the v1 name instead; the v2 read fails and the
// fallback has to carry it.
func TestPeakMemoryKB_FallsBackToCgroupV1(t *testing.T) {
	got, ok := peakMemoryKB(context.Background(), readerReturning(map[string]string{
		"/sys/fs/cgroup/memory/memory.max_usage_in_bytes": "2097152\n",
	}))
	if !ok || got != 2048 {
		t.Errorf("peak = %d KB ok=%v, want 2048 KB", got, ok)
	}
}

// The important one: when nothing can be read, the answer is "unknown",
// not "zero". Reporting 0 KB claims the program used no memory, which is
// never true and is exactly the false confidence this replaces.
func TestPeakMemoryKB_UnreadableIsUnknownNotZero(t *testing.T) {
	if _, ok := peakMemoryKB(context.Background(), readerReturning(nil)); ok {
		t.Error("an unreadable cgroup must report not-measured rather than a value")
	}
	failing := func(context.Context, []string) (ExecuteResult, error) {
		return ExecuteResult{}, errors.New("exec failed")
	}
	if _, ok := peakMemoryKB(context.Background(), failing); ok {
		t.Error("an exec failure must report not-measured")
	}
}

// cgroup can literally contain "max", and nonsense must not parse.
func TestPeakMemoryKB_RejectsNonNumericReadings(t *testing.T) {
	for _, bad := range []string{"max", "", "not-a-number", "-1", "0"} {
		if _, ok := peakMemoryKB(context.Background(), readerReturning(map[string]string{
			"/sys/fs/cgroup/memory.peak": bad,
		})); ok {
			t.Errorf("value %q must not be accepted as a measurement", bad)
		}
	}
}
