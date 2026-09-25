package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func agedFork(id string, age time.Duration) *types.Session {
	return &types.Session{
		ID: id, TenantID: 1,
		ForkBootstrap: &types.ForkBootstrap{
			SnapshotID: "snap-" + id,
			CreatedAt:  time.Now().UTC().Add(-age),
		},
	}
}

func TestReapDeletesSnapshotAndClearsBootstrap(t *testing.T) {
	sessions := newFakeSessionStore(nil)
	sessions.unconsumed = []*types.Session{agedFork("f1", 8*24*time.Hour)}
	snapshots := &fakeSnapshotDeleter{}
	reaper := NewForkSnapshotReaper(sessions, snapshots, 7*24*time.Hour)

	n, err := reaper.ReapOnce(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, []string{"snap-f1"}, snapshots.deleted)
	require.True(t, sessions.bootstrapCleared)
}

func TestReapReportsZeroWhenNothingIsOldEnough(t *testing.T) {
	sessions := newFakeSessionStore(nil)
	sessions.unconsumed = nil // 仓储层已按 retention 过滤
	reaper := NewForkSnapshotReaper(sessions, &fakeSnapshotDeleter{}, 7*24*time.Hour)

	n, err := reaper.ReapOnce(context.Background())

	require.NoError(t, err)
	require.Zero(t, n)
}

// 一个删不掉的快照不该挡住后面的。
func TestReapContinuesAfterOneDeleteFails(t *testing.T) {
	sessions := newFakeSessionStore(nil)
	sessions.unconsumed = []*types.Session{
		agedFork("f1", 8*24*time.Hour),
		agedFork("f2", 9*24*time.Hour),
	}
	snapshots := &fakeSnapshotDeleter{err: errors.New("provider timeout")}
	reaper := NewForkSnapshotReaper(sessions, snapshots, 7*24*time.Hour)

	_, err := reaper.ReapOnce(context.Background())

	require.NoError(t, err, "单个删除失败不该让整轮回收报错")
	require.Len(t, snapshots.deleted, 2, "两个都要尝试")
}

// 删不掉时绝不能清空 bootstrap，否则快照 ID 丢失，快照永久泄漏。
func TestReapKeepsBootstrapWhenDeleteFails(t *testing.T) {
	sessions := newFakeSessionStore(nil)
	sessions.unconsumed = []*types.Session{agedFork("f1", 8*24*time.Hour)}
	snapshots := &fakeSnapshotDeleter{err: errors.New("provider timeout")}
	reaper := NewForkSnapshotReaper(sessions, snapshots, 7*24*time.Hour)

	_, err := reaper.ReapOnce(context.Background())

	require.NoError(t, err)
	require.False(t, sessions.bootstrapCleared)
}

func TestReapDeletesStaleLeaseWithNoSessionHolder(t *testing.T) {
	sessions := newFakeSessionStore(nil)
	sessions.leases = []*types.ForkSnapshotLease{{
		SnapshotID:      "snap-orphan",
		TenantID:        9,
		SandboxConfigID: "cfg-1",
		CreatedAt:       time.Now().UTC().Add(-20 * time.Minute),
	}}
	snapshots := &fakeForkSessionSnapshotDeleter{}
	reaper := NewForkSnapshotReaper(sessions, snapshots, 7*24*time.Hour)

	n, err := reaper.ReapOnce(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, []string{"snap-orphan"}, snapshots.forkDeleted)
	require.Empty(t, sessions.leases)
}

func TestReapSkipsFreshLease(t *testing.T) {
	sessions := newFakeSessionStore(nil)
	sessions.leases = []*types.ForkSnapshotLease{{
		SnapshotID: "snap-fresh",
		CreatedAt:  time.Now().UTC(),
	}}
	snapshots := &fakeSnapshotDeleter{}
	reaper := NewForkSnapshotReaper(sessions, snapshots, 7*24*time.Hour)

	n, err := reaper.ReapOnce(context.Background())

	require.NoError(t, err)
	require.Zero(t, n)
	require.Empty(t, snapshots.deleted)
	require.Len(t, sessions.leases, 1)
}

func TestReapDropsLeaseWhenSessionHoldsSnapshot(t *testing.T) {
	holder := agedFork("f1", time.Hour)
	holder.ForkBootstrap.SnapshotID = "snap-held"
	sessions := newFakeSessionStore(holder)
	sessions.leases = []*types.ForkSnapshotLease{{
		SnapshotID: "snap-held",
		CreatedAt:  time.Now().UTC().Add(-20 * time.Minute),
	}}
	snapshots := &fakeSnapshotDeleter{}
	reaper := NewForkSnapshotReaper(sessions, snapshots, 7*24*time.Hour)

	n, err := reaper.ReapOnce(context.Background())

	require.NoError(t, err)
	require.Zero(t, n)
	require.Empty(t, snapshots.deleted, "session still needs this snapshot")
	require.Empty(t, sessions.leases, "redundant lease is just bookkeeping")
}

