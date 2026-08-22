package judge

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"sync/atomic"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

const judgeImage = "codearena-sandbox:latest"

// SandboxLabel marks every container this package creates.
//
// It is the only handle a restarted worker has on the containers its
// predecessor left behind. Nothing else identifies them: they are created
// from a shared image with a daemon-generated name, so a leaked one is
// otherwise indistinguishable from any other container on the host.
const (
	SandboxLabel      = "codearena.sandbox"
	sandboxLabelValue = "judge"
)

// AutoRemove is deliberately NOT set on these containers.
//
// The execution model is `sleep infinity` plus exec: the container never
// exits on its own, so AutoRemove — which fires when the main process
// exits — would simply never trigger and would buy nothing. Worse, the
// two places the container can stop (an operator's `docker stop`, the
// daemon restarting) are exactly the moments when a run is still in
// flight, and having the daemon delete the container underneath
// ContainerExecInspect turns a recoverable failure into an unexplained
// one. Removal stays explicit — Close on the happy path, ReconcileOrphans
// at startup for whatever a crash left behind.

// reconcileTimeout bounds the startup sweep. It talks to a local socket
// and removes a handful of containers at most.
const reconcileTimeout = 60 * time.Second

// Setup budgets, kept separate from the problem's own time limit so
// infrastructure latency is never charged to the user's program.
const (
	sourceWriteTimeout = 30 * time.Second
	compileTimeout     = 15 * time.Second

	// streamDrainGrace is how long StdCopy gets to finish reading output
	// the program already wrote, after the process itself has exited.
	streamDrainGrace = 2 * time.Second

	// daemonPingTimeout bounds the reachability check at startup. It only
	// ever talks to a local socket, so it either answers at once or is
	// not there at all.
	daemonPingTimeout = 5 * time.Second
)

// DockerSandbox implements the Sandbox interface using real Docker
// containers for isolated code execution.
type DockerSandbox struct{ cli *client.Client }

// NewDockerSandbox creates a DockerSandbox backed by the local Docker daemon.
func NewDockerSandbox() (*DockerSandbox, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}

	// Prove the daemon is actually reachable before claiming a sandbox.
	//
	// Constructing the client contacts nothing, so a process with no
	// access to the socket — the API container, which is denied it on
	// purpose — looked perfectly healthy here and only failed later, when
	// it tried to create a container. That turned a startup condition
	// callers can handle into a 500 on every run.
	ctx, cancel := context.WithTimeout(context.Background(), daemonPingTimeout)
	defer cancel()
	if _, err := cli.Ping(ctx); err != nil {
		cli.Close()
		return nil, fmt.Errorf("docker daemon unreachable: %w", err)
	}

	return &DockerSandbox{cli: cli}, nil
}

// ReconcileOrphans removes every sandbox container left on this host and
// reports how many it removed.
//
// A judge container is started with `sleep infinity`, so removing it is
// the only thing that ever stops it — there is no exit for the daemon to
// notice and no timeout inside the container to fire. A worker killed
// mid-judge therefore leaks a container that holds a vCPU reservation and
// up to 512MB of memory for as long as the host lives, and they
// accumulate across every crash. On a two-vCPU instance a couple of those
// is the whole machine.
//
// Call this at worker startup, before consuming anything. It assumes a
// judge worker is the only judge worker on its Docker host, which is what
// the deployment does: with two workers sharing a daemon, the second to
// start would remove the first's in-flight containers.
func (d *DockerSandbox) ReconcileOrphans(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, reconcileTimeout)
	defer cancel()

	orphans, err := d.cli.ContainerList(ctx, types.ContainerListOptions{
		All:     true, // a stopped orphan still holds its disk and its name
		Filters: filters.NewArgs(filters.Arg("label", SandboxLabel+"="+sandboxLabelValue)),
	})
	if err != nil {
		return 0, fmt.Errorf("list sandbox containers: %w", err)
	}

	removed := 0
	for _, c := range orphans {
		if err := d.cli.ContainerRemove(ctx, c.ID, types.ContainerRemoveOptions{Force: true}); err != nil {
			// Keep going: one container that refuses to die must not stop
			// the rest being reclaimed, and the worker can still judge.
			log.Printf("WARNING: could not remove orphaned sandbox %s: %v", c.ID[:12], err)
			continue
		}
		removed++
	}
	return removed, nil
}

