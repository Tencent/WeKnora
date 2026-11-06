package sandbox

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newSessionManagerDesktopTestHarness(t *testing.T) (*SessionBoundManager, *desktopFakeClient) {
	return newSessionManagerDesktopHarness(t, true)
}

func newSessionManagerDesktopHarness(t *testing.T, desktopEnabled bool) (*SessionBoundManager, *desktopFakeClient) {
	t.Helper()

	client := &desktopFakeClient{fakeRemoteClient: newFakeRemoteClient(SandboxTypeCube)}
	client.capabilities.SupportsDesktop = true
	cfg := DefaultConfig()
	cfg.CubeTemplate = "tpl-test"
	cfg.DesktopEnabled = desktopEnabled
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

func TestDesktopMachineDoesNotPinASessionOwnedSandbox(t *testing.T) {
	ctx := terminalTestContext()
	mgr, _ := newSessionManagerDesktopTestHarness(t)
	_, err := mgr.ExecShellCommand(ctx, "sess-live", "true", "", time.Second, nil)
	require.NoError(t, err)

	machineID, sandboxID, err := mgr.DesktopMachine(ctx, "sess-live")
	require.NoError(t, err)
	require.Equal(t, "sess-live", machineID)
	require.Empty(t, sandboxID,
		"session mode must resolve, including a stale rebuild, instead of pinning the current id")
}

func TestOpenSessionDesktopNoLiveSandbox(t *testing.T) {
	ctx := terminalTestContext()
	mgr, inner := newSessionManagerDesktopTestHarness(t)

	_, err := mgr.OpenSessionDesktop(ctx, "sess-nobinding", RemoteDesktopOptions{})
	require.ErrorIs(t, err, ErrNoLiveSessionSandbox)
	require.False(t, inner.dialed, "must not dial when there is nothing bound")
}

func TestRequireRunningSessionSandboxRefusesPausedWithoutConnect(t *testing.T) {
	ctx := terminalTestContext()
	mgr, inner := newSessionManagerDesktopTestHarness(t)

	_, err := mgr.ExecShellCommand(ctx, "sess-paused", "true", "", time.Second, nil)
	require.NoError(t, err)
	pauseAllFakeSandboxes(t, inner.fakeRemoteClient)
	connectsBefore := fakeConnectCount(t, inner.fakeRemoteClient)

	err = mgr.RequireRunningSessionSandbox(ctx, "sess-paused")
	require.ErrorIs(t, err, ErrSandboxPaused)
	require.Equal(t, connectsBefore, fakeConnectCount(t, inner.fakeRemoteClient),
		"lookup-only desktop must List a paused sandbox instead of Connect, which would resume it")
}

func TestRequireRunningSessionSandboxAcceptsRunning(t *testing.T) {
	ctx := terminalTestContext()
	mgr, _ := newSessionManagerDesktopTestHarness(t)

	_, err := mgr.ExecShellCommand(ctx, "sess-live", "true", "", time.Second, nil)
	require.NoError(t, err)

	require.NoError(t, mgr.RequireRunningSessionSandbox(ctx, "sess-live"))
}

func TestRequireRunningSessionSandboxNoBindingIsNotPaused(t *testing.T) {
	ctx := terminalTestContext()
	mgr, _ := newSessionManagerDesktopTestHarness(t)

	err := mgr.RequireRunningSessionSandbox(ctx, "sess-missing")
	require.ErrorIs(t, err, ErrNoLiveSessionSandbox)
	require.NotErrorIs(t, err, ErrSandboxPaused)
}

func TestOpenSessionDesktopReportsDialedSandboxID(t *testing.T) {
	// The ID must be the one actually dialled, not the result of a second
	// lookup: a skill install landing between the two would make the
	// handler's rebuild check miss.
	ctx := terminalTestContext()
	mgr, inner := newSessionManagerDesktopTestHarness(t)

	_, err := mgr.ExecShellCommand(ctx, "sess-live", "true", "", time.Second, nil)
	require.NoError(t, err)

	inner.mu.Lock()
	require.Len(t, inner.sandboxes, 1)
	var dialedID string
	for id := range inner.sandboxes {
		dialedID = id
	}
	inner.mu.Unlock()

	got, err := mgr.OpenSessionDesktop(ctx, "sess-live", RemoteDesktopOptions{})
	require.NoError(t, err)
	require.Equal(t, dialedID, got.SandboxID)
	require.True(t, inner.dialed, "DialDesktop must reach the inner client through wrapLangfuse")
}

func TestOpenSessionDesktopWiresTTLRefreshThroughLangfuse(t *testing.T) {
	ctx := terminalTestContext()
	mgr, inner := newSessionManagerDesktopTestHarness(t)
	inner.capabilities.SupportsTimeoutRefresh = true

	_, err := mgr.ExecShellCommand(ctx, "sess-live", "true", "", time.Second, nil)
	require.NoError(t, err)

	got, err := mgr.OpenSessionDesktop(ctx, "sess-live", RemoteDesktopOptions{})
	require.NoError(t, err)
	require.NotNil(t, got.StartTTLRefresh, "wrapping must not hide StartDesktopTTLRefresh")

	got.StartTTLRefresh(ctx)
	require.Equal(t, 1, inner.ttlStarted)
}

func TestSessionDesktopManagerNilWhenDesktopDisabled(t *testing.T) {
	// The provider can relay a desktop, but this config boots a CLI image.
	// Advertising the capability would let the tab Exec ensure.sh and wake
	// the sandbox only to learn the script is missing.
	mgr, _ := newSessionManagerDesktopHarness(t, false)
	require.Nil(t, mgr.SessionDesktopManager(),
		"DesktopEnabled=false must hide the capability before any sandbox exists")
}

func TestDesktopPromptFollowsTheConfigurationSwitch(t *testing.T) {
	on, _ := newSessionManagerDesktopHarness(t, true)
	require.True(t, on.DesktopEnabledForPrompt())
	off, _ := newSessionManagerDesktopHarness(t, false)
	require.False(t, off.DesktopEnabledForPrompt())
	var none *SessionBoundManager
	require.False(t, none.DesktopEnabledForPrompt())
}

func TestOpenSessionDesktopDisabledDoesNotDial(t *testing.T) {
	ctx := terminalTestContext()
	mgr, inner := newSessionManagerDesktopHarness(t, false)

	_, err := mgr.ExecShellCommand(ctx, "sess-live", "true", "", time.Second, nil)
	require.NoError(t, err)

	_, err = mgr.OpenSessionDesktop(ctx, "sess-live", RemoteDesktopOptions{})
	require.ErrorIs(t, err, ErrDesktopUnsupported)
	require.False(t, inner.dialed, "must not dial websockify when the config is not a desktop image")
}

func TestDesktopMachineIsSharedByTheMachinesSessionsAndNamesTheSandbox(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA), userAssignmentFor(userSessionB))
	ctx := ownerCtx()
	_, err := h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
	a, sandboxA, err := h.mgr.DesktopMachine(ctx, userSessionA)
	require.NoError(t, err)
	b, sandboxB, err := h.mgr.DesktopMachine(ctx, userSessionB)
	require.NoError(t, err)
	require.Equal(t, a, b)
	require.Equal(t, sandboxA, sandboxB)
	require.Len(t, a, 64)
}

