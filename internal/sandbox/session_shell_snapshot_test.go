package sandbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type shellSnapshotClient struct {
	*fakeRemoteClient
	ops         []string
	handles     []string
	listCalls   int
	execCalls   int
	listErrorAt int
	listErr     error
	prepError   bool
	command     *RemoteExecResult
	directories map[string][]RemoteDirEntry
}

func (c *shellSnapshotClient) ConnectSession(
	ctx context.Context, req RemoteConnectRequest,
) (RemoteSandboxHandle, error) {
	c.ops = append(c.ops, "connect")
	return c.Connect(ctx, req)
}

func (c *shellSnapshotClient) Stat(context.Context, RemoteSandboxHandle, string) (*RemoteStatEntry, error) {
	c.ops = append(c.ops, "stat")
	return nil, errors.New("snapshot must not stat")
}

func (c *shellSnapshotClient) ListDir(
	ctx context.Context, handle RemoteSandboxHandle, dir string,
) ([]RemoteDirEntry, error) {
	c.ops = append(c.ops, "list")
	c.handles = append(c.handles, handle.ID())
	c.listCalls++
	if c.listCalls == c.listErrorAt {
		return nil, c.listErr
	}
	if c.directories != nil {
		return c.directories[dir], ctx.Err()
	}
	return []RemoteDirEntry{{Path: dir + "/report.txt", Type: RemoteEntryFile, Size: int64(c.listCalls)}}, ctx.Err()
}

func (c *shellSnapshotClient) Exec(
	ctx context.Context, handle RemoteSandboxHandle, req RemoteExecRequest,
) (*RemoteExecResult, error) {
	c.ops = append(c.ops, "exec")
	c.handles = append(c.handles, handle.ID())
	c.execCalls++
	if c.execCalls == 1 && c.prepError {
		return &RemoteExecResult{ExitCode: 1, Stderr: "permission denied"}, nil
	}
	if c.execCalls == 2 && c.command != nil {
		if req.OnOutput != nil {
			req.OnOutput("stdout", []byte(c.command.Stdout))
		}
		return c.command, nil
	}
	return c.fakeRemoteClient.Exec(ctx, handle, req)
}

func newShellSnapshotHarness(t *testing.T) (context.Context, *SessionBoundManager, *shellSnapshotClient) {
	t.Helper()
	client := &shellSnapshotClient{fakeRemoteClient: newFakeRemoteClient(SandboxTypeCube)}
	cfg := DefaultConfig()
	cfg.CubeTemplate = "tpl-test"
	mgr, err := NewSessionBoundManager(SessionBoundManagerConfig{
		Config: cfg, Client: client, Store: NewMemorySessionSandboxBindingStore(),
		Checker: &fakeSessionExistenceChecker{exists: true}, SkipHealthProbe: true,
	})
	require.NoError(t, err)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	_, err = mgr.resolveSession(ctx, "sess-1")
	require.NoError(t, err)
	return ctx, mgr, client
}

func TestShellOutputSnapshotUsesOneHandleAndFiveOperations(t *testing.T) {
	ctx, mgr, client := newShellSnapshotHarness(t)
	client.command = &RemoteExecResult{Stdout: "hello", Stderr: "warning", ExitCode: 7, Duration: time.Millisecond}
	var streamed string
	result, snapshot, err := mgr.ExecShellCommandWithOutputSnapshot(ctx, "sess-1", "echo hello", ShellExecOptions{
		OnOutput: func(_ string, p []byte) { streamed += string(p) },
	}, SessionOutputRoot)
	require.NoError(t, err)
	require.Equal(t, []string{"connect", "list", "exec", "exec", "list"}, client.ops)
	require.Empty(t, client.getIDs, "combined connector must not be preceded by Get")
	require.Len(t, client.connectIDs, 1)
	for _, id := range client.handles {
		require.Equal(t, client.connectIDs[0], id)
	}
	require.Equal(t, "hello", streamed)
	require.Equal(t, 7, result.ExitCode)
	require.Equal(t, "warning", result.Stderr)
	require.Equal(t, time.Millisecond, result.Duration)
	require.NotNil(t, snapshot)
	require.Equal(t, int64(1), snapshot.Before[0].Size)
	require.Equal(t, int64(2), snapshot.After[0].Size)

	_, _, err = mgr.ExecShellCommandWithOutputSnapshot(ctx, "sess-1", "true", ShellExecOptions{}, SessionOutputRoot)
	require.NoError(t, err)
	require.Len(t, client.connectIDs, 2, "handles must not be cached across tool calls")
}

