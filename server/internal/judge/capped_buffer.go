package judge

import "bytes"

// maxOutputBytes caps how much of a program's output the judge keeps.
//
// Output streams out of the container and is buffered in the judge
// process, which is outside the container's memory cgroup — so every
// limit that protects the sandbox (memory, swap, PIDs) does nothing
// here. Without a cap, `while true: print(...)` stays well inside its
// own memory limit while exhausting the judge's heap.
//
// 1 MiB is far more than any legitimate solution prints, and small
// enough that a full buffer costs nothing.
const maxOutputBytes = 1 << 20

// cappedBuffer is an io.Writer that stores at most max bytes and
// discards the rest, recording that it had to.
type cappedBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func newCappedBuffer(max int) *cappedBuffer {
	return &cappedBuffer{max: max}
}

// Write stores what fits and silently drops the remainder.
//
// It always reports a full write. A short write would make the caller
// (stdcopy.StdCopy) stop with an error and leave the container's output
// pipe blocked, which would hang the exec rather than bound it — so the
// bytes are accepted and thrown away instead.
func (c *cappedBuffer) Write(p []byte) (int, error) {
	if remaining := c.max - c.buf.Len(); remaining > 0 {
		if len(p) > remaining {
			c.truncated = true
			c.buf.Write(p[:remaining])
		} else {
			c.buf.Write(p)
		}
	} else if len(p) > 0 {
		c.truncated = true
	}
	return len(p), nil
}

// String returns the captured output.
func (c *cappedBuffer) String() string { return c.buf.String() }

// Truncated reports whether output was discarded, which the caller
// treats as a failed run rather than comparing a truncated prefix
// against the expected output.
func (c *cappedBuffer) Truncated() bool { return c.truncated }
