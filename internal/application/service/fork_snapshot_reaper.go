// Package service - orphan fork snapshot collection.
//
// A fork takes a provider snapshot immediately but provisions the sandbox
// lazily. A branch the user never opens therefore leaves a snapshot behind
// forever. Persist can also fail after the snapshot already exists, or the
// process can crash before the session row is committed; those IDs live in
// fork_snapshot_leases so this reaper can still find them.
//
// The happy path tries to delete at consume time. Cube often refuses while
// the source and child sandboxes still hold runtime refs, so this reaper
// retries leftover snapshot IDs after those refs drop, and still collects
// forks that were never opened.
package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// DefaultForkSnapshotRetention is how long an unopened fork keeps its
// snapshot. A week is long enough that "I'll get back to that branch on
// Monday" works, and short enough that abandoned forks do not accumulate.
const DefaultForkSnapshotRetention = 7 * 24 * time.Hour

// DefaultForkSnapshotLeaseGrace is how long a snapshot lease lives before the
// reaper treats it as abandoned. It must outlast CreateForked (milliseconds)
// and a brief process restart, but not a week — there is no session for the
// user to come back to.
const DefaultForkSnapshotLeaseGrace = 15 * time.Minute

type reaperSessionStore interface {
	ListUnconsumedForks(ctx context.Context, olderThan time.Time) ([]*types.Session, error)
	UpdateForkBootstrap(ctx context.Context, sessionID string, b *types.ForkBootstrap) error
	UnconsumedForkSnapshotHolders(ctx context.Context, snapshotID string) ([]string, error)
	ListStaleForkSnapshotLeases(ctx context.Context, olderThan time.Time) ([]*types.ForkSnapshotLease, error)
	DeleteForkSnapshotLease(ctx context.Context, snapshotID string) error
}

type forkSnapshotReleaseStore interface {
	HasOtherUnconsumedForkSnapshot(ctx context.Context, snapshotID, excludeSessionID string) (bool, error)
	CreateForkSnapshotLease(ctx context.Context, lease *types.ForkSnapshotLease) error
}

// ForkSnapshotReaper deletes snapshots of forks that were never opened.
type ForkSnapshotReaper struct {
	sessions  reaperSessionStore
	snapshots ForkSnapshotDeleter
	retention time.Duration
}

// NewForkSnapshotReaper wires the reaper.
func NewForkSnapshotReaper(
	sessions reaperSessionStore, snapshots ForkSnapshotDeleter, retention time.Duration,
) *ForkSnapshotReaper {
	if retention <= 0 {
		retention = DefaultForkSnapshotRetention
	}
	return &ForkSnapshotReaper{sessions: sessions, snapshots: snapshots, retention: retention}
}

// NewForkSnapshotReaperFromRepos is the exported DI constructor. It uses the
// default retention; tests inject a shorter window through NewForkSnapshotReaper.
func NewForkSnapshotReaperFromRepos(
	sessions interfaces.SessionRepository, snapshots ForkSnapshotDeleter,
) *ForkSnapshotReaper {
	return NewForkSnapshotReaper(sessions, snapshots, DefaultForkSnapshotRetention)
}

type forkSessionSnapshotDeleter interface {
	DeleteForkSnapshot(ctx context.Context, tenantID uint64, sandboxConfigID, snapshotID string) error
}

