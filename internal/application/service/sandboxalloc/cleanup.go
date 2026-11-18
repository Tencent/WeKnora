package sandboxalloc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/sandbox/allocation"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// UserMachineOps is the slice of *sandbox.SessionBoundManager the cleaner and
// the GC drive.
type UserMachineOps interface {
	DeleteSessionWorkspaces(ctx context.Context, machine allocation.UserMachine, sessionIDs []string) ([]string, error)
	ReclaimUserMachine(ctx context.Context, machine allocation.UserMachine,
		check func(context.Context) (bool, error)) (bool, error)
}

// cleanupBackoff is the wait before the next attempt. There is no attempt
// counter (C17): the delay grows with the time since the release, which
// roughly doubles it per attempt, from 1m up to 4h.
func cleanupBackoff(releasedAt *time.Time, now time.Time) time.Duration {
	d := time.Minute
	if releasedAt != nil {
		d = now.Sub(*releasedAt)
	}
	return min(max(d, time.Minute), 4*time.Hour)
}

// WorkspaceCleaner gives a deleted user-mode session's directory back.
type WorkspaceCleaner struct {
	svc      *Service
	resolver sandbox.TenantSandboxResolver
	now      func() time.Time
}

// NewWorkspaceCleaner wires the cleaner. resolver resolves managers without
// the workspace kill switch: teardown must work while scripts are disabled.
func NewWorkspaceCleaner(svc *Service, resolver sandbox.TenantSandboxResolver) *WorkspaceCleaner {
	return &WorkspaceCleaner{svc: svc, resolver: resolver, now: svc.Now}
}

// sessionDirectoryDeleter is the only method directory cleanup needs.
// UserMachineOps also requires ReclaimUserMachine. Asserting that here would
// fail directory cleanup for a manager that can delete directories but not
// reclaim. The GC and delete-all assert ReclaimUserMachine on their own.
type sessionDirectoryDeleter interface {
	DeleteSessionWorkspaces(ctx context.Context, machine allocation.UserMachine, sessionIDs []string) ([]string, error)
}

func (c *WorkspaceCleaner) machineOps(ctx context.Context, m allocation.UserMachine) (sessionDirectoryDeleter, error) {
	if c.resolver == nil {
		return nil, errors.New("sandboxalloc: no sandbox resolver")
	}
	mgr, err := c.resolver.Resolve(types.WithSandboxTenantID(ctx, m.OwnerTenantID), m.ConfigTenantID, m.ConfigID)
	if err != nil {
		return nil, err
	}
	ops, ok := mgr.(sessionDirectoryDeleter)
	if !ok {
		return nil, fmt.Errorf("sandboxalloc: config %d/%s does not serve user machines", m.ConfigTenantID, m.ConfigID)
	}
	return ops, nil
}

// userMachineReclaimer is asserted by the GC and by delete-all, separately
// from directory cleanup. machineOps stays the narrower assert so a manager
// that can delete directories still can, before it learns how to reclaim.
type userMachineReclaimer interface {
	ReclaimUserMachine(
		ctx context.Context, machine allocation.UserMachine, check func(context.Context) (bool, error),
	) (bool, error)
}

func (c *WorkspaceCleaner) reclaimOps(ctx context.Context, m allocation.UserMachine) (userMachineReclaimer, error) {
	ops, err := c.machineOps(ctx, m)
	if err != nil {
		return nil, err
	}
	reclaimer, ok := ops.(userMachineReclaimer)
	if !ok {
		return nil, fmt.Errorf("sandboxalloc: config %d/%s does not reclaim user machines",
			m.ConfigTenantID, m.ConfigID)
	}
	return reclaimer, nil
}

// ReleaseSessions retires every session in sessionIDs in one transaction
// (C5): placement is closed for all of them, and each user-mode assignment
// is released with its cleanup task. handled names the sessions on a shared
// machine; the caller tears the others down as before. An error means
// nothing was retired; the caller must not delete the sessions.
func (c *WorkspaceCleaner) ReleaseSessions(ctx context.Context, sessionIDs []string) (map[string]bool, error) {
	out, err := c.svc.Retire(ctx, sessionIDs)
	if err != nil {
		return nil, err
	}
	handled := make(map[string]bool, len(out))
	for _, r := range out {
		if r.Assignment != nil && r.Assignment.MachineDigest != "" {
			handled[r.SessionID] = true
		}
	}
	return handled, nil
}

