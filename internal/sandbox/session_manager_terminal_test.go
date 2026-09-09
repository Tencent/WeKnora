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

func fakeCreateCount(t *testing.T, client *fakeRemoteClient) int {
	t.Helper()
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.createCount
}

func setAllFakeSandboxState(t *testing.T, client *fakeRemoteClient, state RemoteSandboxState) {
	t.Helper()
	client.mu.Lock()
	defer client.mu.Unlock()
	require.NotEmpty(t, client.sandboxes)
	for _, rec := range client.sandboxes {
		rec.state = state
		rec.rawState = string(state)
	}
}

func clearAllFakeSandboxMetadata(t *testing.T, client *fakeRemoteClient) {
	t.Helper()
	client.mu.Lock()
	defer client.mu.Unlock()
	require.NotEmpty(t, client.sandboxes)
	for _, rec := range client.sandboxes {
		rec.metadata = map[string]string{}
	}
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

	createsBefore := fakeCreateCount(t, client.fakeRemoteClient)
	session, err := mgr.OpenSessionTerminal(ctx, "sess-1", RemoteTerminalOptions{AllowResume: true})
	require.NoError(t, err)
	require.NotNil(t, session)
	require.Greater(t, fakeConnectCount(t, client.fakeRemoteClient), connectsBefore)
	require.Equal(t, createsBefore, fakeCreateCount(t, client.fakeRemoteClient),
		"resuming a paused sandbox must Connect, not provision a replacement")
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

func TestOpenSessionTerminalRefusesTransitioningSandboxWithoutResume(t *testing.T) {
	ctx := terminalTestContext()
	mgr, client := newSessionManagerTerminalTestHarness(t)

	_, err := mgr.ExecShellCommand(ctx, "sess-1", "true", "", time.Second, nil)
	require.NoError(t, err)
	setAllFakeSandboxState(t, client.fakeRemoteClient, RemoteStateTransitioning)
	connectsBefore := fakeConnectCount(t, client.fakeRemoteClient)

	_, err = mgr.OpenSessionTerminal(ctx, "sess-1", RemoteTerminalOptions{})
	require.ErrorIs(t, err, ErrSandboxPaused)
	require.Equal(t, connectsBefore, fakeConnectCount(t, client.fakeRemoteClient),
		"pausing/resuming is Transitioning; lookup-only Connect would still resume")
}

func TestOpenSessionTerminalListMissWithBindingIsPausedNotUnbound(t *testing.T) {
	ctx := terminalTestContext()
	mgr, client := newSessionManagerTerminalTestHarness(t)

	_, err := mgr.ExecShellCommand(ctx, "sess-1", "true", "", time.Second, nil)
	require.NoError(t, err)
	clearAllFakeSandboxMetadata(t, client.fakeRemoteClient)
	connectsBefore := fakeConnectCount(t, client.fakeRemoteClient)

	_, err = mgr.OpenSessionTerminal(ctx, "sess-1", RemoteTerminalOptions{})
	require.ErrorIs(t, err, ErrSandboxPaused)
	require.NotErrorIs(t, err, ErrNoLiveSessionSandbox)
	require.Equal(t, connectsBefore, fakeConnectCount(t, client.fakeRemoteClient),
		"a bound sandbox missing from List must not be reported as unbound")
}