// ReapOnce runs a single collection pass and reports how many snapshots it
// retired.
//
// A per-snapshot delete failure is logged and skipped rather than returned:
// one unreachable snapshot must not stop the pass, and the next run retries it.
func (r *ForkSnapshotReaper) ReapOnce(ctx context.Context) (int, error) {
	if r == nil || r.sessions == nil || r.snapshots == nil {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-r.retention)
	stale, err := r.sessions.ListUnconsumedForks(ctx, cutoff)
	if err != nil {
		return 0, err
	}

	staleIDs := make(map[string]struct{}, len(stale))
	for _, session := range stale {
		if session != nil {
			staleIDs[session.ID] = struct{}{}
		}
	}

	deleted := make(map[string]bool)
	skipped := make(map[string]bool)
	reaped := 0
	for _, session := range stale {
		if session == nil || session.ForkBootstrap == nil {
			continue
		}
		snapshotID := session.ForkBootstrap.SnapshotID
		if strings.TrimSpace(snapshotID) == "" {
			continue
		}
		if skipped[snapshotID] {
			continue
		}
		if deleted[snapshotID] {
			if err := r.sessions.UpdateForkBootstrap(ctx, session.ID, nil); err != nil {
				logger.Warnf(ctx, "[ForkSnapshotReaper] clear bootstrap of %s failed: %v",
					session.ID, err)
				continue
			}
			reaped++
			continue
		}

		holders, shareErr := r.sessions.UnconsumedForkSnapshotHolders(ctx, snapshotID)
		if shareErr != nil {
			logger.Warnf(ctx, "[ForkSnapshotReaper] lookup snapshot holders %s failed; leaving it: %v",
				snapshotID, shareErr)
			continue
		}
		if snapshotHeldOutsideBatch(holders, staleIDs) {
			skipped[snapshotID] = true
			continue
		}

		if err := r.deleteSnapshot(ctx, session, snapshotID); err != nil {
			// Clearing the bootstrap now would drop the only record of this
			// snapshot's ID and leak it permanently. Leave it for next time.
			logger.Warnf(ctx, "[ForkSnapshotReaper] delete %s (session=%s) failed: %v",
				snapshotID, session.ID, err)
			continue
		}
		if err := r.sessions.UpdateForkBootstrap(ctx, session.ID, nil); err != nil {
			logger.Warnf(ctx, "[ForkSnapshotReaper] clear bootstrap of %s failed: %v",
				session.ID, err)
			continue
		}
		deleted[snapshotID] = true
		reaped++
	}
	leased, err := r.reapSnapshotLeases(ctx, deleted, skipped)
	if err != nil {
		return reaped, err
	}
	reaped += leased
	if reaped > 0 {
		logger.Infof(ctx, "[ForkSnapshotReaper] reaped %d orphan fork snapshot(s)", reaped)
	}
	return reaped, nil
}

// releaseForkSnapshotOnDelete retires a fork snapshot that no remaining live
// session needs. Call it after the session row is gone so a sibling that still
// lists the same snapshot is not stranded. Delete failure writes a lease so
// the reaper can retry; lookup failure does the same rather than risk deleting
// a snapshot another fork still needs.
func releaseForkSnapshotOnDelete(
	ctx context.Context,
	sessions forkSnapshotReleaseStore,
	snapshots ForkSnapshotDeleter,
	session *types.Session,
) {
	if session == nil || session.ForkBootstrap == nil {
		return
	}
	snapshotID := strings.TrimSpace(session.ForkBootstrap.SnapshotID)
	if snapshotID == "" {
		return
	}
	if sessions != nil {
		keep, err := sessions.HasOtherUnconsumedForkSnapshot(ctx, snapshotID, session.ID)
		if err != nil {
			logger.Warnf(ctx, "[SessionFork] lookup snapshot holders %s failed; leasing it: %v",
				snapshotID, err)
			leaseForkSnapshotOnDelete(ctx, sessions, session, snapshotID)
			return
		}
		if keep {
			return
		}
	}
	if err := deleteForkSnapshot(ctx, snapshots, session, snapshotID); err != nil {
		logger.Warnf(ctx, "[SessionFork] delete snapshot %s on session delete failed: %v",
			snapshotID, err)
		leaseForkSnapshotOnDelete(ctx, sessions, session, snapshotID)
	}
}

func leaseForkSnapshotOnDelete(
	ctx context.Context,
	sessions forkSnapshotReleaseStore,
	session *types.Session,
	snapshotID string,
) {
	if sessions == nil || session == nil || strings.TrimSpace(snapshotID) == "" {
		return
	}
	lease := &types.ForkSnapshotLease{
		SnapshotID: snapshotID,
		// Pairs with SandboxConfigID: the workspace that owns the config, not
		// necessarily the one that owns the session (shared agents).
		TenantID:        session.SandboxConfigOwner(),
		SandboxConfigID: session.SandboxConfigID,
		CreatedAt:       time.Now().UTC(),
	}
	if err := sessions.CreateForkSnapshotLease(ctx, lease); err != nil {
		logger.Warnf(ctx, "[SessionFork] record snapshot lease %s failed: %v", snapshotID, err)
	}
}

