//go:build docker_integration

package session

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// TestTerminalAuditDockerPTYIntegration proves the production Bash hook and
// marker parser against a real Docker Engine TTY exec. It deliberately mounts
// the checked-out prompt script over the image copy so the test covers this
// commit before a new sandbox image is published.
func TestTerminalAuditDockerPTYIntegration(t *testing.T) {
	image := strings.TrimSpace(os.Getenv("DOCKER_INTEGRATION_IMAGE"))
	if image == "" {
		t.Skip("DOCKER_INTEGRATION_IMAGE is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cli, err := client.New(client.FromEnv)
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	defer cli.Close()
	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		t.Skipf("docker daemon unreachable: %v", err)
	}

	promptPath, err := filepath.Abs(filepath.Join("..", "..", "..", "docker", "sandbox-pty-prompt.sh"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{Image: image, Cmd: []string{"sleep", "300"}},
		HostConfig: &container.HostConfig{
			Binds: []string{filepath.ToSlash(promptPath) + ":/etc/weknora/pty-prompt.sh:ro"},
		},
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	defer func() {
		_, _ = cli.ContainerRemove(context.Background(), created.ID, client.ContainerRemoveOptions{Force: true})
	}()
	if _, err := cli.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		t.Fatalf("start container: %v", err)
	}

	token := "real-docker-audit-token"
	envMap := terminalAuditEnvironment(token)
	envs := make([]string, 0, len(envMap))
	for key, value := range envMap {
		envs = append(envs, key+"="+value)
	}
	sort.Strings(envs)
	execResult, err := cli.ExecCreate(ctx, created.ID, client.ExecCreateOptions{
		TTY: true, AttachStdin: true, AttachStdout: true, AttachStderr: true,
		WorkingDir: "/workspace", User: "root", Env: envs,
		Cmd: []string{"/bin/sh", "-c", "exec /bin/bash -il"},
	})
	if err != nil {
		t.Fatalf("create TTY exec: %v", err)
	}
	attached, err := cli.ExecAttach(ctx, execResult.ID, client.ExecAttachOptions{TTY: true})
	if err != nil {
		t.Fatalf("attach TTY exec: %v", err)
	}
	defer attached.Close()

	type observed struct {
		exitCode int
		command  string
	}
	records := make(chan observed, 4)
	filter := newTerminalAuditMarkerFilter(token, func(exitCode int, command string) {
		records <- observed{exitCode: exitCode, command: command}
	})
	readErr := make(chan error, 1)
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, err := attached.Reader.Read(buffer)
			if n > 0 {
				filtered := filter.Consume(buffer[:n])
				if bytes.Contains(filtered, []byte(token)) {
					readErr <- context.Canceled
					return
				}
			}
			if err != nil {
				readErr <- err
				return
			}
		}
	}()
	commands := "true\nfalse\nprintf '审计 ANSI \\033[31m红\\033[0m\\n'\nexport API_TOKEN=real-secret\n"
	if _, err := attached.Conn.Write([]byte(commands)); err != nil {
		t.Fatalf("write TTY commands: %v", err)
	}

	got := make([]observed, 0, 4)
	for len(got) < 4 {
		select {
		case record := <-records:
			got = append(got, record)
		case err := <-readErr:
			t.Fatalf("TTY stream ended before audit markers: %v", err)
		case <-ctx.Done():
			t.Fatalf("timed out waiting for audit markers: %v", ctx.Err())
		}
	}
	if got[0].command != "true" || got[0].exitCode != 0 ||
		got[1].command != "false" || got[1].exitCode != 1 {
		t.Fatalf("unexpected command statuses: %#v", got)
	}
	if !strings.Contains(got[2].command, "审计 ANSI") {
		t.Fatalf("Unicode command was not preserved: %#v", got[2])
	}
	if strings.Contains(got[3].command, "real-secret") ||
		!strings.Contains(got[3].command, "[REDACTED]") {
		t.Fatalf("secret assignment was not redacted")
	}
	_, _ = attached.Conn.Write([]byte("exit\n"))
}
