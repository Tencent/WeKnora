package sandboxalloc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/sandbox/allocation"
)

type fakeMachineOps struct {
	sandbox.Manager
	deleted  [][]string
	failNext error
	busy     map[string]bool
}

func (f *fakeMachineOps) DeleteSessionWorkspaces(
	_ context.Context, _ allocation.UserMachine, ids []string,
) ([]string, error) {
	if f.failNext != nil {
		err := f.failNext
		f.failNext = nil
		return nil, err
	}
	var done []string
	for _, id := range ids {
		if !f.busy[id] {
			done = append(done, id)
		}
	}
	f.deleted = append(f.deleted, done)
	return done, nil
}

func (f *fakeMachineOps) ReclaimUserMachine(
	context.Context, allocation.UserMachine, func(context.Context) (bool, error),
) (bool, error) {
	return true, nil
}

type fakeResolver struct{ mgr sandbox.Manager }

func (r fakeResolver) Resolve(context.Context, uint64, string) (sandbox.Manager, error) {
	return r.mgr, nil
}

// pendingCleanups names the sessions in ids whose directory cleanup is still due.
func pendingCleanups(t *testing.T, e *testEnv, ids ...string) []string {
	t.Helper()
	var out []string
	for _, id := range ids {
		row, err := e.svc.repo.GetAssignment(context.Background(), id)
		require.NoError(t, err)
		if row != nil && row.CleanupDueAt != nil {
			out = append(out, id)
		}
	}
	return out
}

func TestReleaseSessionsPersistsIntentAndSkipsSessionMode(t *testing.T) {
	enableUserMode(t)
	a, b, legacy := uuid.NewString(), uuid.NewString(), uuid.NewString()
	legacyRow := webSession(legacy)
	legacyRow.SandboxConfigID, legacyRow.SandboxConfigTenantID = "cfg-a", 7
	e := newEnv(t, fakeConfigs{"cfg-a": "user"}, webSession(a), webSession(b), legacyRow)
	for _, id := range []string{a, b, legacy} {
		e.place(t, id, "cfg-a")
	}

	ops := &fakeMachineOps{}
	c := NewWorkspaceCleaner(e.svc, fakeResolver{mgr: ops})
	handled, err := c.ReleaseSessions(context.Background(), []string{a, b, legacy})
	require.NoError(t, err)
	require.True(t, handled[a])
	require.True(t, handled[b])
	require.False(t, handled[legacy], "a per-session sandbox is torn down by the caller as before")
	require.Empty(t, ops.deleted, "release only records intent; nothing runs on the machine yet")

	require.ElementsMatch(t, []string{a, b}, pendingCleanups(t, e, a, b, legacy))

	c.RunCleanups(context.Background(), []string{a, b})
	require.Len(t, ops.deleted, 1, "one machine, one command")
	require.ElementsMatch(t, []string{a, b}, ops.deleted[0])
	require.Empty(t, pendingCleanups(t, e, a, b))
}

// deleteOnlyMachine is what *sandbox.SessionBoundManager can do today:
// DeleteSessionWorkspaces exists, ReclaimUserMachine does not until the
// empty-machine GC. Cleanup must not require the later method.
type deleteOnlyMachine struct {
	sandbox.Manager
	deleted [][]string
}

func (d *deleteOnlyMachine) DeleteSessionWorkspaces(
	_ context.Context, _ allocation.UserMachine, ids []string,
) ([]string, error) {
	copied := append([]string(nil), ids...)
	d.deleted = append(d.deleted, copied)
	return copied, nil
}

func TestRunCleanupsCompletesWhenTheManagerOnlyDeletesDirectories(t *testing.T) {
	enableUserMode(t)
	id := uuid.NewString()
	e := newEnv(t, fakeConfigs{"cfg-a": "user"}, webSession(id))
	e.place(t, id, "cfg-a")
	ops := &deleteOnlyMachine{}
	c := NewWorkspaceCleaner(e.svc, fakeResolver{mgr: ops})
	_, err := c.ReleaseSessions(context.Background(), []string{id})
	require.NoError(t, err)

	c.RunCleanups(context.Background(), []string{id})

	require.Empty(t, pendingCleanups(t, e, id), "the directory task is done, not retried")
	require.Equal(t, [][]string{{id}}, ops.deleted)
}

func TestRunCleanupsKeepsBusyAndFailedTasksForRetry(t *testing.T) {
	enableUserMode(t)
	a, b := uuid.NewString(), uuid.NewString()
	e := newEnv(t, fakeConfigs{"cfg-a": "user"}, webSession(a), webSession(b))
	e.place(t, a, "cfg-a")
	e.place(t, b, "cfg-a")
	ops := &fakeMachineOps{busy: map[string]bool{b: true}}
	c := NewWorkspaceCleaner(e.svc, fakeResolver{mgr: ops})
	_, err := c.ReleaseSessions(context.Background(), []string{a, b})
	require.NoError(t, err)

	c.RunCleanups(context.Background(), []string{a, b})
	require.Equal(t, []string{b}, pendingCleanups(t, e, a, b), "B still had work registered")

	ops.busy = nil
	ops.failNext = errors.New("connect: timeout")
	c.now = func() time.Time { return time.Now().UTC().Add(time.Hour) }
	done, failed, err := c.RetryDue(context.Background(), 10)
	require.NoError(t, err)
	require.Zero(t, done)
	require.Equal(t, 1, failed)

	c.now = func() time.Time { return time.Now().UTC().Add(24 * time.Hour) }
	done, _, err = c.RetryDue(context.Background(), 10)
	require.NoError(t, err)
	require.Equal(t, 1, done)
}

