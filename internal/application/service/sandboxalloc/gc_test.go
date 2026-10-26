package sandboxalloc

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/sandbox/allocation"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// perConfigRetention answers each config's retention from a map.
type perConfigRetention map[string]int

func (p perConfigRetention) GetByID(
	_ context.Context, tenantID uint64, id string,
) (*types.TenantSandboxConfigEntity, error) {
	return &types.TenantSandboxConfigEntity{ID: id, TenantID: tenantID, Config: &types.TenantSandboxConfig{
		AllocationMode: "user", UserSandboxEmptyRetentionDays: p[id],
	}}, nil
}

type retentionConfigs struct{ days *int }

func (r retentionConfigs) GetByID(
	_ context.Context, tenantID uint64, id string,
) (*types.TenantSandboxConfigEntity, error) {
	days := 0
	if r.days != nil {
		days = *r.days
	}
	return &types.TenantSandboxConfigEntity{ID: id, TenantID: tenantID, Config: &types.TenantSandboxConfig{
		AllocationMode: "user", UserSandboxEmptyRetentionDays: days,
	}}, nil
}

// reclaimRecorder runs beforeCheck/afterCheck around the check the GC hands
// to the manager, standing in for things that happen while the lock is held.
type reclaimRecorder struct {
	fakeMachineOps
	beforeCheck, afterCheck func()
	reclaims                int
}

func (r *reclaimRecorder) ReclaimUserMachine(
	ctx context.Context, _ allocation.UserMachine, check func(context.Context) (bool, error),
) (bool, error) {
	if r.beforeCheck != nil {
		r.beforeCheck()
	}
	ok, err := check(ctx)
	if err != nil || !ok {
		return false, err
	}
	if r.afterCheck != nil {
		r.afterCheck()
	}
	r.reclaims++
	return true, nil
}

func releasedMachine(t *testing.T) (*testEnv, string) {
	t.Helper()
	enableUserMode(t)
	a := uuid.NewString()
	e := newEnv(t, fakeConfigs{"cfg-a": "user"}, webSession(a))
	e.place(t, a, "cfg-a")
	_, err := e.svc.Release(context.Background(), a)
	require.NoError(t, err)
	return e, a
}

func TestGCReclaimsOnlyAfterRetention(t *testing.T) {
	seven := 7
	e, _ := releasedMachine(t)
	ops := &reclaimRecorder{}
	gc := NewUserMachineGC(e.svc, NewWorkspaceCleaner(e.svc, fakeResolver{mgr: ops}),
		retentionConfigs{days: &seven}, nil)

	gc.now = func() time.Time { return time.Now().UTC().Add(6 * 24 * time.Hour) }
	report, err := gc.RunOnce(context.Background())
	require.NoError(t, err)
	require.Zero(t, report.Reclaimed)

	gc.now = func() time.Time { return time.Now().UTC().Add(8 * 24 * time.Hour) }
	report, err = gc.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, report.Reclaimed)
	rows, _ := e.svc.repo.ListEmptyUserMachines(context.Background(),
		time.Now().UTC().Add(30*24*time.Hour), interfaces.EmptyMachineCursor{}, 10)
	require.Empty(t, rows, "the registry row goes with the machine")
}

func TestGCRecomputesInsideTheLock(t *testing.T) {
	one := 1
	e, _ := releasedMachine(t)
	b := uuid.NewString()
	e.addSession(t, webSession(b))
	ops := &reclaimRecorder{beforeCheck: func() {
		// Joined and left between the scan and the lock: empty_since moved.
		e.svc.now = func() time.Time { return time.Now().UTC().Add(48 * time.Hour) }
		e.place(t, b, "cfg-a")
		_, err := e.svc.Release(context.Background(), b)
		require.NoError(t, err)
	}}
	gc := NewUserMachineGC(e.svc, NewWorkspaceCleaner(e.svc, fakeResolver{mgr: ops}), retentionConfigs{days: &one}, nil)
	gc.now = func() time.Time { return time.Now().UTC().Add(48 * time.Hour) }

	report, err := gc.RunOnce(context.Background())
	require.NoError(t, err)
	require.Zero(t, ops.reclaims)
	require.Equal(t, 1, report.Busy)
}

