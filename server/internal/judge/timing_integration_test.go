//go:build integration

package judge

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"
)

// TestJudgeTiming_RealHardware records what the judge actually costs on
// the machine it is running on, using the real sandbox and the real judge
// path rather than a synthetic benchmark.
//
// It exists because a migration to arm64 turned on a question nobody had
// measured: how much of the compile timeout does an ordinary submission
// actually consume? The answer on x86 turned out to be most of it — a
// cold Go compile took about 12 seconds against a 15 second limit — which
// was a latent production problem rather than an architecture one.
//
// The numbers below are printed, not asserted, except for one floor: no
// ordinary submission may come close to a limit. A test that only passes
// or fails would hide the margin, and the margin is the interesting part.
func TestJudgeTiming_RealHardware(t *testing.T) {
	sandbox, err := NewDockerSandbox()
	if err != nil {
		t.Fatalf("docker sandbox: %v", err)
	}

	// The canonical reverse-a-string submission, in each language the
	// judge supports. These are what a real user submits.
	programs := []struct {
		language string
		source   string
	}{
		{"python", "s = input()\nprint(s[::-1])"},
		{"cpp", "#include <iostream>\n#include <string>\n#include <algorithm>\nint main(){std::string s;std::getline(std::cin,s);std::reverse(s.begin(),s.end());std::cout<<s<<std::endl;return 0;}"},
		{"go", "package main\n\nimport (\n\t\"bufio\"\n\t\"fmt\"\n\t\"os\"\n\t\"strings\"\n)\n\nfunc main() {\n\tr := bufio.NewReader(os.Stdin)\n\ts, _ := r.ReadString('\\n')\n\ts = strings.TrimRight(s, \"\\r\\n\")\n\tb := []rune(s)\n\tfor i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {\n\t\tb[i], b[j] = b[j], b[i]\n\t}\n\tfmt.Println(string(b))\n}"},
		{"java", "import java.util.Scanner;\npublic class Main{public static void main(String[] a){Scanner sc=new Scanner(System.in);String s=sc.hasNextLine()?sc.nextLine():\"\";System.out.println(new StringBuilder(s).reverse());}}"},
	}

	limits := Limits{TimeLimit: 2 * time.Second, MemoryLimitMB: 256}

	t.Logf("=== judge timing on %s/%s ===", runtime.GOOS, runtime.GOARCH)
	t.Logf("%-8s %10s %10s %10s %10s  %s", "lang", "startup", "compile", "run", "total", "verdict")

	for _, p := range programs {
		p := p
		t.Run(p.language, func(t *testing.T) {
			ctx := context.Background()

			startupStart := time.Now()
			sub, err := sandbox.NewSubmission(ctx, p.language, p.source, limits)
			if err != nil {
				t.Fatalf("provision sandbox: %v", err)
			}
			defer sub.Close(context.WithoutCancel(ctx))
			startup := time.Since(startupStart)

			compileStart := time.Now()
			compileRes, err := sub.Compile(ctx)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			compile := time.Since(compileStart)
			if compileRes.ExitCode != 0 {
				t.Fatalf("compile failed (exit %d): %s", compileRes.ExitCode, compileRes.Stderr)
			}

			runStart := time.Now()
			runRes, err := sub.Run(ctx, "hello arm64\n")
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			run := time.Since(runStart)

			got := runRes.Stdout
			want := "46mra olleh\n"
			verdict := "correct"
			if got != want {
				verdict = fmt.Sprintf("WRONG (%q)", got)
			}

			t.Logf("%-8s %10s %10s %10s %10s  %s",
				p.language,
				startup.Round(time.Millisecond),
				compile.Round(time.Millisecond),
				run.Round(time.Millisecond),
				(startup + compile + run).Round(time.Millisecond),
				verdict)

			if got != want {
				t.Errorf("output = %q, want %q", got, want)
			}

			// The floor: an ordinary submission must not be anywhere near
			// the compile budget. Half of it is already too close — that
			// is the state x86 was silently in before the build cache was
			// pre-warmed, and it is what would make a slower host start
			// failing correct submissions.
			if compile > compileTimeout/2 {
				t.Errorf("compile took %s, over half the %s budget — an ordinary submission "+
					"must have a comfortable margin, not a marginal one", compile, compileTimeout)
			}
			// Likewise the run: this program does almost nothing, so
			// anything approaching the time limit is infrastructure
			// overhead being charged to the user.
			if run > limits.TimeLimit/2 {
				t.Errorf("run took %s, over half the %s limit for a trivial program", run, limits.TimeLimit)
			}
		})
	}
}

// TestSandboxImage_GoBuildCacheIsPreWarmed guards the one thing standing
// between Go submissions and spurious compile failures.
//
// Measured on the production-target instance (t4g.small, 1 vCPU, 256 MB,
// the judge's own flags): a compile with the pre-warmed cache takes about
// 0.5s, while the same compile with an empty cache takes 12-15s idle and
// 17s under load, against a 15s budget. The cache is therefore not an
// optimisation — without it, correct Go submissions fail as compile
// errors whenever the machine is busy.
//
// It is easy to lose by accident: drop the RUN that builds it from the
// Dockerfile, or change GOCACHE in the exec environment, and everything
// still looks fine on a fast idle laptop while failing in production.
func TestSandboxImage_GoBuildCacheIsPreWarmed(t *testing.T) {
	sandbox, err := NewDockerSandbox()
	if err != nil {
		t.Fatalf("docker sandbox: %v", err)
	}

	// A Go program is the only way to reach the exec environment the
	// judge really uses, GOCACHE included.
	sub, err := sandbox.NewSubmission(context.Background(), "go",
		"package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"cache-probe\") }",
		Limits{TimeLimit: 5 * time.Second, MemoryLimitMB: 256})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	defer sub.Close(context.WithoutCancel(context.Background()))

	start := time.Now()
	res, err := sub.Compile(context.Background())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("compile failed (%d): %s", res.ExitCode, res.Stderr)
	}

	t.Logf("go compile with the image's cache: %s (budget %s)", elapsed.Round(time.Millisecond), compileTimeout)

	// A cold compile is an order of magnitude slower, so a generous
	// threshold still separates them unambiguously. This is deliberately
	// not a tight performance assertion — it checks the cache exists.
	if elapsed > compileTimeout/3 {
		t.Errorf("go compile took %s, over a third of the %s budget — the pre-warmed "+
			"build cache is missing or GOCACHE no longer points at it. See the RUN that "+
			"populates /opt/gocache in docker/judge-sandbox/Dockerfile.", elapsed, compileTimeout)
	}
}