type fakeForkSessionSnapshotDeleter struct {
	fakeSnapshotDeleter
	forkDeleted []string
	forkErr     error
	lastTenant  uint64
	lastConfig  string
}

func (f *fakeForkSessionSnapshotDeleter) DeleteForkSnapshot(
	_ context.Context, tenantID uint64, sandboxConfigID, snapshotID string,
) error {
	f.lastTenant = tenantID
	f.lastConfig = sandboxConfigID
	f.forkDeleted = append(f.forkDeleted, snapshotID)
	return f.forkErr
}

func TestReapPrefersTenantScopedForkDelete(t *testing.T) {
	sessions := newFakeSessionStore(nil)
	sess := agedFork("f1", 8*24*time.Hour)
	sess.SandboxConfigID = "cfg-1"
	sessions.unconsumed = []*types.Session{sess}
	snapshots := &fakeForkSessionSnapshotDeleter{}
	reaper := NewForkSnapshotReaper(sessions, snapshots, 7*24*time.Hour)

	n, err := reaper.ReapOnce(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, []string{"snap-f1"}, snapshots.forkDeleted)
	require.Equal(t, uint64(1), snapshots.lastTenant)
	require.Equal(t, "cfg-1", snapshots.lastConfig)
	require.Empty(t, snapshots.deleted, "ID-only DeleteSnapshot must not run when tenant-scoped delete exists")
	require.True(t, sessions.bootstrapCleared)
}

func TestReapSkipsSnapshotStillNeededByUnopenedSibling(t *testing.T) {
	sessions := newFakeSessionStore(&types.Session{
		ID: "unopened", TenantID: 1,
		ForkBootstrap: &types.ForkBootstrap{
			SnapshotID: "snap-shared",
			CreatedAt:  time.Now().UTC(),
		},
	})
	consumedAt := time.Now().UTC()
	sessions.unconsumed = []*types.Session{{
		ID: "opened", TenantID: 1,
		ForkBootstrap: &types.ForkBootstrap{
			SnapshotID: "snap-shared",
			CreatedAt:  consumedAt.Add(-time.Minute),
			ConsumedAt: &consumedAt,
		},
	}}
	snapshots := &fakeSnapshotDeleter{}
	reaper := NewForkSnapshotReaper(sessions, snapshots, 7*24*time.Hour)

	n, err := reaper.ReapOnce(context.Background())

	require.NoError(t, err)
	require.Zero(t, n)
	require.Empty(t, snapshots.deleted)
	require.False(t, sessions.bootstrapCleared)
}

func TestReapDeletesSharedSnapshotOnceBothHoldersAreStale(t *testing.T) {
	sessions := newFakeSessionStore(nil)
	sessions.unconsumed = []*types.Session{
		agedFork("f1", 8*24*time.Hour),
		{
			ID: "f2", TenantID: 1,
			ForkBootstrap: &types.ForkBootstrap{
				SnapshotID: "snap-f1",
				CreatedAt:  time.Now().UTC().Add(-9 * 24 * time.Hour),
			},
		},
	}
	snapshots := &fakeSnapshotDeleter{}
	reaper := NewForkSnapshotReaper(sessions, snapshots, 7*24*time.Hour)

	n, err := reaper.ReapOnce(context.Background())

	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.Equal(t, []string{"snap-f1"}, snapshots.deleted)
}

func TestReapRetriesConsumedLeftoverSnapshot(t *testing.T) {
	sessions := newFakeSessionStore(nil)
	consumedAt := time.Now().UTC()
	sessions.unconsumed = []*types.Session{{
		ID: "fork-1", TenantID: 1,
		ForkBootstrap: &types.ForkBootstrap{
			SnapshotID: "snap-fork-1",
			CreatedAt:  consumedAt.Add(-time.Minute),
			ConsumedAt: &consumedAt,
		},
	}}
	snapshots := &fakeSnapshotDeleter{}
	reaper := NewForkSnapshotReaper(sessions, snapshots, 7*24*time.Hour)

	n, err := reaper.ReapOnce(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, []string{"snap-fork-1"}, snapshots.deleted)
	require.True(t, sessions.bootstrapCleared)
}

func TestReapKeepsBootstrapWhenForkDeleteFails(t *testing.T) {
	sessions := newFakeSessionStore(nil)
	sessions.unconsumed = []*types.Session{agedFork("f1", 8*24*time.Hour)}
	snapshots := &fakeForkSessionSnapshotDeleter{forkErr: errors.New("cube timeout")}
	reaper := NewForkSnapshotReaper(sessions, snapshots, 7*24*time.Hour)

	_, err := reaper.ReapOnce(context.Background())

	require.NoError(t, err)
	require.False(t, sessions.bootstrapCleared)
}

