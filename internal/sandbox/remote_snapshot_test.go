package sandbox

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSnapshotManagerFromDetectsCapability(t *testing.T) {
	t.Run("client implementing the interface is returned", func(t *testing.T) {
		client := newFakeRemoteClient(SandboxTypeCube)
		client.capabilities.SupportsSnapshots = true

		mgr, ok := SnapshotManagerFrom(client)

		require.True(t, ok)
		require.NotNil(t, mgr)
	})

	t.Run("client without snapshot support is rejected", func(t *testing.T) {
		client := &noSnapshotClient{}

		mgr, ok := SnapshotManagerFrom(client)

		require.False(t, ok)
		require.Nil(t, mgr)
	})
}

func TestFakeSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	client := newFakeRemoteClient(SandboxTypeCube)

	ref, err := client.CreateSnapshot(ctx, "sb-1", "weknora-sk-abc-g1")
	require.NoError(t, err)
	require.NotEmpty(t, ref.ID)

	list, err := client.ListSnapshots(ctx, "sb-1")
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, ref.ID, list[0].ID)

	// Deleting twice must be idempotent: a missing snapshot is not an error.
	require.NoError(t, client.DeleteSnapshot(ctx, ref.ID))
	require.NoError(t, client.DeleteSnapshot(ctx, ref.ID))

	list, err = client.ListSnapshots(ctx, "sb-1")
	require.NoError(t, err)
	require.Empty(t, list)
}

// noSnapshotClient implements RemoteSandboxClient without the snapshot methods,
// so SnapshotManagerFrom must reject it.
type noSnapshotClient struct{ RemoteSandboxClient }
