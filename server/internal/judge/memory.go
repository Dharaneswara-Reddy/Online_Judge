package judge

import (
	"context"
	"strconv"
	"strings"
)

// peakMemoryPaths are the cgroup files that report the high-water mark of
// a container's memory usage, newest kernel interface first.
//
// cgroup v2 exposes memory.peak; v1 exposed memory.max_usage_in_bytes.
// Reading whichever exists keeps this working across hosts without
// probing the kernel version.
var peakMemoryPaths = []string{
	"/sys/fs/cgroup/memory.peak",
	"/sys/fs/cgroup/memory/memory.max_usage_in_bytes",
}

// peakMemoryKB reports the highest memory the container reached, in KB.
//
// This is read from inside the container rather than from the Docker API,
// because the API has nowhere to report it: user code runs as an exec
// inside a container whose main process is `sleep infinity`, so
// ContainerInspect describes the wrong process and `docker stats` samples
// a moment in time — by the time a run has finished, the peak it cared
// about is gone. The cgroup's own high-water mark is the only reading
// that survives the process it measured.
//
// It is read once per submission rather than once per test case. The
// container is reused across cases, so the cgroup's peak is already the
// maximum over the whole submission — which is the number the verdict
// wants — and reading it once costs one exec instead of twenty.
//
// A failure returns 0 with ok=false. The caller must not turn that into a
// zero reading: "not measured" and "used no memory" are different claims,
// and only one of them is honest.
func peakMemoryKB(ctx context.Context, run func(context.Context, []string) (ExecuteResult, error)) (int64, bool) {
	for _, path := range peakMemoryPaths {
		res, err := run(ctx, []string{"cat", path})
		if err != nil || res.ExitCode != 0 {
			continue
		}
		value := strings.TrimSpace(res.Stdout)
		if value == "" || value == "max" {
			continue
		}
		bytes, convErr := strconv.ParseInt(value, 10, 64)
		if convErr != nil || bytes <= 0 {
			continue
		}
		return bytes / 1024, true
	}
	return 0, false
}