func (r *ForkSnapshotReaper) reapSnapshotLeases(
	ctx context.Context, deleted, skipped map[string]bool,
) (int, error) {
	cutoff := time.Now().UTC().Add(-DefaultForkSnapshotLeaseGrace)
	leases, err := r.sessions.ListStaleForkSnapshotLeases(ctx, cutoff)
	if err != nil {
		return 0, err
	}
	reaped := 0
	for _, lease := range leases {
		if lease == nil || strings.TrimSpace(lease.SnapshotID) == "" {
			continue
		}
		snapshotID := lease.SnapshotID
		if skipped[snapshotID] {
			continue
		}
		holders, shareErr := r.sessions.UnconsumedForkSnapshotHolders(ctx, snapshotID)
		if shareErr != nil {
			logger.Warnf(ctx, "[ForkSnapshotReaper] lookup snapshot holders %s failed; leaving lease: %v",
				snapshotID, shareErr)
			continue
		}
		if len(holders) > 0 {
			if err := r.sessions.DeleteForkSnapshotLease(ctx, snapshotID); err != nil {
				logger.Warnf(ctx, "[ForkSnapshotReaper] clear lease %s failed: %v", snapshotID, err)
			}
			continue
		}
		if deleted[snapshotID] {
			if err := r.sessions.DeleteForkSnapshotLease(ctx, snapshotID); err != nil {
				logger.Warnf(ctx, "[ForkSnapshotReaper] clear lease %s failed: %v", snapshotID, err)
				continue
			}
			reaped++
			continue
		}
		placeholder := &types.Session{
			TenantID:        lease.TenantID,
			SandboxConfigID: lease.SandboxConfigID,
		}
		if err := r.deleteSnapshot(ctx, placeholder, snapshotID); err != nil {
			logger.Warnf(ctx, "[ForkSnapshotReaper] delete leased snapshot %s failed: %v",
				snapshotID, err)
			continue
		}
		if err := r.sessions.DeleteForkSnapshotLease(ctx, snapshotID); err != nil {
			logger.Warnf(ctx, "[ForkSnapshotReaper] clear lease %s failed: %v", snapshotID, err)
			continue
		}
		deleted[snapshotID] = true
		reaped++
	}
	return reaped, nil
}

func snapshotHeldOutsideBatch(holders []string, staleIDs map[string]struct{}) bool {
	for _, id := range holders {
		if _, inBatch := staleIDs[id]; !inBatch {
			return true
		}
	}
	return false
}

func (r *ForkSnapshotReaper) deleteSnapshot(
	ctx context.Context, session *types.Session, snapshotID string,
) error {
	return deleteForkSnapshot(ctx, r.snapshots, session, snapshotID)
}

func deleteForkSnapshot(
	ctx context.Context, snapshots ForkSnapshotDeleter, session *types.Session, snapshotID string,
) error {
	if snapshots == nil {
		return errors.New("fork snapshot deleter is nil")
	}
	var err error
	if session != nil {
		if scoped, ok := snapshots.(forkSessionSnapshotDeleter); ok {
			err = scoped.DeleteForkSnapshot(
				ctx, session.SandboxConfigOwner(), session.SandboxConfigID, snapshotID)
		} else {
			err = snapshots.DeleteSnapshot(ctx, snapshotID)
		}
	} else {
		err = snapshots.DeleteSnapshot(ctx, snapshotID)
	}
	if err == nil || sandbox.IsRemoteNotFound(err) {
		return nil
	}
	return err
}

type resolverForkSnapshotDeleter struct {
	resolver sandbox.TenantSandboxResolver
	fallback sandbox.Manager
}

// NewResolverForkSnapshotDeleter deletes fork snapshots through the tenant's
// sandbox manager. Cube and E2B credentials are per-tenant, so an ID-only
// delete cannot reach them.
func NewResolverForkSnapshotDeleter(
	resolver sandbox.TenantSandboxResolver, fallback sandbox.Manager,
) ForkSnapshotDeleter {
	return &resolverForkSnapshotDeleter{resolver: resolver, fallback: fallback}
}

func (d *resolverForkSnapshotDeleter) DeleteSnapshot(context.Context, string) error {
	return errors.New("fork snapshot delete requires tenant and sandbox config")
}

type managerSnapshotDeleter interface {
	DeleteSnapshot(ctx context.Context, snapshotID string) error
}

func (d *resolverForkSnapshotDeleter) DeleteForkSnapshot(
	ctx context.Context, tenantID uint64, sandboxConfigID, snapshotID string,
) error {
	if d == nil {
		return errors.New("fork snapshot deleter is nil")
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, tenantID)
	mgr, err := resolveTenantSandboxForConfig(ctx, d.resolver, d.fallback, tenantID, sandboxConfigID, nil)
	if err != nil {
		return err
	}
	deleter, ok := mgr.(managerSnapshotDeleter)
	if !ok || deleter == nil {
		return errors.New("sandbox: resolved manager does not support snapshot delete")
	}
	return deleter.DeleteSnapshot(ctx, snapshotID)
}

var (
	_ ForkSnapshotDeleter        = (*resolverForkSnapshotDeleter)(nil)
	_ forkSessionSnapshotDeleter = (*resolverForkSnapshotDeleter)(nil)
)
