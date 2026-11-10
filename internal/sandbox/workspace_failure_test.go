package sandbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type workspaceFailureClient struct {
	RemoteSandboxClient
	result   *RemoteExecResult
	err      error
	requests []RemoteExecRequest
}

func (c *workspaceFailureClient) Exec(_ context.Context, _ RemoteSandboxHandle, req RemoteExecRequest) (*RemoteExecResult, error) {
	c.requests = append(c.requests, req)
	return c.result, c.err
}

func TestWorkspaceFailureStopsBeforeCommandOrFileWrite(t *testing.T) {
	for _, failure := range []struct {
		name   string
		result *RemoteExecResult
		err    error
	}{
		{"permission", &RemoteExecResult{ExitCode: 1, Stderr: "Permission denied"}, nil},
		{"timeout", &RemoteExecResult{Killed: true}, nil},
		{"transport", nil, errors.New("connection lost")},
		{"empty result", nil, nil},
	} {
		t.Run(failure.name, func(t *testing.T) {
			mgr, client := newSessionManagerExecTestHarness(t)
			failing := &workspaceFailureClient{RemoteSandboxClient: client, result: failure.result, err: failure.err}
			mgr.client = failing
			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
			_, err := mgr.ExecShellCommand(ctx, "session", "echo must-not-run", "/workspace/project", time.Second, nil)
			require.ErrorContains(t, err, "workspace preparation failed")
			err = mgr.WriteSessionWorkspaceFile(ctx, "session", "/workspace/output/result.txt", []byte("must-not-write"))
			require.ErrorContains(t, err, "workspace preparation failed")
			_, err = mgr.Execute(ctx, &ExecuteConfig{SessionID: "session", Script: "test.py", ScriptContent: "print('must-not-run')", SkipValidation: true})
			require.ErrorContains(t, err, "workspace preparation failed")
			require.Empty(t, client.writeFiles)
			require.Len(t, failing.requests, 3)
			for _, req := range failing.requests {
				require.Equal(t, DefaultSandboxExecUser, req.User)
				require.Contains(t, req.Command, "mkdir -p")
				require.NotContains(t, req.Command, "must-not")
			}
		})
	}
}
