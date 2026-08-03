package judge

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

const judgeImage = "codearena/judge-sandbox:latest"

// DockerSandbox implements the Sandbox interface using real Docker
// containers for isolated code execution.
type DockerSandbox struct{ cli *client.Client }

// NewDockerSandbox creates a DockerSandbox backed by the local Docker daemon.
func NewDockerSandbox() (*DockerSandbox, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &DockerSandbox{cli: cli}, nil
}

// NewSubmission provisions an isolated container for one submission,
// writes the source code into it, and returns a handle for
// compilation and execution.
func (d *DockerSandbox) NewSubmission(ctx context.Context, language, sourceCode string, limits Limits) (SubmissionSandbox, error) {
	lang, err := GetLanguage(language)
	if err != nil {
		return nil, err
	}

	pidsLimit := int64(64)
	memBytes := limits.MemoryLimitMB * 1024 * 1024

	hostConfig := &container.HostConfig{
		NetworkMode:    "none",
		ReadonlyRootfs: true,
		Tmpfs:          map[string]string{"/home/sandbox": "rw,size=64m,uid=1000,gid=1000"},
		Resources: container.Resources{
			Memory:     memBytes,
			MemorySwap: memBytes, // no swap headroom
			NanoCPUs:   1_000_000_000,
			PidsLimit:  &pidsLimit,
		},
	}

	resp, err := d.cli.ContainerCreate(ctx, &container.Config{
		Image:      judgeImage,
		Cmd:        []string{"sleep", "infinity"},
		User:       "1000:1000",
		WorkingDir: "/home/sandbox",
	}, hostConfig, nil, nil, "")
	if err != nil {
		return nil, fmt.Errorf("create container: %w", err)
	}

	if err := d.cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		return nil, fmt.Errorf("start container: %w", err)
	}

	sub := &dockerSubmission{cli: d.cli, containerID: resp.ID, lang: lang, limits: limits}
	if err := sub.writeSource(ctx, sourceCode); err != nil {
		sub.Close(ctx)
		return nil, err
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

// writeSource archives the source code into a tar stream and copies it
// into the container's working directory.
func (s *dockerSubmission) writeSource(ctx context.Context, sourceCode string) error {
	tarBuf, err := tarSingleFile(s.lang.SourceFile, sourceCode)
	if err != nil {
		return err
	}
	return s.cli.CopyToContainer(ctx, s.containerID, "/home/sandbox", tarBuf, types.CopyToContainerOptions{})
}

// Compile runs the language's compile command inside the container.
// For interpreted languages (CompileCmd == nil), this is a no-op.
func (s *dockerSubmission) Compile(ctx context.Context) (ExecuteResult, error) {
	if s.lang.CompileCmd == nil {
		return ExecuteResult{ExitCode: 0}, nil
	}
	return s.exec(ctx, s.lang.CompileCmd, "")
}

// Run executes the compiled/interpreted program with the given stdin.
func (s *dockerSubmission) Run(ctx context.Context, stdin string) (ExecuteResult, error) {
	return s.exec(ctx, s.lang.RunCmd, stdin)
}

// exec runs a command inside the container with a wall-clock timeout.
func (s *dockerSubmission) exec(ctx context.Context, cmd []string, stdin string) (ExecuteResult, error) {
	execCtx, cancel := context.WithTimeout(ctx, s.limits.TimeLimit)
	defer cancel()

	execResp, err := s.cli.ContainerExecCreate(ctx, s.containerID, types.ExecConfig{
		Cmd:          cmd,
		AttachStdin:  stdin != "",
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

	if stdin != "" {
		go func() {
			io.Copy(attach.Conn, bytes.NewBufferString(stdin))
			attach.CloseWrite()
		}()
	}

	var stdout, stderr bytes.Buffer
	start := time.Now()
	copyDone := make(chan error, 1)
	go func() {
		_, cErr := stdcopy.StdCopy(&stdout, &stderr, attach.Reader)
		copyDone <- cErr
	}()

	select {
	case <-execCtx.Done():
		return ExecuteResult{TimedOut: true}, nil
	case cErr := <-copyDone:
		if cErr != nil {
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
		OOMKilled: inspect.ExitCode == 137,
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
