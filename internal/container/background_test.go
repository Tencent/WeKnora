package container

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/application/service/sandboxalloc"
	"github.com/Tencent/WeKnora/internal/background"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/sandbox/allocation"
	"github.com/Tencent/WeKnora/internal/stream"
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

func TestBackgroundActivityRegistryUsesRedisBindingStore(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	store := newSessionBindingStore(client)
	require.IsType(t, &sandbox.RedisSessionSandboxBindingStore{}, store)
	require.IsType(t, &sandbox.RedisActivityRegistry{}, newBackgroundActivityRegistry(store, nil))
}

func TestBackgroundActivityRegistryIsProcessLocalWithoutRedis(t *testing.T) {
	store := newSessionBindingStore(nil)
	require.IsType(t, &sandbox.MemorySessionSandboxBindingStore{}, store)
	require.IsType(t, &sandbox.MemoryActivityRegistry{}, newBackgroundActivityRegistry(store, nil))
}

func TestBackgroundActivityRegistryWithoutBindingStoreIsProcessLocal(t *testing.T) {
	require.IsType(t, &background.MemoryActivityRegistry{}, newBackgroundActivityRegistry(nil, nil))
}

func TestBackgroundNoticeInboxFollowsTheStreamManager(t *testing.T) {
	mini := miniredis.RunT(t)
	redisStreams, err := stream.NewRedisStreamManager(mini.Addr(), "", "", 0, "test", time.Hour)
	require.NoError(t, err)
	t.Cleanup(func() { _ = redisStreams.Close() })
	require.IsType(t, &stream.RedisNoticeInbox{}, newBackgroundNoticeInbox(redisStreams))

	require.IsType(t, &background.MemoryNoticeInbox{}, newBackgroundNoticeInbox(stream.NewMemoryStreamManager()))
	require.IsType(t, &background.MemoryNoticeInbox{}, newBackgroundNoticeInbox(nil))
}

type nameCleaner struct {
	names []string
	stops []types.CleanupFunc
}

func (c *nameCleaner) Register(fn types.CleanupFunc) { c.RegisterWithName("", fn) }

func (c *nameCleaner) RegisterWithName(name string, fn types.CleanupFunc) {
	c.names = append(c.names, name)
	c.stops = append(c.stops, fn)
}

func (c *nameCleaner) Promote(string) {}

func (c *nameCleaner) Cleanup(context.Context) []error {
	var errs []error
	for _, fn := range c.stops {
		if err := fn(); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func TestNewShellJobServiceSetsHandler(t *testing.T) {
	bg := service.NewBackgroundWork(background.NewMemoryActivityRegistry(nil), background.NewMemoryNoticeInbox(nil))
	svc := newRemoteShellJobService(nil, nil, bg, nil)
	require.Equal(t, svc, bg.Handler)
	require.False(t, svc.RemoteAvailable())
	_, err := svc.SubmitShellJob(context.Background(), interfaces.SessionRef{TenantID: 1, SessionID: "s"},
		tools.ShellJobSubmitRequest{Command: "true"})
	require.Error(t, err)
}

func TestShellJobSweeperStartsOnlyWhenRemoteAvailable(t *testing.T) {
	bg := service.NewBackgroundWork(background.NewMemoryActivityRegistry(nil), background.NewMemoryNoticeInbox(nil))
	local := newRemoteShellJobService(nil, nil, bg, stream.NewMemoryStreamManager())
	cleaner := &nameCleaner{}
	startShellJobSweeper(local, cleaner)
	require.Empty(t, cleaner.names)

	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	remoteBG := service.NewBackgroundWork(
		background.NewMemoryActivityRegistry(nil),
		background.NewMemoryNoticeInbox(nil),
	)
	remote := newRemoteShellJobService(client, &service.PinnedSessionSandbox{}, remoteBG, nil)
	require.True(t, remote.RemoteAvailable())
	startShellJobSweeper(remote, cleaner)
	require.Equal(t, []string{"ShellJobSweeper"}, cleaner.names)
	for _, err := range cleaner.Cleanup(context.Background()) {
		require.NoError(t, err)
	}
}