// UserSessions classifies existing placements without retiring them. Retirement
// must commit with the session deletion, not with this pre-delete lookup.
func (c *WorkspaceCleaner) UserSessions(ctx context.Context, ids []string) (map[string]bool, error) {
	handled := make(map[string]bool)
	for _, id := range ids {
		row, err := c.svc.repo.GetAssignment(ctx, id)
		if err != nil {
			return nil, err
		}
		if row != nil && row.MachineDigest != "" {
			handled[id] = true
		}
	}
	return handled, nil
}

// DeleteSessions atomically removes the authorized session rows and records
// retirement and cleanup intent. Only committed retirements may trigger cleanup.
func (c *WorkspaceCleaner) DeleteSessions(
	ctx context.Context, tenantID uint64, idsByOwner map[string][]string,
) (map[string]bool, int, error) {
	retired, err := c.svc.repo.DeleteSessionsRetiring(ctx, tenantID, idsByOwner, c.now())
	if err != nil {
		return nil, 0, err
	}
	handled := make(map[string]bool)
	for _, r := range retired {
		if r.Assignment != nil && r.Assignment.MachineDigest != "" {
			handled[r.SessionID] = true
		}
	}
	return handled, len(retired), nil
}

// RunCleanups runs the due cleanup of these sessions now, one command per
// machine. Anything not done stays due for RetryDue.
func (c *WorkspaceCleaner) RunCleanups(ctx context.Context, sessionIDs []string) {
	rows := make([]*types.SessionSandboxAssignment, 0, len(sessionIDs))
	for _, id := range sessionIDs {
		row, err := c.svc.repo.GetAssignment(ctx, id)
		if err != nil {
			logger.Warnf(ctx, "[sandbox-alloc] load cleanup of %s: %v", id, err)
			continue
		}
		if row != nil && row.CleanupDueAt != nil && row.MachineDigest != "" {
			rows = append(rows, row)
		}
	}
	c.execute(ctx, rows)
}

// RetryDue retries queued directory deletes whose backoff has elapsed.
func (c *WorkspaceCleaner) RetryDue(ctx context.Context, limit int) (int, int, error) {
	rows, err := c.svc.repo.ListDueWorkspaceCleanups(ctx, c.now(), limit)
	if err != nil {
		return 0, 0, err
	}
	done, failed := c.execute(ctx, rows)
	return done, failed, nil
}

func (c *WorkspaceCleaner) execute(ctx context.Context, rows []*types.SessionSandboxAssignment) (int, int) {
	batches := map[string][]*types.SessionSandboxAssignment{}
	for _, row := range rows {
		batches[row.MachineDigest] = append(batches[row.MachineDigest], row)
	}
	done, failed := 0, 0
	retry := func(row *types.SessionSandboxAssignment, reason string) {
		failed++
		logger.Warnf(ctx, "[sandbox-alloc] clean workspace of %s: %s", row.SessionID, reason)
		_ = c.svc.repo.RetryWorkspaceCleanup(ctx, row.SessionID, c.now().Add(cleanupBackoff(row.ReleasedAt, c.now())))
	}
	for digest, rows := range batches {
		// The machine's identity is its registry row. A reclaimed machine took
		// the directory with it (C5).
		reg, err := c.svc.repo.GetUserMachine(ctx, digest)
		if err != nil {
			for _, row := range rows {
				retry(row, err.Error())
			}
			continue
		}
		if reg == nil {
			for _, row := range rows {
				_ = c.svc.repo.CompleteWorkspaceCleanup(ctx, row.SessionID)
				done++
			}
			continue
		}
		b := struct {
			machine allocation.UserMachine
			rows    []*types.SessionSandboxAssignment
		}{machineOf(reg), rows}
		ids := make([]string, 0, len(b.rows))
		for _, row := range b.rows {
			ids = append(ids, row.SessionID)
		}
		var deleted []string
		ops, err := c.machineOps(ctx, b.machine)
		if err == nil {
			deleted, err = ops.DeleteSessionWorkspaces(ctx, b.machine, ids)
		}
		gone := make(map[string]bool, len(deleted))
		for _, id := range deleted {
			gone[id] = true
		}
		reason := "session operations still registered"
		if err != nil {
			reason = err.Error()
		}
		for _, row := range b.rows {
			if gone[row.SessionID] {
				_ = c.svc.repo.CompleteWorkspaceCleanup(ctx, row.SessionID)
				done++
				continue
			}
			retry(row, reason)
		}
	}
	return done, failed
}

