package interfaces

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// ErrPlacementConflict is returned when the session's pin changed between the
// caller's read and the placement transaction. Re-read the session and decide again.
var ErrPlacementConflict = errors.New("sandbox placement conflict")

// ErrSessionRetired is returned when the session is being deleted (or its
// owner was). It may never get a sandbox placement again.
var ErrSessionRetired = errors.New("sandbox: session is retired")

// RetiredSession is one session's outcome of RetireSessions. Assignment is
// nil when the session was never placed (it got a tombstone). A released
// user-mode assignment has CleanupDueAt set: its directory still has to go.
type RetiredSession struct {
	SessionID    string
	Assignment   *types.SessionSandboxAssignment
	MachineEmpty bool
}

// EmptyMachineCursor is the keyset position of ListEmptyUserMachines. The
// zero value starts from the beginning.
type EmptyMachineCursor struct {
	EmptySince time.Time
	Digest     string
}

// EmptyMachineClaim is the identity a reclaimer verified under the row lock.
type EmptyMachineClaim struct {
	EmptySince     time.Time
	ConfigTenantID uint64
	ConfigID       string
}

// MachineReclaimLock is the open row lock from LockEmptyMachine.
type MachineReclaimLock interface {
	Claim() EmptyMachineClaim
	// Abort releases the lock without deleting the row.
	Abort()
	// CommitDelete drops the row only if it is still empty at the same
	// empty_since, then releases the lock. False means the row stayed.
	CommitDelete() (bool, error)
}

// PlacementPin is the (sandbox_config_id, sandbox_config_tenant_id) pair on
// the sessions row. A zero ConfigID means the session has no pin.
type PlacementPin struct {
	ConfigID string
	TenantID uint64
}

// SandboxAllocationRepository persists session placements and shared machines.
type SandboxAllocationRepository interface {
	// GetAssignment returns the session's row, tombstones included.
	GetAssignment(ctx context.Context, sessionID string) (*types.SessionSandboxAssignment, error)
	// ClaimPlacement records a's placement and projects it onto the session's
	// pin in one transaction that locks the session row. machine is the
	// shared machine a user-mode placement joins (nil for a machine of its
	// own); it is registered, and its empty clock cleared, with the row. An
	// existing assignment wins and is returned unchanged. expected is the pin
	// the caller decided from; if the row holds anything else, it returns
	// ErrPlacementConflict. A soft-deleted or missing session is
	// gorm.ErrRecordNotFound; a released one (tombstones included) is
	// ErrSessionRetired.
	ClaimPlacement(ctx context.Context, a *types.SessionSandboxAssignment, machine *types.SandboxUserMachine,
		expected PlacementPin) (*types.SessionSandboxAssignment, error)
	// RetireSessions is the first step of deleting sessions. In one
	// transaction it locks each session row — the lock ClaimPlacement takes —
	// and releases its live assignment, scheduling the workspace cleanup of a
	// user-mode one. A session without an assignment gets a tombstone, so a
	// placement racing the deletion cannot land after it. Idempotent; results
	// are in sessionIDs order.
	RetireSessions(ctx context.Context, sessionIDs []string, at time.Time) ([]RetiredSession, error)
	// DeleteSessionsRetiring soft-deletes the listed owner-scoped sessions and
	// retires their assignments in one transaction. Missing rows are skipped.
	// An error rolls back the entire batch, including queued workspace cleanup.
	DeleteSessionsRetiring(ctx context.Context, tenantID uint64, idsByOwner map[string][]string,
		at time.Time) ([]RetiredSession, error)
	// RetireOwnerSessions retires every session of a Web account in one
	// transaction: the account's own sessions in every tenant (including
	// sessions that were never placed) and any session whose live assignment
	// is on one of that account's machines. It releases those assignments,
	// writes the cleanup task of a user-mode one, and sets empty_since on a
	// machine this leaves empty. A later ClaimPlacement returns
	// ErrSessionRetired.
	RetireOwnerSessions(ctx context.Context, ownerID string, at time.Time) ([]RetiredSession, error)
	HasSandboxCheckpoint(ctx context.Context, sessionID string) (bool, error)

	ListUserMachinesByConfig(
		ctx context.Context, configTenantID uint64, configID string,
	) ([]*types.SandboxUserMachine, error)
	ListUserMachinesByOwner(
		ctx context.Context, ownerTenantID uint64, ownerID string,
	) ([]*types.SandboxUserMachine, error)
	// ListEmptyUserMachines pages the machines empty since before emptyBefore
	// in (empty_since, machine_digest) order, strictly after the cursor, so a
	// candidate that is skipped never hides the ones behind it.
	ListEmptyUserMachines(
		ctx context.Context, emptyBefore time.Time, after EmptyMachineCursor, limit int,
	) ([]*types.SandboxUserMachine, error)
	GetUserMachine(ctx context.Context, digest string) (*types.SandboxUserMachine, error)
	CountLiveAssignments(ctx context.Context, digest string) (int64, error)
	// DeleteUserMachineIfEmptySince drops the registry row only while it
	// still has no live assignment and its empty_since is exactly
	// emptySince — the value the reclaimer verified — so a machine that was
	// joined and emptied again meanwhile keeps its row and its new clock.
	DeleteUserMachineIfEmptySince(ctx context.Context, digest string, emptySince time.Time) (bool, error)
	// LockEmptyMachine locks the registry row when the machine has no live
	// assignment and a non-nil empty_since. A nil lock means it is not
	// reclaimable (missing row, a live session, or a cleared clock — a join
	// clears the clock). The lock is held until Abort or CommitDelete, and
	// that span must cover the provider delete: a placement takes the same
	// row lock before its assignment exists.
	LockEmptyMachine(ctx context.Context, digest string) (MachineReclaimLock, error)
	DeleteUserMachinesByConfig(ctx context.Context, configTenantID uint64, configID string) error

	// ListDueWorkspaceCleanups returns released assignments whose directory
	// cleanup is due at now, oldest first.
	ListDueWorkspaceCleanups(ctx context.Context, now time.Time, limit int) ([]*types.SessionSandboxAssignment, error)
	CompleteWorkspaceCleanup(ctx context.Context, sessionID string) error
	// RetryWorkspaceCleanup moves the session's next cleanup attempt to next.
	RetryWorkspaceCleanup(ctx context.Context, sessionID string, next time.Time) error
}
