//go:build integration

package judge

import (
	"context"
	"testing"
	"time"
)

// What does the judge charge a program that does nothing at all? That
// number is the floor: every submission pays it before its own work
// starts, and it is the part of the reported runtime that is not the
// user's algorithm.
func TestRuntimeOverhead_Floor(t *testing.T) {
	sandbox, err := NewDockerSandbox()
	if err != nil {
		t.Fatalf("docker: %v", err)
	}
	limits := Limits{TimeLimit: 5 * time.Second, MemoryLimitMB: 256}

	for _, tc := range []struct{ lang, src string }{
		{"python", "pass"},
		{"cpp", "int main(){return 0;}"},
		{"go", "package main\nfunc main(){}"},
		{"java", "public class Main{public static void main(String[] a){}}"},
	} {
		tc := tc
		t.Run(tc.lang, func(t *testing.T) {
			sub, err := sandbox.NewSubmission(context.Background(), tc.lang, tc.src, limits)
			if err != nil {
				t.Fatalf("provision: %v", err)
			}
			defer sub.Close(context.WithoutCancel(context.Background()))
			if _, err := sub.Compile(context.Background()); err != nil {
				t.Fatalf("compile: %v", err)
			}
			var best int64 = 1 << 40
			for i := 0; i < 3; i++ {
				res, err := sub.Run(context.Background(), "")
				if err != nil {
					t.Fatalf("run: %v", err)
				}
				if res.RuntimeMS < best {
					best = res.RuntimeMS
				}
			}
			t.Logf("%-7s reported floor for a no-op program: %d ms", tc.lang, best)

			// The floor is what every submission pays before its own work
			// begins. Measured on this host: cpp 29ms, go 26ms, python
			// 38ms, java 106ms. For the compiled languages a no-op binary
			// runs in well under a millisecond, so essentially all of that
			// is the Docker exec round trip — the platform's cost, charged
			// to the user.
			//
			// It is small against the time limits problems actually set
			// (a 1s budget makes it about 3%), and the exec create and
			// attach calls are already outside the measurement. Removing
			// the rest would mean timing inside the container and reading
			// the result back through a side channel, which costs another
			// round trip per test case — likely more than the 27ms it
			// would recover.
			//
			// So this is a guard rather than a fix: it fails if the
			// overhead grows, which is the thing that would actually make
			// verdicts unfair.
			ceiling := int64(150)
			if tc.lang == "java" {
				// JVM startup dominates here and belongs to the program,
				// not the platform.
				ceiling = 400
			}
			if best > ceiling {
				t.Errorf("%s charges %d ms to a program that does nothing, over the %d ms ceiling — "+
					"per-run overhead has grown and is being billed as user execution time",
					tc.lang, best, ceiling)
			}
		})
	}
}

// memoryKb reported 0 for every submission ever judged: the field was
// plumbed end to end but nothing assigned it. This proves the number is
// now real by making two programs whose memory use differs by a wide,
// deliberate margin and checking the judge can tell them apart.
func TestPeakMemory_ReportsRealUsage(t *testing.T) {
	sandbox, err := NewDockerSandbox()
	if err != nil {
		t.Fatalf("docker: %v", err)
	}
	limits := Limits{TimeLimit: 5 * time.Second, MemoryLimitMB: 256}
	j := NewJudge(sandbox)

	small, err := j.Evaluate(context.Background(), "python",
		"print('ok')", []TestCase{{ExpectedOutput: "ok\n"}}, limits)
	if err != nil {
		t.Fatalf("small: %v", err)
	}
	big, err := j.Evaluate(context.Background(), "python",
		"b = bytearray(120 * 1024 * 1024)\nprint('ok')",
		[]TestCase{{ExpectedOutput: "ok\n"}}, limits)
	if err != nil {
		t.Fatalf("big: %v", err)
	}

	t.Logf("trivial program : %d KB", small.MemoryKB)
	t.Logf("120 MB program  : %d KB", big.MemoryKB)

	if small.MemoryKB <= 0 {
		t.Error("a judged submission still reports no memory — the measurement is not reaching the verdict")
	}
	// The allocating program must be visibly heavier. 100 MB of headroom
	// below the 120 MB it asks for leaves room for allocator behaviour
	// without letting a stuck zero pass.
	if big.MemoryKB-small.MemoryKB < 100*1024 {
		t.Errorf("120 MB allocation only moved the reading from %d KB to %d KB — "+
			"the value is not tracking actual usage", small.MemoryKB, big.MemoryKB)
	}
}
