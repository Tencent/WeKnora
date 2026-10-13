package sandbox

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type ttlRefreshFakeClient struct {
	*fakeRemoteClient
	refreshMu  sync.Mutex
	refreshIDs []string
	refreshCtx context.Context
}

func (c *ttlRefreshFakeClient) StartDesktopTTLRefresh(ctx context.Context, handle RemoteSandboxHandle) {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	c.refreshIDs = append(c.refreshIDs, handle.ID())
	c.refreshCtx = ctx
}

func newHoldTestManager(t *testing.T, client RemoteSandboxClient) *SessionBoundManager {
	t.Helper()
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
	return mgr
}

func holdTestContext() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
}

func TestHoldSessionSandboxRefusesToReplaceALostSandbox(t *testing.T) {
	mgr, client := newSessionManagerExecTestHarness(t)
	client.capabilities.SupportsSnapshots = true
	ctx := holdTestContext()
	handle, err := mgr.resolveSession(ctx, "s1")
	require.NoError(t, err)

	held, release, err := mgr.HoldSessionSandbox(ctx, "s1")
	require.NoError(t, err)
	defer release()

	_, err = mgr.ExecShellCommandWithOptions(held, "s1", "true", ShellExecOptions{AsRoot: true})
	require.NoError(t, err)

	client.mu.Lock()
	delete(client.sandboxes, handle.ID())
	client.mu.Unlock()

	_, err = mgr.ExecShellCommandWithOptions(held, "s1", "apt-get install -y ffmpeg", ShellExecOptions{AsRoot: true})
	require.ErrorIs(t, err, ErrHeldSandboxLost)
	err = mgr.WriteSessionFile(held, "s1", SkillsImageRoot+"/demo/SKILL.md", []byte("x"))
	require.ErrorIs(t, err, ErrHeldSandboxLost)
	_, err = mgr.ReadSessionFile(held, "s1", SkillsImageRoot+"/demo/SKILL.md")
	require.ErrorIs(t, err, ErrHeldSandboxLost)
	_, err = mgr.CreateSnapshot(held, "s1", "snap")
	require.ErrorIs(t, err, ErrHeldSandboxLost)
	require.Equal(t, 1, client.createCount, "a held sandbox must never be silently replaced")
}

func TestHoldSessionSandboxOnlyPinsItsOwnSession(t *testing.T) {
	mgr, client := newSessionManagerExecTestHarness(t)
	ctx := holdTestContext()
	_, err := mgr.resolveSession(ctx, "s1")
	require.NoError(t, err)
	held, release, err := mgr.HoldSessionSandbox(ctx, "s1")
	require.NoError(t, err)
	defer release()

	_, err = mgr.ExecShellCommandWithOptions(held, "s2", "true", ShellExecOptions{})
	require.NoError(t, err)
	require.Equal(t, 2, client.createCount)
}

func TestHoldSessionSandboxRequiresALiveSandbox(t *testing.T) {
	mgr, client := newSessionManagerExecTestHarness(t)
	_, _, err := mgr.HoldSessionSandbox(holdTestContext(), "s1")
	require.Error(t, err)
	require.Zero(t, client.createCount, "holding must never provision")
}

func TestHoldSessionSandboxRefreshesProviderTTLUntilReleased(t *testing.T) {
	client := &ttlRefreshFakeClient{fakeRemoteClient: newFakeRemoteClient(SandboxTypeCube)}
	client.capabilities.SupportsTimeoutRefresh = true
	mgr := newHoldTestManager(t, client)
	ctx := holdTestContext()
	handle, err := mgr.resolveSession(ctx, "s1")
	require.NoError(t, err)

	_, release, err := mgr.HoldSessionSandbox(ctx, "s1")
	require.NoError(t, err)

	client.refreshMu.Lock()
	require.Equal(t, []string{handle.ID()}, client.refreshIDs)
	refreshCtx := client.refreshCtx
	client.refreshMu.Unlock()
	require.NoError(t, refreshCtx.Err())

	release()
	select {
	case <-refreshCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("release must stop the TTL refresh")
	}
}
