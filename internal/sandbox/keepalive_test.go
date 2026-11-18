package sandbox

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

type keepAliveCall struct {
	sandboxID string
	ttl       time.Duration
}

// keepAliveFakeClient adds the keep-alive capability to the shared fake.
type keepAliveFakeClient struct {
	*fakeRemoteClient
	keepMu    sync.Mutex
	keepCalls []keepAliveCall
	keepErr   error
}

func (c *keepAliveFakeClient) KeepAlive(_ context.Context, handle RemoteSandboxHandle, ttl time.Duration) error {
	c.keepMu.Lock()
	defer c.keepMu.Unlock()
	id := ""
	if handle != nil {
		id = handle.ID()
	}
	c.keepCalls = append(c.keepCalls, keepAliveCall{sandboxID: id, ttl: ttl})
	return c.keepErr
}

func (c *keepAliveFakeClient) calls() []keepAliveCall {
	c.keepMu.Lock()
	defer c.keepMu.Unlock()
	return append([]keepAliveCall(nil), c.keepCalls...)
}

func pausingCreateRequest() RemoteCreateRequest {
	return RemoteCreateRequest{
		TemplateID: "template-a",
		Timeout: RemoteTimeoutPolicy{
			Mode: RemoteTimeoutExplicit, Value: 15 * time.Minute,
			Action: RemoteOnTimeoutPause, AutoResume: true,
		},
	}
}

func TestCubeKeepAliveRefreshesTimeout(t *testing.T) {
	mock := newCubeMockServer(t)
	client := newTestCubeRemoteClient(t, mock)
	handle, err := client.Create(context.Background(), pausingCreateRequest())
	require.NoError(t, err)

	before := mock.timeoutPOSTs.Load()
	require.NoError(t, client.KeepAlive(context.Background(), handle, 30*time.Minute))
	require.Equal(t, before+1, mock.timeoutPOSTs.Load())
}

func TestE2BKeepAliveRefreshesTimeout(t *testing.T) {
	mock := newE2BMockServer(t)
	client := newTestE2BRemoteClient(t, mock)
	handle, err := client.Create(context.Background(), pausingCreateRequest())
	require.NoError(t, err)

	before := mock.timeoutPOSTs.Load()
	require.NoError(t, client.KeepAlive(context.Background(), handle, 5*time.Minute))
	require.Equal(t, before+1, mock.timeoutPOSTs.Load())
}

func TestDockerKeepAliveRunsANoOpExecThatTouchesTheActivityMarker(t *testing.T) {
	engine := newFakeDockerEngine()
	docker := newTestDockerClient(t, engine)

	require.NoError(t, docker.KeepAlive(context.Background(), testHandle("container-1"), time.Minute))

	require.Len(t, engine.execOptions, 1)
	cmd := engine.execOptions[0].Cmd
	require.Contains(t, cmd[2], dockerActivityMarker)
	require.Equal(t, []string{"weknora-exec", "true"}, cmd[3:])
}

func TestKeepAliverFromSeesThroughLangfuse(t *testing.T) {
	inner := &keepAliveFakeClient{fakeRemoteClient: newFakeRemoteClient(SandboxTypeCube)}
	keeper, ok := keepAliverFrom(wrapLangfuseRemoteClient(inner))
	require.True(t, ok)
	require.NoError(t, keeper.KeepAlive(context.Background(), nil, time.Minute))
	require.Equal(t, []keepAliveCall{{ttl: time.Minute}}, inner.calls())
}

func TestKeepAliverFromRejectsClientWithoutTheCapability(t *testing.T) {
	_, ok := keepAliverFrom(wrapLangfuseRemoteClient(newFakeRemoteClient(SandboxTypeCube)))
	require.False(t, ok, "wrapping must not invent keep-alive support")
	_, ok = keepAliverFrom(nil)
	require.False(t, ok)
}

const keepAliveTestTTL = 12 * time.Minute

func newKeepAliveTestManager(t *testing.T, client RemoteSandboxClient) *SessionBoundManager {
	t.Helper()
	cfg := DefaultConfig()
	cfg.CubeTemplate = "tpl-test"
	cfg.CubeSandboxTTL = keepAliveTestTTL
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

func keepAliveTestCtx() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
}

func bindKeepAliveTestSession(t *testing.T, mgr *SessionBoundManager, sessionID string) string {
	t.Helper()
	ctx := keepAliveTestCtx()
	_, err := mgr.Execute(ctx, &ExecuteConfig{
		SessionID: sessionID, SkipValidation: true, ScriptContent: "print('ok')\n", Script: "hello.py",
	})
	require.NoError(t, err)
	id, ok := mgr.BoundSandboxID(ctx, sessionID)
	require.True(t, ok)
	return id
}

