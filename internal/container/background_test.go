package container

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/sandboxalloc"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/sandbox/allocation"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestBackgroundActivityRegistrySharesMemoryBindingState(t *testing.T) {
	ctx := t.Context()
	store := sandbox.NewMemorySessionSandboxBindingStore()
	registry := newBackgroundActivityRegistry(store, nil)
	ref := interfaces.SessionRef{TenantID: 42, SessionID: "s1"}
	key := sandbox.SessionSandboxKey{TenantID: ref.TenantID, SessionID: ref.SessionID}
	require.NoError(t, registry.Add(ctx, ref, interfaces.BackgroundActivity{ID: "sj_1"}, time.Minute, 0))
	require.NoError(t, store.BeginTurn(ctx, key))
	active, rebuild, err := store.TurnState(ctx, key)
	require.NoError(t, err)
	require.True(t, active)
	require.False(t, rebuild)
	require.NoError(t, store.EndTurn(ctx, key))
	_, err = store.TryLockRewind(ctx, key)
	require.ErrorIs(t, err, sandbox.ErrSessionWorkActive)
}

type backgroundAllocationRepo struct {
	interfaces.SandboxAllocationRepository
	assignment *types.SessionSandboxAssignment
	machine    *types.SandboxUserMachine
}

func (r *backgroundAllocationRepo) GetAssignment(context.Context, string) (*types.SessionSandboxAssignment, error) {
	return r.assignment, nil
}

func (r *backgroundAllocationRepo) GetUserMachine(context.Context, string) (*types.SandboxUserMachine, error) {
	return r.machine, nil
}

func TestBackgroundActivityRegistryUsesRecordedUserAllocation(t *testing.T) {
	ctx := t.Context()
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	store, err := sandbox.NewRedisSessionSandboxBindingStore(client, "test")
	require.NoError(t, err)
	a := allocation.Assignment{
		SessionID: "0f8fad5b-d9cb-469f-a165-70867728950e", ConfigTenantID: 42, ConfigID: "cfg-1",
		Machine: allocation.UserMachine{OwnerTenantID: 42, OwnerID: "u1", ConfigTenantID: 42, ConfigID: "cfg-1"},
	}
	row, machine := sandboxalloc.ToEntity(a, a.Machine.Digest("test"))
	allocations := sandboxalloc.New(&backgroundAllocationRepo{assignment: row, machine: machine}, nil, nil, "test")
	registry := newBackgroundActivityRegistry(store, allocations)
	ref := interfaces.SessionRef{TenantID: 42, SessionID: a.SessionID}
	require.NoError(t, registry.Add(ctx, ref, interfaces.BackgroundActivity{ID: "sj_1"}, time.Minute, 0))
	key := sandbox.SessionSandboxKey{TenantID: ref.TenantID, SessionID: ref.SessionID, User: a.Machine}
	active, rebuild, err := store.TurnState(ctx, key)
	require.NoError(t, err)
	require.True(t, active, "the production factory must address the same leases as the shared binding store")
	require.False(t, rebuild)
	_, err = store.TryLockRewind(ctx, key)
	require.ErrorIs(t, err, sandbox.ErrSessionWorkActive)
}
