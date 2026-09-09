package sandbox

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBrowserPreviewDoesNotCreateOrResumeSandbox(t *testing.T) {
	ctx := terminalTestContext()
	mgr, client := newSessionManagerTerminalTestHarness(t)
	_, err := mgr.ExecLiveSessionCommand(ctx, "sess-1", "true", time.Second)
	require.ErrorIs(t, err, ErrNoLiveSessionSandbox)
	require.Zero(t, fakeCreateCount(t, client.fakeRemoteClient))
	_, err = mgr.ExecShellCommand(ctx, "sess-1", "true", "", time.Second, nil)
	require.NoError(t, err)
	pauseAllFakeSandboxes(t, client.fakeRemoteClient)
	before := fakeConnectCount(t, client.fakeRemoteClient)
	_, err = mgr.ExecLiveSessionCommand(ctx, "sess-1", "true", time.Second)
	require.ErrorIs(t, err, ErrSandboxPaused)
	require.Equal(t, before, fakeConnectCount(t, client.fakeRemoteClient))
	require.Equal(t, 1, fakeCreateCount(t, client.fakeRemoteClient))
}