func TestShellOutputSnapshotFailuresDoNotReplayCommand(t *testing.T) {
	for _, tc := range []struct {
		name       string
		failAt     int
		listErr    error
		wantLists  int
		wantResult bool
	}{
		{"before unavailable", 1, errors.New("offline"), 1, false},
		{"after unavailable", 2, errors.New("offline"), 2, false},
		{
			"missing root", 1,
			NewRemoteError(SandboxTypeCube, "ListDir", RemoteErrorKindNotFound, "missing", nil), 2, true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, mgr, client := newShellSnapshotHarness(t)
			client.listErrorAt, client.listErr = tc.failAt, tc.listErr
			result, snapshot, err := mgr.ExecShellCommandWithOutputSnapshot(
				ctx, "sess-1", "true", ShellExecOptions{}, SessionOutputRoot,
			)
			require.NoError(t, err)
			require.True(t, result.IsSuccess())
			require.Equal(t, tc.wantResult, snapshot != nil)
			require.Equal(t, 2, client.execCalls, "one preparation and one command, never retried")
			require.Equal(t, tc.wantLists, client.listCalls)
			if snapshot != nil {
				require.Empty(t, snapshot.Before)
				require.Len(t, snapshot.After, 1)
			}
		})
	}
}

func TestShellOutputSnapshotPreparationFailureDoesNotStartCommand(t *testing.T) {
	ctx, mgr, client := newShellSnapshotHarness(t)
	client.prepError = true
	result, snapshot, err := mgr.ExecShellCommandWithOutputSnapshot(
		ctx, "sess-1", "true", ShellExecOptions{}, SessionOutputRoot,
	)
	require.ErrorContains(t, err, "Command was not started")
	require.Nil(t, result)
	require.Nil(t, snapshot)
	require.Equal(t, 1, client.execCalls)
}

func TestShellOutputSnapshotPreservesTimeout(t *testing.T) {
	ctx, mgr, client := newShellSnapshotHarness(t)
	client.command = &RemoteExecResult{Stdout: "partial", Killed: true}
	result, snapshot, err := mgr.ExecShellCommandWithOutputSnapshot(
		ctx, "sess-1", "sleep 10", ShellExecOptions{}, SessionOutputRoot,
	)
	require.NoError(t, err)
	require.True(t, result.Killed)
	require.Equal(t, ErrTimeout.Error(), result.Error)
	require.Equal(t, "partial", result.Stdout)
	require.NotNil(t, snapshot)
	require.Equal(t, 2, client.execCalls)
}

func TestShellOutputSnapshotRecursesWithoutStat(t *testing.T) {
	ctx, mgr, client := newShellSnapshotHarness(t)
	client.directories = map[string][]RemoteDirEntry{
		"/workspace/artifacts":        {{Name: "nested", Type: RemoteEntryDir}},
		"/workspace/artifacts/nested": {{Name: "report.txt", Type: RemoteEntryFile, Size: 10}},
	}
	_, snapshot, err := mgr.ExecShellCommandWithOutputSnapshot(
		ctx, "sess-1", "true", ShellExecOptions{}, "/workspace/artifacts",
	)
	require.NoError(t, err)
	require.NotNil(t, snapshot)
	require.Len(t, snapshot.Before, 1)
	require.Equal(t, "/workspace/artifacts/nested/report.txt", snapshot.Before[0].Path)
	require.Equal(t, snapshot.Before, snapshot.After)
	require.Equal(t, []string{"connect", "list", "list", "exec", "exec", "list", "list"}, client.ops)
}

func TestShellOutputSnapshotRejectsPartialRecursiveListing(t *testing.T) {
	ctx, mgr, client := newShellSnapshotHarness(t)
	client.directories = map[string][]RemoteDirEntry{
		SessionOutputRoot: {
			{Name: "old.txt", Type: RemoteEntryFile},
			{Name: "nested", Type: RemoteEntryDir},
		},
	}
	client.listErrorAt = 2
	client.listErr = NewRemoteError(SandboxTypeCube, "ListDir", RemoteErrorKindNotFound,
		"subdirectory disappeared", nil)
	result, snapshot, err := mgr.ExecShellCommandWithOutputSnapshot(
		ctx, "sess-1", "true", ShellExecOptions{}, SessionOutputRoot,
	)
	require.NoError(t, err)
	require.True(t, result.IsSuccess())
	require.Nil(t, snapshot, "a partial baseline must not label old files as new outputs")
	require.Equal(t, 2, client.execCalls)
	require.Equal(t, 2, client.listCalls)
}
