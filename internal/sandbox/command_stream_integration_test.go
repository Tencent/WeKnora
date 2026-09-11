//go:build docker_integration

package sandbox

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestDockerCommandStreamIntegration(t *testing.T) {
	manager := newDockerIntegrationManager(t, dockerIntegrationConfig(t))
	ctx, cancel := context.WithTimeout(types.WithSandboxTenantID(context.Background(), dockerIntegrationTenantID), time.Minute)
	defer cancel()
	sessionID := fmt.Sprintf("browser-stream-%d", time.Now().UnixNano())
	defer func() {
		cleanup, done := context.WithTimeout(types.WithSandboxTenantID(context.Background(), dockerIntegrationTenantID), time.Minute)
		defer done()
		require.NoError(t, manager.DestroySession(cleanup, sessionID))
	}()
	_, err := manager.ExecShellCommand(ctx, sessionID, "true", "", time.Second, nil)
	require.NoError(t, err)
	// More than one Docker stdout chunk, followed by an acknowledgement over
	// the same connection. It must not be buffered until process exit.
	command := `python3 -u -c 'import sys; print("x"*150000); print(sys.stdin.readline().strip())'`
	stream, err := manager.OpenSessionCommandStream(ctx, sessionID, command)
	require.NoError(t, err)
	defer stream.Close()
	var output strings.Builder
	for output.Len() < 150001 {
		select {
		case event, ok := <-stream.Output():
			require.True(t, ok)
			require.NoError(t, event.Err)
			output.Write(event.Data)
		case <-ctx.Done():
			t.Fatal("stream output timed out")
		}
	}
	require.Equal(t, strings.Repeat("x", 150000)+"\n", output.String())
	require.NoError(t, stream.Write(ctx, []byte("ack-7\n")))
	output.Reset()
	for !strings.Contains(output.String(), "ack-7") {
		select {
		case event, ok := <-stream.Output():
			require.True(t, ok)
			require.NoError(t, event.Err)
			output.Write(event.Data)
		case <-ctx.Done():
			t.Fatal("stream input timed out")
		}
	}
}

func TestDockerInstallOutputIntegration(t *testing.T) {
	manager := newDockerIntegrationManager(t, dockerIntegrationConfig(t))
	ctx, cancel := context.WithTimeout(types.WithSandboxTenantID(context.Background(), dockerIntegrationTenantID), time.Minute)
	defer cancel()
	sessionID := fmt.Sprintf("install-output-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(types.WithSandboxTenantID(context.Background(), dockerIntegrationTenantID), time.Minute)
		defer done()
		require.NoError(t, manager.DestroySession(cleanup, sessionID))
	})
	type completion struct {
		result *ExecuteResult
		err    error
	}
	output := make(chan string, 8)
	finished := make(chan completion, 1)
	go func() {
		execCtx := WithCommandOutput(ctx, func(stream string, chunk []byte) {
			output <- stream + ":" + string(chunk)
		})
		result, err := manager.ExecShellCommand(execCtx, sessionID,
			"printf 'download started\\n'; while [ ! -f /tmp/install-output-release ]; do sleep 0.1; done; printf 'download finished\\n' >&2",
			"", 30*time.Second, nil)
		finished <- completion{result, err}
	}()
	select {
	case chunk := <-output:
		require.Contains(t, chunk, "stdout:download started")
	case done := <-finished:
		t.Fatalf("command finished before its live output: %+v", done)
	case <-ctx.Done():
		t.Fatal("no output while command was running")
	}
	// The command cannot exit until we acknowledge its first output through
	// another exec. A buffer-until-exit implementation cannot pass this test.
	released, err := manager.ExecShellCommand(ctx, sessionID, "touch /tmp/install-output-release", "", 10*time.Second, nil)
	require.NoError(t, err)
	require.True(t, released.IsSuccess())
	select {
	case done := <-finished:
		require.NoError(t, done.err)
		require.True(t, done.result.IsSuccess())
		require.Equal(t, "download started\n", done.result.Stdout)
		require.Equal(t, "download finished\n", done.result.Stderr)
	case <-ctx.Done():
		t.Fatal("command did not complete after acknowledgement")
	}
	require.Contains(t, <-output, "stderr:download finished")
}