func TestDesktopMachineChangesWithTheSandbox(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA))
	ctx := ownerCtx()
	_, err := h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
	before, sandboxBefore, err := h.mgr.DesktopMachine(ctx, userSessionA)
	require.NoError(t, err)
	m, _ := userAssignmentFor(userSessionA).UserMachine()
	_, err = h.mgr.bindings.DeleteIfMatch(ctx, h.mgr.UserMachineKey(m), h.client.provider, sandboxBefore)
	require.NoError(t, err)
	// Closed inbound refuses to adopt the leftover machine and will not start
	// a second one while it still exists. Removing it is what lets the next
	// exec create the replacement the slot key must change with.
	require.NoError(t, h.client.Delete(ctx, sandboxBefore))
	_, err = h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
	require.NoError(t, err)
	after, _, err := h.mgr.DesktopMachine(ctx, userSessionA)
	require.NoError(t, err)
	require.NotEqual(t, before, after, "a rebuilt machine gets a new slot key")
}

func TestDesktopMachineWithoutABindingIsNoLiveSandbox(t *testing.T) {
	h := newUserManagerHarness(t, userAssignmentFor(userSessionA))
	_, _, err := h.mgr.DesktopMachine(ownerCtx(), userSessionA)
	require.ErrorIs(t, err, ErrNoLiveSessionSandbox)
}