func TestGCKeepsTheRowWhenASessionJoinsBeforeTheLockedCheck(t *testing.T) {
	one := 1
	e, _ := releasedMachine(t)
	b := uuid.NewString()
	e.addSession(t, webSession(b))
	ops := &reclaimRecorder{beforeCheck: func() {
		// Joining clears empty_since. The locked check must see that and
		// not destroy the machine this session now owns.
		e.place(t, b, "cfg-a")
	}}
	gc := NewUserMachineGC(e.svc, NewWorkspaceCleaner(e.svc, fakeResolver{mgr: ops}), retentionConfigs{days: &one}, nil)
	gc.now = func() time.Time { return time.Now().UTC().Add(48 * time.Hour) }

	_, err := gc.RunOnce(context.Background())
	require.NoError(t, err)
	require.Zero(t, ops.reclaims, "a joined machine is not destroyed")
	m, err := e.svc.repo.GetUserMachine(context.Background(), e.digest(t, b))
	require.NoError(t, err)
	require.NotNil(t, m, "B lives on this owner+config; its registry row must survive")
	require.Nil(t, m.EmptySince)
}

func TestGCReadsRetentionFreshEachRun(t *testing.T) {
	days := 30
	e, _ := releasedMachine(t)
	ops := &reclaimRecorder{}
	gc := NewUserMachineGC(e.svc, NewWorkspaceCleaner(e.svc, fakeResolver{mgr: ops}),
		retentionConfigs{days: &days}, nil)
	gc.now = func() time.Time { return time.Now().UTC().Add(48 * time.Hour) }
	_, _ = gc.RunOnce(context.Background())
	require.Zero(t, ops.reclaims)

	days = 1 // admin shortened the window
	_, _ = gc.RunOnce(context.Background())
	require.Equal(t, 1, ops.reclaims)
}

func TestGCIsNotStarvedByOldCandidatesThatAreNotDue(t *testing.T) {
	enableUserMode(t)
	// Two old machines on a 30-day config, one newer machine on a 1-day config.
	thirty, one := uuid.NewString(), uuid.NewString()
	old1, old2, due := uuid.NewString(), uuid.NewString(), uuid.NewString()
	sess := func(id, owner string) *types.Session {
		s := webSession(id)
		s.UserID = owner
		return s
	}
	e := newEnv(t, fakeConfigs{thirty: "user", one: "user"}, sess(old1, "u-1"), sess(old2, "u-2"), sess(due, "u-3"))
	for _, id := range []string{old1, old2} {
		e.place(t, id, thirty)
	}
	e.place(t, due, one)
	for _, id := range []string{old1, old2, due} {
		_, err := e.svc.Release(context.Background(), id)
		require.NoError(t, err)
	}
	ops := &reclaimRecorder{}
	days := map[string]int{thirty: 30, one: 1}
	gc := NewUserMachineGC(e.svc, NewWorkspaceCleaner(e.svc, fakeResolver{mgr: ops}), perConfigRetention(days), nil)
	gc.batch = 1 // one candidate per page: the two not-due ones come first
	gc.now = func() time.Time { return time.Now().UTC().Add(48 * time.Hour) }

	report, err := gc.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3, report.Scanned)
	require.Equal(t, 1, report.Reclaimed, "the due machine behind them is still reached")
}

func TestGCResumesFromItsCursorWhenARunIsCapped(t *testing.T) {
	enableUserMode(t)
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	var rows []*types.Session
	for i, id := range ids {
		s := webSession(id)
		s.UserID = fmt.Sprintf("u-%d", i)
		rows = append(rows, s)
	}
	e := newEnv(t, fakeConfigs{"cfg-a": "user"}, rows...)
	for _, id := range ids {
		e.place(t, id, "cfg-a")
		_, err := e.svc.Release(context.Background(), id)
		require.NoError(t, err)
	}
	ops := &reclaimRecorder{}
	thirty := 30
	gc := NewUserMachineGC(e.svc, NewWorkspaceCleaner(e.svc, fakeResolver{mgr: ops}),
		retentionConfigs{days: &thirty}, nil)
	gc.batch, gc.maxRun = 1, 2
	gc.now = func() time.Time { return time.Now().UTC().Add(48 * time.Hour) }

	first, err := gc.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, first.Scanned)
	second, err := gc.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, second.Scanned, "the second run continues where the first stopped")
	third, err := gc.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, third.Scanned, "after the end the scan starts over")
}

func TestGCRunsOnOneReplicaAtATime(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	e := newEnv(t, fakeConfigs{})
	gc := NewUserMachineGC(e.svc, NewWorkspaceCleaner(e.svc, fakeResolver{}), retentionConfigs{}, client)
	require.NoError(t, mini.Set(UserMachineGCLockKey, "someone-else"))
	report, err := gc.RunOnce(context.Background())
	require.NoError(t, err)
	require.True(t, report.Skipped)
}