// NewSubmission provisions an isolated container for one submission,
// writes the source code into it, and returns a handle for
// compilation and execution.
func (d *DockerSandbox) NewSubmission(ctx context.Context, language, sourceCode string, limits Limits) (SubmissionSandbox, error) {
	lang, err := GetLanguage(language)
	if err != nil {
		return nil, err
	}

	pidsLimit := int64(256)
	memBytes := limits.MemoryLimitMB * 1024 * 1024

	hostConfig := &container.HostConfig{
		NetworkMode:    "none",
		ReadonlyRootfs: true,
		// Drop every capability and forbid regaining privileges. Without
		// these, a local privilege escalation in any setuid binary would
		// hand user code root inside the container, which is the standing
		// precondition for most published container escapes. nosuid on
		// the writable mounts stops the same trick via a file the program
		// creates itself.
		CapDrop:     []string{"ALL"},
		SecurityOpt: []string{"no-new-privileges"},
		Tmpfs: map[string]string{
			"/home/sandbox": "rw,exec,nosuid,nodev,size=256m,uid=1000,gid=1000",
			"/tmp":          "rw,exec,nosuid,nodev,size=256m,uid=1000,gid=1000",
		},
		Resources: container.Resources{
			Memory:     memBytes,
			MemorySwap: memBytes, // no swap headroom
			NanoCPUs:   1_000_000_000,
			PidsLimit:  &pidsLimit,
		},
	}

	resp, err := d.cli.ContainerCreate(ctx, &container.Config{
		Image: judgeImage,
		Cmd:   []string{"sleep", "infinity"},
		User:  "1000:1000",
		// Every sandbox carries a label so it can be found again after the
		// worker that made it is gone. Without one, a crashed worker's
		// containers are indistinguishable from anything else on the host
		// and nothing can safely clean them up.
		Labels:     map[string]string{SandboxLabel: sandboxLabelValue},
		WorkingDir: "/home/sandbox",
	}, hostConfig, nil, nil, "")
	if err != nil {
		return nil, fmt.Errorf("ContainerCreate failed: %w", err)
	}

	if err := d.cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		d.cli.ContainerRemove(ctx, resp.ID, types.ContainerRemoveOptions{Force: true})
		return nil, fmt.Errorf("ContainerStart failed: %w", err)
	}

	sub := &dockerSubmission{cli: d.cli, containerID: resp.ID, lang: lang, limits: limits}
	if err := sub.writeSource(ctx, sourceCode); err != nil {
		sub.Close(ctx)
		return nil, fmt.Errorf("writeSource failed: %w", err)
	}
	return sub, nil
}

// dockerSubmission is a handle to one running container tied to a
// single user submission.
type dockerSubmission struct {
	cli         *client.Client
	containerID string
	lang        Language
	limits      Limits
}

