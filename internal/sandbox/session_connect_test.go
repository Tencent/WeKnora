package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/require"
)

func TestCubeSessionConnectReusesProbeHandle(t *testing.T) {
	mock := newCubeMockServer(t)
	client := newTestCubeRemoteClient(t, mock)
	ctx := context.Background()
	created, err := client.Create(ctx, RemoteCreateRequest{TemplateID: "template-a"})
	require.NoError(t, err)
	handle, err := connectRemoteSession(ctx, wrapLangfuseRemoteClient(client), RemoteConnectRequest{
		SandboxID: created.ID(), TrafficAccessToken: "restored-token",
	})
	require.NoError(t, err)
	require.Equal(t, created.ID(), handle.ID())
	require.Equal(t, "restored-token", InboundTokenOf(handle))
	require.EqualValues(t, 1, mock.connectCount.Load())
	require.EqualValues(t, 1, mock.infoCount.Load())

	_, err = client.ConnectSession(ctx, RemoteConnectRequest{SandboxID: "gone"})
	require.True(t, CanReplaceRemoteBinding(err))
}

func TestE2BSessionConnectReusesProbeHandleAndResumes(t *testing.T) {
	mock := newE2BMockServer(t)
	client := newTestE2BRemoteClient(t, mock)
	ctx := context.Background()
	created, err := client.Create(ctx, RemoteCreateRequest{TemplateID: "template-a"})
	require.NoError(t, err)
	mock.sandboxes[created.ID()]["state"] = "paused"
	handle, err := connectRemoteSession(ctx, wrapLangfuseRemoteClient(client), RemoteConnectRequest{
		SandboxID: created.ID(), TrafficAccessToken: "restored-token",
	})
	require.NoError(t, err)
	require.Equal(t, created.ID(), handle.ID())
	require.Equal(t, "restored-token", InboundTokenOf(handle))
	require.EqualValues(t, 1, mock.connectCount.Load())
	require.EqualValues(t, 1, mock.infoCount.Load())
	require.Equal(t, "running", mock.sandboxes[created.ID()]["state"])

	_, err = client.ConnectSession(ctx, RemoteConnectRequest{SandboxID: "gone"})
	require.True(t, CanReplaceRemoteBinding(err))
}

func TestDockerSessionConnectInspectsOnlyOnce(t *testing.T) {
	engine := newFakeDockerEngine()
	inspections := 0
	engine.inspectHook = func(id string) (container.InspectResponse, error) {
		inspections++
		return container.InspectResponse{ID: id, State: &container.State{Status: "running"}}, nil
	}
	client := newTestDockerClient(t, engine)
	handle, err := connectRemoteSession(context.Background(), wrapLangfuseRemoteClient(client),
		RemoteConnectRequest{SandboxID: "container-1"})
	require.NoError(t, err)
	require.Equal(t, "container-1", handle.ID())
	require.Equal(t, 1, inspections)
}

type sessionConnectFaultClient struct {
	*fakeRemoteClient
	err   error
	calls int
}

func (c *sessionConnectFaultClient) ConnectSession(
	ctx context.Context, req RemoteConnectRequest,
) (RemoteSandboxHandle, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return c.Connect(ctx, req)
}

func TestCombinedSessionConnectOnlyReplacesDefinitiveFailures(t *testing.T) {
	for _, kind := range []RemoteErrorKind{
		RemoteErrorKindTerminal, RemoteErrorKindNotFound, RemoteErrorKindUnavailable,
		RemoteErrorKindInternal, RemoteErrorKindAuthentication,
	} {
		t.Run(string(kind), func(t *testing.T) {
			ctx := context.Background()
			store := NewMemorySessionSandboxBindingStore()
			client := &sessionConnectFaultClient{
				fakeRemoteClient: newFakeRemoteClient(SandboxTypeCube),
				err:              NewRemoteError(SandboxTypeCube, "ConnectSession", kind, "probe failed", nil),
			}
			key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
			client.addSandbox("existing", "template-a", RemoteStateTerminal, nil, time.Now())
			_, err := store.Create(ctx, key, validSessionSandboxBinding(key, "existing"))
			require.NoError(t, err)
			lifecycle := newTestRemoteSessionLifecycle(t, wrapLangfuseRemoteClient(client), store,
				&fakeSessionExistenceChecker{exists: true})
			handle, err := lifecycle.Resolve(ctx, key)
			if kind == RemoteErrorKindTerminal || kind == RemoteErrorKindNotFound {
				require.NoError(t, err)
				require.NotEqual(t, "existing", handle.ID())
				require.Equal(t, 1, client.createCount)
			} else {
				require.Error(t, err)
				require.Zero(t, client.createCount)
				binding, err := store.Get(ctx, key)
				require.NoError(t, err)
				require.Equal(t, "existing", binding.SandboxID)
			}
			require.Equal(t, 1, client.calls)
			require.Empty(t, client.getIDs)
		})
	}
}

func TestSessionSummaryValidation(t *testing.T) {
	for _, summary := range []*RemoteSandboxSummary{nil, {ID: "wrong"}} {
		err := validateSessionSummary(SandboxTypeCube, "bound", summary)
		require.Error(t, err)
		require.False(t, CanReplaceRemoteBinding(err))
	}
	err := validateSessionSummary(SandboxTypeCube, "bound",
		&RemoteSandboxSummary{ID: "bound", State: RemoteStateTerminal})
	require.True(t, CanReplaceRemoteBinding(err))
}