func TestReleaseOwnerThenReclaim(t *testing.T) {
	enableUserMode(t)
	a := uuid.NewString()
	e := newEnv(t, fakeConfigs{"cfg-a": "user"}, webSession(a))
	e.place(t, a, "cfg-a")
	ops := &reclaimRecorder{}
	c := NewWorkspaceCleaner(e.svc, fakeResolver{mgr: ops})

	require.NoError(t, c.ReleaseOwnerAssignments(context.Background(), "u-1"))
	got, ok, err := e.svc.Lookup(context.Background(), a)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, got.ReleasedAt)

	c.ReclaimOwner(context.Background(), "u-1")
	require.Equal(t, 1, ops.reclaims)
	m, _ := e.svc.repo.GetUserMachine(context.Background(), e.digest(t, a))
	require.Nil(t, m, "the registry row goes with the machine")
}

func TestReleaseMachineRefusesLiveSessions(t *testing.T) {
	enableUserMode(t)
	a := uuid.NewString()
	e := newEnv(t, fakeConfigs{"cfg-a": "user"}, webSession(a))
	e.place(t, a, "cfg-a")
	ops := &reclaimRecorder{}
	c := NewWorkspaceCleaner(e.svc, fakeResolver{mgr: ops})
	machines, err := c.OwnerMachines(context.Background(), 7, "u-1")
	require.NoError(t, err)
	require.Len(t, machines, 1)

	ok, err := c.ReleaseMachine(context.Background(), machines[0])
	require.NoError(t, err)
	require.False(t, ok, "a live session keeps the machine")
	require.Zero(t, ops.reclaims, "the in-lock check must refuse before destroy")
	m, err := e.svc.repo.GetUserMachine(context.Background(), e.digest(t, a))
	require.NoError(t, err)
	require.NotNil(t, m)
}

func TestReleaseMachineDoesNotTreatAClearedClockAsEmpty(t *testing.T) {
	enableUserMode(t)
	a := uuid.NewString()
	e := newEnv(t, fakeConfigs{"cfg-a": "user"}, webSession(a))
	e.place(t, a, "cfg-a")
	ops := &reclaimRecorder{}
	c := NewWorkspaceCleaner(e.svc, fakeResolver{mgr: ops})
	require.NoError(t, c.ReleaseOwnerAssignments(context.Background(), "u-1"))
	require.NoError(t, e.db.Exec(
		"UPDATE sandbox_user_machines SET empty_since = NULL WHERE machine_digest = ?", e.digest(t, a),
	).Error)
	machines, err := c.OwnerMachines(context.Background(), 0, "u-1")
	require.NoError(t, err)

	ok, err := c.ReleaseMachine(context.Background(), machines[0])
	require.NoError(t, err)
	require.False(t, ok, "a cleared empty_since means a session joined; do not destroy")
	require.Zero(t, ops.reclaims)
	m, err := e.svc.repo.GetUserMachine(context.Background(), e.digest(t, a))
	require.NoError(t, err)
	require.NotNil(t, m)
}

func TestReleaseMachineReclaimsAnEmptyMachine(t *testing.T) {
	enableUserMode(t)
	a := uuid.NewString()
	e := newEnv(t, fakeConfigs{"cfg-a": "user"}, webSession(a))
	e.place(t, a, "cfg-a")
	ops := &reclaimRecorder{}
	c := NewWorkspaceCleaner(e.svc, fakeResolver{mgr: ops})
	require.NoError(t, c.ReleaseOwnerAssignments(context.Background(), "u-1"))
	machines, err := c.OwnerMachines(context.Background(), 0, "u-1")
	require.NoError(t, err)
	require.Len(t, machines, 1)

	ok, err := c.ReleaseMachine(context.Background(), machines[0])
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 1, ops.reclaims)
	m, err := e.svc.repo.GetUserMachine(context.Background(), e.digest(t, a))
	require.NoError(t, err)
	require.Nil(t, m, "an empty machine's registry row goes with it")
}

// busyReclaimer is what ReclaimUserMachine returns when work is still
// registered: false, and it never destroys. ReleaseMachine must not add a
// second path that deletes the machine anyway.
type busyReclaimer struct{ fakeMachineOps }

func (busyReclaimer) ReclaimUserMachine(
	context.Context, allocation.UserMachine, func(context.Context) (bool, error),
) (bool, error) {
	return false, nil
}

func TestReleaseMachineLeavesTheRowWhenTheManagerRefuses(t *testing.T) {
	enableUserMode(t)
	a := uuid.NewString()
	e := newEnv(t, fakeConfigs{"cfg-a": "user"}, webSession(a))
	e.place(t, a, "cfg-a")
	c := NewWorkspaceCleaner(e.svc, fakeResolver{mgr: &busyReclaimer{}})
	require.NoError(t, c.ReleaseOwnerAssignments(context.Background(), "u-1"))
	machines, err := c.OwnerMachines(context.Background(), 0, "u-1")
	require.NoError(t, err)

	ok, err := c.ReleaseMachine(context.Background(), machines[0])
	require.NoError(t, err)
	require.False(t, ok)
	m, err := e.svc.repo.GetUserMachine(context.Background(), e.digest(t, a))
	require.NoError(t, err)
	require.NotNil(t, m)
}