type stubSnapshotDeletingManager struct {
	stubPinnedManager
	deleted []string
	err     error
}

func (m *stubSnapshotDeletingManager) DeleteSnapshot(_ context.Context, snapshotID string) error {
	m.deleted = append(m.deleted, snapshotID)
	return m.err
}

func TestResolverForkSnapshotDeleterDeletesViaResolvedManager(t *testing.T) {
	mgr := &stubSnapshotDeletingManager{}
	resolver := &stubTenantSandboxResolver{mgr: mgr}
	deleter := NewResolverForkSnapshotDeleter(resolver, nil)
	scoped, ok := deleter.(forkSessionSnapshotDeleter)
	require.True(t, ok)

	err := scoped.DeleteForkSnapshot(context.Background(), 9, "cfg-1", "snap-1")

	require.NoError(t, err)
	require.Equal(t, []string{"snap-1"}, mgr.deleted)
	require.Equal(t, 1, resolver.calls)
	require.Equal(t, "cfg-1", resolver.lastCfg)
}

func TestResolverForkSnapshotDeleterKeepsIDOnlyDeleteUnsupported(t *testing.T) {
	err := NewResolverForkSnapshotDeleter(nil, nil).DeleteSnapshot(context.Background(), "snap-1")
	require.Error(t, err)
}

func TestReleaseForkSnapshotOnDeleteRemovesUnusedSnapshot(t *testing.T) {
	session := &types.Session{
		ID: "fork-1", TenantID: 9, UserID: "u1", SandboxConfigID: "cfg-1",
		ForkBootstrap: &types.ForkBootstrap{SnapshotID: "snap-1", CreatedAt: time.Now().UTC()},
	}
	sessions := newFakeSessionStore(session)
	snapshots := &fakeForkSessionSnapshotDeleter{}

	releaseForkSnapshotOnDelete(context.Background(), sessions, snapshots, session)

	require.Equal(t, []string{"snap-1"}, snapshots.forkDeleted)
	require.Equal(t, uint64(9), snapshots.lastTenant)
	require.Equal(t, "cfg-1", snapshots.lastConfig)
	require.Empty(t, sessions.leases)
}

func TestReleaseForkSnapshotOnDeleteKeepsSharedSnapshot(t *testing.T) {
	session := &types.Session{
		ID: "fork-1", TenantID: 1, UserID: "u1",
		ForkBootstrap: &types.ForkBootstrap{SnapshotID: "snap-shared", CreatedAt: time.Now().UTC()},
	}
	sessions := newFakeSessionStore(session)
	sessions.unconsumed = []*types.Session{{
		ID: "sibling", TenantID: 1,
		ForkBootstrap: &types.ForkBootstrap{SnapshotID: "snap-shared", CreatedAt: time.Now().UTC()},
	}}
	snapshots := &fakeForkSessionSnapshotDeleter{}

	releaseForkSnapshotOnDelete(context.Background(), sessions, snapshots, session)

	require.Empty(t, snapshots.forkDeleted)
	require.Empty(t, sessions.leases)
}

func TestReleaseForkSnapshotOnDeleteLeasesWhenDeleteFails(t *testing.T) {
	session := &types.Session{
		ID: "fork-1", TenantID: 9, UserID: "u1", SandboxConfigID: "cfg-1",
		ForkBootstrap: &types.ForkBootstrap{SnapshotID: "snap-1", CreatedAt: time.Now().UTC()},
	}
	sessions := newFakeSessionStore(session)
	snapshots := &fakeForkSessionSnapshotDeleter{forkErr: errors.New("cube timeout")}

	releaseForkSnapshotOnDelete(context.Background(), sessions, snapshots, session)

	require.Equal(t, []string{"snap-1"}, snapshots.forkDeleted)
	require.Len(t, sessions.leases, 1)
	require.Equal(t, "snap-1", sessions.leases[0].SnapshotID)
	require.Equal(t, uint64(9), sessions.leases[0].TenantID)
	require.Equal(t, "cfg-1", sessions.leases[0].SandboxConfigID)
}

func TestReleaseForkSnapshotOnDeleteSkipsSessionsWithoutBootstrap(t *testing.T) {
	session := &types.Session{ID: "plain", TenantID: 1, UserID: "u1"}
	sessions := newFakeSessionStore(session)
	snapshots := &fakeForkSessionSnapshotDeleter{}

	releaseForkSnapshotOnDelete(context.Background(), sessions, snapshots, session)

	require.Empty(t, snapshots.forkDeleted)
	require.Empty(t, sessions.leases)
}