// ReleaseOwnerAssignments retires every session of a deleted account and
// releases their assignments (C5). It must succeed before the account is
// deleted: afterwards nothing could release them, and their machines would
// never become empty.
func (c *WorkspaceCleaner) ReleaseOwnerAssignments(ctx context.Context, ownerID string) error {
	_, err := c.svc.repo.RetireOwnerSessions(ctx, ownerID, c.now())
	return err
}

// ReclaimOwner gives back the deleted account's machines now; any it cannot
// reclaim yet are left to the GC's retention window.
func (c *WorkspaceCleaner) ReclaimOwner(ctx context.Context, ownerID string) {
	machines, err := c.OwnerMachines(ctx, 0, ownerID)
	if err != nil {
		logger.Warnf(ctx, "[sandbox-alloc] list machines of %s: %v", ownerID, err)
		return
	}
	c.ReclaimOwnerMachines(ctx, machines)
}

// OwnerMachines lists ownerID's machines in one tenant (0 = every tenant).
func (c *WorkspaceCleaner) OwnerMachines(
	ctx context.Context, ownerTenantID uint64, ownerID string,
) ([]allocation.UserMachine, error) {
	rows, err := c.svc.repo.ListUserMachinesByOwner(ctx, ownerTenantID, ownerID)
	if err != nil {
		return nil, err
	}
	out := make([]allocation.UserMachine, 0, len(rows))
	for _, r := range rows {
		out = append(out, machineOf(r))
	}
	return out, nil
}

// ReleaseMachine deletes m now, when an admin chose to. Registered work and
// a live session still block it (false, nil): the same locked check
// ReclaimOwnerMachines uses decides, so a terminal is never cut and there is
// no second destroy path. A cleared empty_since is a session that joined,
// not an empty machine. Sessions on a refused machine keep their assignments.
func (c *WorkspaceCleaner) ReleaseMachine(ctx context.Context, m allocation.UserMachine) (bool, error) {
	return c.destroyIfEmpty(ctx, m)
}

// ReclaimOwnerMachines deletes machines whose sessions were all just deleted
// by "delete all" or a user deletion, without waiting for the retention
// window. A machine with a live session, a cleared empty clock, or registered
// work is left to the GC.
func (c *WorkspaceCleaner) ReclaimOwnerMachines(ctx context.Context, machines []allocation.UserMachine) {
	for _, m := range machines {
		if _, err := c.destroyIfEmpty(ctx, m); err != nil {
			logger.Warnf(ctx, "[sandbox-alloc] release machine %s: %v", digestPrefix(c.svc.Digest(m)), err)
		}
	}
}

// destroyIfEmpty deletes the provider sandbox and the registry row while the
// machine row is locked. The lock is taken inside the manager's check, which
// runs after the lifecycle lock and the admission barrier and before the
// provider delete, and it is released only after the registry delete. A nil
// empty_since is not empty: joining a machine clears that clock.
func (c *WorkspaceCleaner) destroyIfEmpty(ctx context.Context, m allocation.UserMachine) (bool, error) {
	ops, err := c.reclaimOps(ctx, m)
	if err != nil {
		return false, err
	}
	digest := c.svc.Digest(m)
	var lock interfaces.MachineReclaimLock
	ok, err := ops.ReclaimUserMachine(ctx, m, func(_ context.Context) (bool, error) {
		var lockErr error
		// The transaction outlives the manager's guarded provider context.
		// Keep caller cancellation, but hold the row lock through CommitDelete.
		lock, lockErr = c.svc.repo.LockEmptyMachine(ctx, digest)
		if lockErr != nil || lock == nil {
			return false, lockErr
		}
		return true, nil
	})
	if lock != nil {
		defer lock.Abort()
	}
	if err != nil || !ok {
		return false, err
	}
	if lock == nil {
		// The manager reported the machine reclaimable without ever running
		// the check that takes its row lock; nothing was provably deleted,
		// so no registry row may be dropped.
		return false, fmt.Errorf(
			"sandboxalloc: manager of config %d/%s skipped the reclaim check",
			m.ConfigTenantID, m.ConfigID)
	}
	deleted, delErr := lock.CommitDelete()
	if delErr != nil {
		logger.Warnf(ctx, "[sandbox-alloc] drop registry row %s: %v", digestPrefix(digest), delErr)
		return false, delErr
	}
	return deleted, nil
}

// machineOf is the allocation identity stored on a registry row.
func machineOf(reg *types.SandboxUserMachine) allocation.UserMachine {
	return allocation.UserMachine{
		OwnerTenantID: reg.OwnerTenantID, OwnerID: reg.OwnerID,
		ConfigTenantID: reg.ConfigTenantID, ConfigID: reg.ConfigID,
	}
}