// writeSource writes the source code into the container's working directory.
//
// It gets its own fixed budget rather than the problem's time limit.
// Copying a file is setup, not the user's program: charging it against a
// 1s limit meant that under load — when Docker's exec round trips slow
// down — the copy timed out and a correct solution was reported as an
// execution failure.
func (s *dockerSubmission) writeSource(ctx context.Context, sourceCode string) error {
	writeCtx, cancel := context.WithTimeout(ctx, sourceWriteTimeout)
	defer cancel()

	res, err := s.execWithCtx(writeCtx, []string{"sh", "-c", fmt.Sprintf("cat > %s", s.lang.SourceFile)}, sourceCode)
	if err != nil {
		return fmt.Errorf("write source: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("write source failed (exit %d): %s", res.ExitCode, res.Stderr)
	}
	return nil
}

// Compile runs the language's compile command inside the container.
// For interpreted languages (CompileCmd == nil), this is a no-op.
func (s *dockerSubmission) Compile(ctx context.Context) (ExecuteResult, error) {
	if s.lang.CompileCmd == nil {
		return ExecuteResult{ExitCode: 0}, nil
	}
	// Give compilers up to 15 seconds, but stay cancellable: deriving
	// from context.Background() meant an abandoned evaluation still
	// burned the full 15s and held its container.
	compileCtx, cancel := context.WithTimeout(ctx, compileTimeout)
	defer cancel()

	return s.execWithCtx(compileCtx, s.lang.CompileCmd, "")
}

// Run executes the compiled/interpreted program with the given stdin.
func (s *dockerSubmission) Run(ctx context.Context, stdin string) (ExecuteResult, error) {
	execCtx, cancel := context.WithTimeout(ctx, s.limits.TimeLimit)
	defer cancel()

	return s.execWithCtx(execCtx, s.lang.RunCmd, stdin)
}

// exec runs a command inside the container with a timeout context.
func (s *dockerSubmission) exec(ctx context.Context, cmd []string, stdin string) (ExecuteResult, error) {
	execCtx, cancel := context.WithTimeout(ctx, s.limits.TimeLimit)
	defer cancel()
	return s.execWithCtx(execCtx, cmd, stdin)
}

func (s *dockerSubmission) execWithCtx(ctx context.Context, cmd []string, stdin string) (ExecuteResult, error) {
	execResp, err := s.cli.ContainerExecCreate(ctx, s.containerID, types.ExecConfig{
		Cmd: cmd,
		Env: []string{"GOCACHE=/tmp/gocache", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/usr/local/go/bin"},
		// stdin is attached unconditionally, even when there is nothing to
		// send. Attaching only for non-empty input made a test case with
		// empty input a different shape of execution from every other one:
		// the program got no stdin at all rather than a stream that is
		// open and immediately at end-of-file. Whether that difference was
		// visible then depended on the language, which is the one thing a
		// judge must never let happen — the verdict has to come from the
		// program, not from how the runtime reacts to a missing fd.
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("exec create: %w", err)
	}

	attach, err := s.cli.ContainerExecAttach(ctx, execResp.ID, types.ExecStartCheck{})
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("exec attach: %w", err)
	}
	defer attach.Close()

	// Write the input and then close the write half, which is what puts
	// the stream at end-of-file. An empty input takes the same path: the
	// program sees an open stdin that immediately reports EOF, exactly as
	// it would with `prog < /dev/null` on any shell.
	go func() {
		if stdin != "" {
			io.Copy(attach.Conn, bytes.NewBufferString(stdin))
		}
		attach.CloseWrite()
	}()

	// Output is streamed out of the container and buffered here, in the
	// judge process — entirely outside the container's memory cgroup. A
	// program that prints in a loop stays well under its own limit while
	// exhausting the worker's heap, so the buffers are capped.
	stdout := newCappedBuffer(maxOutputBytes)
	stderr := newCappedBuffer(maxOutputBytes)
	start := time.Now()
	copyDone := make(chan error, 1)
	copyFinished := make(chan struct{})
	go func() {
		_, cErr := stdcopy.StdCopy(stdout, stderr, attach.Reader)
		copyDone <- cErr
		close(copyFinished)
	}()

	// closedByUs records that the watcher below pulled the connection
	// deliberately, so the resulting read error can be told apart from a
	// genuine streaming failure.
	var closedByUs atomic.Bool

	// Watch the exec process and close the attach connection once it
	// exits, which is what stops StdCopy blocking forever when a child
	// process keeps the output pipe open.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-copyFinished:
				return
			default:
				insp, err := s.cli.ContainerExecInspect(context.Background(), execResp.ID)
				if err == nil && !insp.Running {
					// The process has exited, but output it already wrote
					// may still be in flight. Closing immediately races
					// StdCopy and surfaces as "use of closed network
					// connection" — which previously became a spurious
					// runtime error on a correct solution under load.
					select {
					case <-copyFinished:
					case <-time.After(streamDrainGrace):
						closedByUs.Store(true)
						attach.Close()
					}
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
	}()

	select {
	case <-ctx.Done():
		return ExecuteResult{
			TimedOut: true,
			ExitCode: 124,
			Stderr:   "Command execution timed out",
		}, nil
	case cErr := <-copyDone:
		// A read error caused by our own close is expected: the process
		// had already exited and whatever it wrote has been captured.
		if cErr != nil && !closedByUs.Load() {
			return ExecuteResult{}, fmt.Errorf("stream output: %w", cErr)
		}
	}
	runtimeMS := time.Since(start).Milliseconds()

	inspect, err := s.cli.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("exec inspect: %w", err)
	}

	return ExecuteResult{
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		ExitCode:  inspect.ExitCode,
		RuntimeMS: runtimeMS,
		// Exit 137 is SIGKILL, which the cgroup's OOM killer uses. It is
		// not ambiguous with the wall-clock timeout despite looking like
		// it should be: TimedOut is tracked separately and Judge checks it
		// first, so a run that overran its time is reported as TLE before
		// this field is ever consulted.
		//
		// The container's State.OOMKilled is NOT usable here. User code
		// runs as an exec inside a container whose main process is
		// `sleep infinity`, and that flag describes the main process, so
		// it stays false when the exec'd child is the one killed —
		// verified by the memory-limit containment test, which fails with
		// runtime_error instead of mle if this is switched over.
		OOMKilled: inspect.ExitCode == 137,
		// A program that overran the output cap is reported as failed
		// rather than having a truncated prefix compared against the
		// expected output, which could otherwise read as Wrong Answer or,
		// worse, accidentally match.
		OutputTruncated: stdout.Truncated() || stderr.Truncated(),
	}, nil
}

// Close force-removes the container, releasing all resources.
func (s *dockerSubmission) Close(ctx context.Context) error {
	return s.cli.ContainerRemove(ctx, s.containerID, types.ContainerRemoveOptions{Force: true})
}

// tarSingleFile creates a tar archive containing one file.
func tarSingleFile(name, content string) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(content))}); err != nil {
		return nil, err
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return &buf, nil
}

// Ensure DockerSandbox satisfies the Sandbox interface at compile time.
var _ Sandbox = (*DockerSandbox)(nil)
