package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

func newSessionManagerTerminalTestHarness(t *testing.T) (*SessionBoundManager, *terminalFakeClient) {
	t.Helper()

	client := &terminalFakeClient{fakeRemoteClient: newFakeRemoteClient(SandboxTypeCube)}
	client.capabilities.SupportsTerminals = true
	cfg := DefaultConfig()
	cfg.CubeTemplate = "tpl-test"
	mgr, err := NewSessionBoundManager(SessionBoundManagerConfig{
		Config:          cfg,
		Client:          client,
		Store:           NewMemorySessionSandboxBindingStore(),
		Checker:         &fakeSessionExistenceChecker{exists: true},
		SkipHealthProbe: true,
	})
	require.NoError(t, err)
	return mgr, client
}

func terminalTestContext() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
}

func pauseAllFakeSandboxes(t *testing.T, client *fakeRemoteClient) {
	t.Helper()
	client.mu.Lock()
	defer client.mu.Unlock()
	require.NotEmpty(t, client.sandboxes)
	for _, rec := range client.sandboxes {
		rec.state = RemoteStatePaused
	}
}

func fakeConnectCount(t *testing.T, client *fakeRemoteClient) int {
	t.Helper()
	client.mu.Lock()
	defer client.mu.Unlock()
	return len(client.connectIDs)
}

func TestOpenSessionTerminalRefusesPausedSandboxWithoutResume(t *testing.T) {
	ctx := terminalTestContext()
	mgr, client := newSessionManagerTerminalTestHarness(t)

	_, err := mgr.ExecShellCommand(ctx, "sess-1", "true", "", time.Second, nil)
	require.NoError(t, err)
	pauseAllFakeSandboxes(t, client.fakeRemoteClient)
	connectsBefore := fakeConnectCount(t, client.fakeRemoteClient)

	_, err = mgr.OpenSessionTerminal(ctx, "sess-1", RemoteTerminalOptions{})
	require.ErrorIs(t, err, ErrSandboxPaused)
	require.Equal(t, connectsBefore, fakeConnectCount(t, client.fakeRemoteClient),
		"lookup-only open must List a paused sandbox instead of Connect, which would resume it")
}

func TestOpenSessionTerminalResumesPausedSandboxWhenAllowed(t *testing.T) {
	ctx := terminalTestContext()
	mgr, client := newSessionManagerTerminalTestHarness(t)

	_, err := mgr.ExecShellCommand(ctx, "sess-1", "true", "", time.Second, nil)
	require.NoError(t, err)
	pauseAllFakeSandboxes(t, client.fakeRemoteClient)
	connectsBefore := fakeConnectCount(t, client.fakeRemoteClient)

	session, err := mgr.OpenSessionTerminal(ctx, "sess-1", RemoteTerminalOptions{AllowResume: true})
	require.NoError(t, err)
	require.NotNil(t, session)
	require.Greater(t, fakeConnectCount(t, client.fakeRemoteClient), connectsBefore)
}

func TestOpenSessionTerminalConnectsRunningSandbox(t *testing.T) {
	ctx := terminalTestContext()
	mgr, _ := newSessionManagerTerminalTestHarness(t)

	_, err := mgr.ExecShellCommand(ctx, "sess-1", "true", "", time.Second, nil)
	require.NoError(t, err)

	session, err := mgr.OpenSessionTerminal(ctx, "sess-1", RemoteTerminalOptions{})
	require.NoError(t, err)
	require.NotNil(t, session)
}

func TestOpenSessionTerminalNoBindingIsNotPaused(t *testing.T) {
	ctx := terminalTestContext()
	mgr, _ := newSessionManagerTerminalTestHarness(t)

	_, err := mgr.OpenSessionTerminal(ctx, "sess-missing", RemoteTerminalOptions{})
	require.ErrorIs(t, err, ErrNoLiveSessionSandbox)
	require.NotErrorIs(t, err, ErrSandboxPaused)
}
