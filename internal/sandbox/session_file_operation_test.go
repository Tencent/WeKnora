package sandbox

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSessionFileOperationConnectsOnceAndScopesHandles(t *testing.T) {
	mgr, client := newSessionManagerExecTestHarness(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	_, err := mgr.resolveSession(ctx, "s1")
	require.NoError(t, err)
	scope := WithSessionFileOperation(ctx)
	_, err = mgr.StatSessionFile(scope, "s1", "/tmp/file")
	require.NoError(t, err)
	_, err = mgr.ReadSessionFile(scope, "s1", "/tmp/file")
	require.NoError(t, err)
	require.Len(t, client.connectIDs, 1)

	_, err = mgr.ReadSessionFile(WithSessionFileOperation(scope), "s1", "/tmp/file")
	require.NoError(t, err)
	require.Len(t, client.connectIDs, 2, "a new operation must refresh the connection")

	otherTenant := context.WithValue(scope, types.TenantIDContextKey, uint64(10001))
	_, err = mgr.ReadSessionFile(otherTenant, "s1", "/tmp/file")
	require.Error(t, err, "must not reuse another tenant's sandbox")
	_, err = mgr.ReadSessionFile(scope, "s2", "/tmp/file")
	require.Error(t, err, "must not reuse another session's sandbox")
	otherManager, otherClient := newSessionManagerExecTestHarness(t)
	_, err = otherManager.ReadSessionFile(scope, "s1", "/tmp/file")
	require.Error(t, err, "must not reuse a different backend's handle")
	require.Zero(t, otherClient.createCount)
	require.Equal(t, 1, client.createCount, "file operations must never provision")
}

func TestSessionFileOperationConcurrentReadsConnectOnce(t *testing.T) {
	mgr, client := newSessionManagerExecTestHarness(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	_, err := mgr.resolveSession(ctx, "s1")
	require.NoError(t, err)
	scope := WithSessionFileOperation(ctx)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := mgr.ReadSessionFile(scope, "s1", "/tmp/file")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Len(t, client.connectIDs, 1)
}

func TestSessionFileOperationDoesNotCacheFailures(t *testing.T) {
	mgr, client := newSessionManagerExecTestHarness(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	handle, err := mgr.resolveSession(ctx, "s1")
	require.NoError(t, err)
	scope := WithSessionFileOperation(ctx)
	client.connectErrs[handle.ID()] = NewRemoteError(SandboxTypeCube, "Connect",
		RemoteErrorKindUnavailable, "offline", nil)
	_, err = mgr.ReadSessionFile(scope, "s1", "/tmp/file")
	require.Error(t, err)
	delete(client.connectErrs, handle.ID())
	_, err = mgr.ReadSessionFile(scope, "s1", "/tmp/file")
	require.NoError(t, err)
	require.Len(t, client.connectIDs, 2)
}

func TestExpectedSandboxMaintenanceDoesNotPrepareOrProvision(t *testing.T) {
	mgr, client := newSessionManagerExecTestHarness(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	handle, err := mgr.resolveSession(ctx, "s1")
	require.NoError(t, err)
	result, err := mgr.ExecShellCommandWithOptions(ctx, "s1", "git status", ShellExecOptions{
		ExpectedSandboxID: handle.ID(), SkipWorkspacePrep: true,
		WorkDir: SessionWorkspaceRoot, Timeout: time.Second,
	})
	require.NoError(t, err)
	require.True(t, result.IsSuccess())
	require.Len(t, client.connectIDs, 1)
	require.Empty(t, client.getIDs)
	require.Len(t, client.execRequests, 1)
	require.Equal(t, "git status", client.execRequests[0].Command)

	for _, tc := range []struct{ session, expected string }{
		{"s1", "old-sandbox"}, {"missing-session", handle.ID()},
	} {
		_, err := mgr.ExecShellCommandWithOptions(ctx, tc.session, "git status", ShellExecOptions{
			ExpectedSandboxID: tc.expected, SkipWorkspacePrep: true,
		})
		require.Error(t, err)
	}
	require.Len(t, client.connectIDs, 1, "do not resume a mismatched sandbox")
	require.Len(t, client.execRequests, 1)
	require.Equal(t, 1, client.createCount)

	mgr.checker.(*fakeSessionExistenceChecker).setExists(false)
	_, err = mgr.ExecShellCommandWithOptions(ctx, "s1", "git status", ShellExecOptions{
		ExpectedSandboxID: handle.ID(), SkipWorkspacePrep: true,
	})
	require.ErrorIs(t, err, ErrSandboxSessionDeleted)
	require.Len(t, client.execRequests, 1)
}