func TestKeepAliveSessionRefreshesTheBoundSandbox(t *testing.T) {
	client := &keepAliveFakeClient{fakeRemoteClient: newFakeRemoteClient(SandboxTypeCube)}
	mgr := newKeepAliveTestManager(t, client)
	sandboxID := bindKeepAliveTestSession(t, mgr, "s1")

	next, err := mgr.KeepAliveSession(keepAliveTestCtx(), "s1")

	require.NoError(t, err)
	require.Equal(t, keepAliveTestTTL/3, next)
	require.Equal(t, []keepAliveCall{{sandboxID: sandboxID, ttl: keepAliveTestTTL}}, client.calls())
}

func TestKeepAliveSessionNeverProvisions(t *testing.T) {
	client := &keepAliveFakeClient{fakeRemoteClient: newFakeRemoteClient(SandboxTypeCube)}
	mgr := newKeepAliveTestManager(t, client)

	next, err := mgr.KeepAliveSession(keepAliveTestCtx(), "unbound")

	require.NoError(t, err)
	require.Zero(t, next)
	require.Empty(t, client.calls())
	creates, _, _, _, _ := client.counts()
	require.Zero(t, creates, "keep-alive must never create a sandbox")
}

func TestKeepAliveSessionWithoutProviderSupportIsNoOp(t *testing.T) {
	client := newFakeRemoteClient(SandboxTypeCube)
	mgr := newKeepAliveTestManager(t, client)
	bindKeepAliveTestSession(t, mgr, "s1")
	_, connectsBefore, _, _, _ := client.counts()

	next, err := mgr.KeepAliveSession(keepAliveTestCtx(), "s1")

	require.NoError(t, err)
	require.Zero(t, next)
	_, connectsAfter, _, _, _ := client.counts()
	require.Equal(t, connectsBefore, connectsAfter, "nothing to refresh, so nothing to connect to")
}

func TestKeepAliveSessionExpectedRefreshesOnlyTheMatchingSandbox(t *testing.T) {
	client := &keepAliveFakeClient{fakeRemoteClient: newFakeRemoteClient(SandboxTypeCube)}
	mgr := newKeepAliveTestManager(t, client)
	sandboxID := bindKeepAliveTestSession(t, mgr, "s1")

	next, err := mgr.KeepAliveSessionExpected(keepAliveTestCtx(), "s1", sandboxID)

	require.NoError(t, err)
	require.Equal(t, keepAliveTestTTL/3, next)
	require.Equal(t, []keepAliveCall{{sandboxID: sandboxID, ttl: keepAliveTestTTL}}, client.calls())
}

func TestKeepAliveSessionExpectedDoesNotRefreshADifferentSandbox(t *testing.T) {
	client := &keepAliveFakeClient{fakeRemoteClient: newFakeRemoteClient(SandboxTypeCube)}
	mgr := newKeepAliveTestManager(t, client)
	bindKeepAliveTestSession(t, mgr, "s1")

	next, err := mgr.KeepAliveSessionExpected(keepAliveTestCtx(), "s1", "sbx-other")

	require.NoError(t, err)
	require.Zero(t, next)
	require.Empty(t, client.calls(), "a different sandbox is not refreshed and not replaced")
}

func TestKeepAliveSessionRefreshesSharedUserMachine(t *testing.T) {
	for _, expected := range []bool{false, true} {
		t.Run(map[bool]string{false: "session", true: "expected"}[expected], func(t *testing.T) {
			h := newUserManagerHarness(t, userAssignmentFor(userSessionA))
			ctx := ownerCtx()
			_, err := h.mgr.ExecShellCommand(ctx, userSessionA, "true", "", time.Second, nil)
			require.NoError(t, err)
			id, found := h.mgr.BoundSandboxID(ctx, userSessionA)
			require.True(t, found)
			keeper := &keepAliveFakeClient{fakeRemoteClient: h.client}
			h.mgr.client = wrapLangfuseRemoteClient(keeper)
			creates, _, _, _, _ := keeper.counts()

			var next time.Duration
			if expected {
				next, err = h.mgr.KeepAliveSessionExpected(ctx, userSessionA, id)
			} else {
				next, err = h.mgr.KeepAliveSession(ctx, userSessionA)
			}
			require.NoError(t, err)
			require.Positive(t, next)
			require.Equal(t, []keepAliveCall{{sandboxID: id, ttl: next * keepAliveRefreshDivisor}}, keeper.calls())
			after, _, _, _, _ := keeper.counts()
			require.Equal(t, creates, after, "keeping a shared machine alive must not create a session sandbox")

			next, err = h.mgr.KeepAliveSessionExpected(ctx, userSessionA, "another-machine")
			require.NoError(t, err)
			require.Zero(t, next)
			require.Len(t, keeper.calls(), 1, "a mismatched shared binding must not be refreshed")
		})
	}
}

func TestKeepAliveSessionReportsProviderFailure(t *testing.T) {
	client := &keepAliveFakeClient{fakeRemoteClient: newFakeRemoteClient(SandboxTypeCube)}
	client.keepErr = errors.New("provider down")
	mgr := newKeepAliveTestManager(t, client)
	bindKeepAliveTestSession(t, mgr, "s1")

	next, err := mgr.KeepAliveSession(keepAliveTestCtx(), "s1")

	require.ErrorContains(t, err, "provider down")
	require.Zero(t, next)
}
