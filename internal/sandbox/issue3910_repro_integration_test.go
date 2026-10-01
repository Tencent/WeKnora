//go:build docker_integration

// Reproduction + regression for GitHub issue #3910.
//
// Issue: calling the agent-chat API failed with
//
//	restore session attachments into sandbox: stage attachment "FAQ知识库.txt":
//	sandbox: create input directory: docker MakeDir: invalid_request:
//	mkdir -p /workspace/input/94088e061da2:
//
// Mechanism under test: every docker filesystem op is wrapped in an
// in-container `timeout -s KILL <N>` (dockerExecCommand). A filesystem op
// that hangs past dockerFilesystemOpTimeout is SIGKILLed — exit != 0 and,
// by definition of SIGKILL, no stderr at all. Pre-fix makeDir classifies
// that as invalid_request with the (empty) stderr as the whole explanation.
//
// The test makes `mkdir` hang deterministically by shadowing it with a
// sleeping script earlier in PATH (/usr/local/bin precedes /usr/bin on the
// sandbox image), so the in-container timeout wrapper kills it exactly the
// way it killed the reporter's mkdir.
//
// Run like the rest of the conformance suite:
//
//	DOCKER_INTEGRATION_IMAGE=wechatopenai/weknora-sandbox:dev \
//	go test -tags=docker_integration ./internal/sandbox \
//	  -run '^TestDocker3910' -count=1 -v -timeout=15m
package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDocker3910KilledMakeDirIntegration(t *testing.T) {
	cfg := dockerIntegrationConfig(t)
	docker, err := NewDockerRemoteClient(cfg)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	handle, err := docker.Create(ctx, RemoteCreateRequest{
		TemplateID: cfg.DockerImage,
		Metadata:   map[string]string{"weknora.test": "issue-3910-repro"},
	})
	require.NoError(t, err, "create sandbox container")
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		_ = docker.Delete(cleanupCtx, handle.ID())
	})

	// Sanity: MakeDir works before the sabotage.
	require.NoError(t, docker.MakeDir(ctx, handle, "/workspace/input/sane"),
		"MakeDir must succeed on a healthy container")

	// Shadow mkdir with a script that hangs (120s >> the 30s filesystem-op
	// timeout). /usr/local/bin precedes /usr/bin in PATH, so the in-container
	// `timeout -s KILL 30 mkdir -p ...` resolves to the sleeper.
	sabotage, err := docker.Exec(ctx, handle, RemoteExecRequest{
		Command: "sh",
		Args: []string{"-c",
			`printf '#!/bin/sh\nsleep 120\n' > /usr/local/bin/mkdir && chmod +x /usr/local/bin/mkdir`},
		User:    "root",
		Timeout: 30 * time.Second,
	})
	require.NoError(t, err, "sabotage exec failed")
	require.Equal(t, 0, sabotage.ExitCode, "sabotage script failed: %s", sabotage.Stderr)

	start := time.Now()
	err = docker.MakeDir(ctx, handle, "/workspace/input/94088e061da2")
	elapsed := time.Since(start)
	require.Error(t, err, "MakeDir against a hung mkdir must fail")
	t.Logf("MakeDir failed after %s with: %q", elapsed.Round(time.Millisecond), err.Error())

	// Fixed behavior: the SIGKILLed filesystem op is a Timeout (retryable,
	// binding preserved), not an invalid_request, and the message carries the
	// evidence a killed process leaves behind — exit code and elapsed time —
	// instead of the dangling colon from #3910.
	require.False(t, IsRemoteInvalidRequest(err),
		"a killed filesystem op is not the caller's fault: got %q", err.Error())
	require.True(t, IsRemoteTimeout(err),
		"a killed filesystem op must classify as Timeout: got %q", err.Error())
	require.Contains(t, err.Error(), "killed after",
		"message must say the op was killed: %q", err.Error())
	require.Regexp(t, `exit=1(24|37)`, err.Error(),
		"message must carry the exit code: %q", err.Error())
	require.NotRegexp(t, `: $`, err.Error(),
		"message must not end in a dangling colon: %q", err.Error())

	// The failed MakeDir must not poison the sandbox: a subsequent healthy
	// operation still works (the binding was preserved, not dropped).
	recovered, err := docker.Exec(ctx, handle, RemoteExecRequest{
		Command: "sh",
		Args:    []string{"-c", `rm -f /usr/local/bin/mkdir`},
		User:    "root",
		Timeout: 30 * time.Second,
	})
	require.NoError(t, err)
	require.Equal(t, 0, recovered.ExitCode)
	require.NoError(t, docker.MakeDir(ctx, handle, "/workspace/input/recovered"),
		"sandbox must stay usable after a killed filesystem op")
}
