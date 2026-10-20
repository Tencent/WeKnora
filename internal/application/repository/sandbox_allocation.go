package repository

import (
	"context"
	"errors"
	"sort"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Tencent/WeKnora/internal/sandbox/workspace"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// errWebUserDeleted means the canonical web user row is already soft-deleted.
// Session create returns it as-is. ClaimPlacement maps it to ErrSessionRetired.
var errWebUserDeleted = errors.New("repository: web user is deleted")

// afterWebUserLock runs after lockLiveWebUser has locked a users row and
// before it returns. Tests insert a session on that same transaction; they
// clear the hook with t.Cleanup.
var afterWebUserLock func(tx *gorm.DB)

type sandboxAllocationRepository struct{ db *gorm.DB }

// NewSandboxAllocationRepository builds the allocation repository.
func NewSandboxAllocationRepository(db *gorm.DB) interfaces.SandboxAllocationRepository {
	return &sandboxAllocationRepository{db: db}
}

// forUpdate adds a row lock where the dialect has one. SQLite serializes
// writers already.
func forUpdate(tx *gorm.DB) *gorm.DB {
	switch tx.Name() {
	case "postgres", "mysql":
		return tx.Clauses(clause.Locking{Strength: "UPDATE"})
	default:
		return tx
	}
}

func (r *sandboxAllocationRepository) GetAssignment(
	ctx context.Context, sessionID string,
) (*types.SessionSandboxAssignment, error) {
	return getAssignment(r.db.WithContext(ctx), sessionID)
}

func getAssignment(tx *gorm.DB, sessionID string) (*types.SessionSandboxAssignment, error) {
	var a types.SessionSandboxAssignment
	err := tx.Where("session_id = ?", sessionID).Take(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// InsertSandboxAssignmentTx inserts a unless the session already has one, and
// for a user-mode row registers (or re-opens) machine. The fork path shares it
// so a forked session and its assignment commit together.
func InsertSandboxAssignmentTx(
	tx *gorm.DB, a *types.SessionSandboxAssignment, machine *types.SandboxUserMachine,
) (bool, error) {
	if (a.MachineDigest == "") != (machine == nil) || (machine != nil && machine.MachineDigest != a.MachineDigest) {
		return false, errors.New("sandbox assignment and its machine disagree")
	}
	if machine != nil {
		// Lock the machine row before the assignment exists. A reclaimer
		// holding it counts live assignments under READ COMMITTED and cannot
		// see an uncommitted insert, so inserting first would let it delete a
		// machine this session is joining. Waiting here means: the reclaimer
		// finishes and the upsert below registers a fresh machine, or the join
		// commits first and the reclaimer counts it.
		if err := lockMachineRow(tx, machine.MachineDigest); err != nil {
			return false, err
		}
	}
	res := tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "session_id"}}, DoNothing: true,
	}).Create(a)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 || machine == nil {
		return res.RowsAffected > 0, nil
	}
	// Joining always clears the empty clock, after the insert, so a release
	// that counted zero concurrently cannot leave it set.
	row := *machine
	row.EmptySince = nil
	err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "machine_digest"}},
		DoUpdates: clause.Assignments(map[string]any{"empty_since": nil}),
	}).Create(&row).Error
	return true, err
}

// lockLiveWebUser locks a canonical web user's row. Other owner ids are
// not web accounts and must not touch the users table. No row means this
// id is not a web account. A soft-deleted row returns errWebUserDeleted
// while the lock is still held.
func lockLiveWebUser(tx *gorm.DB, userID string) error {
	if !workspace.IsCanonicalUUID(userID) {
		return nil
	}
	var row struct {
		ID        string
		DeletedAt gorm.DeletedAt
	}
	err := forUpdate(tx.Unscoped().Model(&types.User{})).
		Select("id", "deleted_at").
		Where("id = ?", userID).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if afterWebUserLock != nil {
		afterWebUserLock(tx)
	}
	if row.DeletedAt.Valid {
		return errWebUserDeleted
	}
	return nil
}

func (r *sandboxAllocationRepository) ClaimPlacement(
	ctx context.Context, a *types.SessionSandboxAssignment, machine *types.SandboxUserMachine,
	expected interfaces.PlacementPin,
) (*types.SessionSandboxAssignment, error) {
	var out *types.SessionSandboxAssignment
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The owner row comes before the session row. Reading user_id without
		// a lock is what makes that order possible: the session lock is next.
		var owner struct {
			UserID string `gorm:"column:user_id"`
		}
		res := tx.Model(&types.Session{}).
			Select("user_id").
			Where("id = ?", a.SessionID).
			Take(&owner)
		if res.Error != nil {
			return res.Error // gorm.ErrRecordNotFound for a missing or soft-deleted session
		}
		if err := lockLiveWebUser(tx, owner.UserID); err != nil {
			if errors.Is(err, errWebUserDeleted) {
				return interfaces.ErrSessionRetired
			}
			return err
		}
		// Lock the session row: the pin lives on it, and an older replica's
		// conditional pin update waits on this lock instead of interleaving.
		var row struct {
			SandboxConfigID       *string
			SandboxConfigTenantID uint64
		}
		res = forUpdate(tx.Model(&types.Session{})).
			Select("sandbox_config_id", "sandbox_config_tenant_id").
			Where("id = ?", a.SessionID).Take(&row)
		if res.Error != nil {
			return res.Error
		}
		current := interfaces.PlacementPin{TenantID: row.SandboxConfigTenantID}
		if row.SandboxConfigID != nil {
			current.ConfigID = *row.SandboxConfigID
		}

		existing, err := getAssignment(tx, a.SessionID)
		if err != nil {
			return err
		}
		if existing != nil && existing.ReleasedAt != nil {
			// Released, or a tombstone: the session is being deleted.
			return interfaces.ErrSessionRetired
		}
		if existing != nil {
			out = existing
			return projectPin(tx, existing, current)
		}
		if current != expected {
			return interfaces.ErrPlacementConflict
		}
		created, err := InsertSandboxAssignmentTx(tx, a, machine)
		if err != nil {
			return err
		}
		if !created {
			return interfaces.ErrPlacementConflict
		}
		out = a
		return projectPin(tx, a, current)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// projectPin writes the assignment's config pair onto an unpinned session.
// A pin naming another config — including the same id in another tenant — is
// a conflict, never overwritten: the per-session machine it names may still
// exist. A legacy pin that has the id but not yet its tenant is completed.
func projectPin(tx *gorm.DB, a *types.SessionSandboxAssignment, current interfaces.PlacementPin) error {
	want := interfaces.PlacementPin{ConfigID: a.ConfigID, TenantID: a.ConfigTenantID}
	switch {
	case current == want:
		return nil
	case current.ConfigID == "", current.ConfigID == want.ConfigID && current.TenantID == 0:
		return tx.Model(&types.Session{}).Where("id = ?", a.SessionID).Updates(map[string]any{
			"sandbox_config_id": want.ConfigID, "sandbox_config_tenant_id": want.TenantID,
		}).Error
	default:
		return interfaces.ErrPlacementConflict
	}
}

func lockMachineRow(tx *gorm.DB, digest string) error {
	var row types.SandboxUserMachine
	err := forUpdate(tx.Model(&types.SandboxUserMachine{})).
		Select("machine_digest").Where("machine_digest = ?", digest).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	return err
}

func countLive(tx *gorm.DB, digest string) (int64, error) {
	var n int64
	err := tx.Model(&types.SessionSandboxAssignment{}).
		Where("machine_digest = ? AND released_at IS NULL", digest).Count(&n).Error
	return n, err
}

func (r *sandboxAllocationRepository) RetireSessions(
	ctx context.Context, sessionIDs []string, at time.Time,
) ([]interfaces.RetiredSession, error) {
	var out []interfaces.RetiredSession
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = RetireSessionsTx(tx, sessionIDs, at)
		return err
	})
	return out, err
}

// DeleteSessionsRetiring uses the same session-before-machine lock order as
// placement. Ownership is checked under the session locks so a stale service
// lookup cannot retire a session that has moved to another owner or tenant.
func (r *sandboxAllocationRepository) DeleteSessionsRetiring(
	ctx context.Context, tenantID uint64, idsByOwner map[string][]string, at time.Time,
) ([]interfaces.RetiredSession, error) {
	owners := make(map[string]string)
	for owner, ids := range idsByOwner {
		for _, id := range ids {
			owners[id] = owner
		}
	}
	ids := make([]string, 0, len(owners))
	for id := range owners {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var retired []interfaces.RetiredSession
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		live := make([]string, 0, len(ids))
		for _, id := range ids {
			var found []string
			q := tx.Model(&types.Session{}).Where("tenant_id = ? AND id = ?", tenantID, id)
			if owner := owners[id]; owner != "" {
				q = q.Where("user_id = ?", owner)
			} else {
				q = q.Where("user_id IS NULL OR user_id = ''")
			}
			if err := forUpdate(q).Pluck("id", &found).Error; err != nil {
				return err
			}
			live = append(live, found...)
		}
		if len(live) == 0 {
			return nil
		}
		var err error
		retired, err = RetireSessionsTx(tx, live, at)
		if err != nil {
			return err
		}
		return tx.Where("tenant_id = ? AND id IN ?", tenantID, live).Delete(&types.Session{}).Error
	})
	if err != nil {
		return nil, err
	}
	return retired, nil
}

// RetireOwnerSessions retires every session of a deleted Web account and
// releases their assignments in one transaction. The account's own sessions
// (any source, including ones never placed) and any session whose live
// assignment is on one of its machines are included: a session row may have
// been reassigned onto a machine this account still owns.
func (r *sandboxAllocationRepository) RetireOwnerSessions(
	ctx context.Context, ownerID string, at time.Time,
) ([]interfaces.RetiredSession, error) {
	var out []interfaces.RetiredSession
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ids, err := ownerSessionIDs(tx, ownerID)
		if err != nil {
			return err
		}
		out, err = RetireSessionsTx(tx, ids, at)
		return err
	})
	return out, err
}

// ownerSessionIDs is the account's own sessions, including ones never placed,
// plus any session whose live assignment still sits on one of its machines.
func ownerSessionIDs(tx *gorm.DB, ownerID string) ([]string, error) {
	var ids []string
	if err := tx.Unscoped().Model(&types.Session{}).
		Where("user_id = ?", ownerID).Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	var assigned []string
	if err := tx.Table("session_sandbox_assignments AS a").
		Joins("JOIN sandbox_user_machines AS m ON m.machine_digest = a.machine_digest").
		Where("m.owner_id = ? AND a.released_at IS NULL", ownerID).
		Pluck("a.session_id", &assigned).Error; err != nil {
		return nil, err
	}
	return append(ids, assigned...), nil
}

// RetireSessionsTx is RetireSessions inside the caller's transaction (T14
// retires a deleted account's sessions with it). Locks are session rows by
// id, then machine rows by digest. A caller that also locks the user row
// must do that first: user, then sessions, then machines.
func RetireSessionsTx(tx *gorm.DB, sessionIDs []string, at time.Time) ([]interfaces.RetiredSession, error) {
	at = at.UTC().Truncate(time.Microsecond)
	seen := make(map[string]bool, len(sessionIDs))
	ordered := make([]string, 0, len(sessionIDs))
	for _, id := range sessionIDs {
		if id != "" && !seen[id] {
			seen[id] = true
			ordered = append(ordered, id)
		}
	}
	locked := append([]string(nil), ordered...)
	sort.Strings(locked)
	exists := make(map[string]bool, len(locked))
	for _, id := range locked {
		// Unscoped: a session already soft-deleted by an older replica must
		// still be retired and its assignment released.
		var ids []string
		if err := forUpdate(tx.Unscoped().Model(&types.Session{})).
			Where("id = ?", id).Pluck("id", &ids).Error; err != nil {
			return nil, err
		}
		exists[id] = len(ids) > 0
	}

	byID := make(map[string]*types.SessionSandboxAssignment, len(ordered))
	var digests []string
	for _, id := range ordered {
		a, err := getAssignment(tx, id)
		if err != nil {
			return nil, err
		}
		if a == nil {
			if !exists[id] {
				continue
			}
			// Never placed: a tombstone makes any later placement fail.
			tomb := &types.SessionSandboxAssignment{SessionID: id, ReleasedAt: &at}
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "session_id"}}, DoNothing: true,
			}).Create(tomb).Error; err != nil {
				return nil, err
			}
			continue
		}
		if a.IsTombstone() {
			continue
		}
		byID[id] = a
		if a.ReleasedAt == nil && a.MachineDigest != "" {
			digests = append(digests, a.MachineDigest)
		}
	}
	sort.Strings(digests)
	for i, d := range digests {
		if i > 0 && digests[i-1] == d {
			continue
		}
		if err := lockMachineRow(tx, d); err != nil {
			return nil, err
		}
	}

	out := make([]interfaces.RetiredSession, 0, len(ordered))
	released := make([]bool, 0, len(ordered))
	for _, id := range ordered {
		res := interfaces.RetiredSession{SessionID: id, Assignment: byID[id]}
		a := byID[id]
		if a == nil || a.ReleasedAt != nil {
			out = append(out, res)
			released = append(released, false)
			continue
		}
		updates := map[string]any{"released_at": at}
		if a.MachineDigest != "" {
			// The directory on the shared machine goes with the release.
			updates["cleanup_due_at"] = at
			a.CleanupDueAt = &at
		}
		if err := tx.Model(&types.SessionSandboxAssignment{}).
			Where("session_id = ? AND released_at IS NULL", id).
			Updates(updates).Error; err != nil {
			return nil, err
		}
		a.ReleasedAt = &at
		out = append(out, res)
		released = append(released, true)
	}
	// Empty clocks last, once every release of this batch is visible.
	for i := range out {
		a := out[i].Assignment
		if !released[i] || a.MachineDigest == "" {
			continue
		}
		live, err := countLive(tx, a.MachineDigest)
		if err != nil {
			return nil, err
		}
		if live > 0 {
			continue
		}
		out[i].MachineEmpty = true
		if err := tx.Model(&types.SandboxUserMachine{}).
			Where("machine_digest = ? AND empty_since IS NULL", a.MachineDigest).
			Update("empty_since", at).Error; err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *sandboxAllocationRepository) HasSandboxCheckpoint(ctx context.Context, sessionID string) (bool, error) {
	var ids []string
	err := r.db.WithContext(ctx).Model(&types.Message{}).
		Where("session_id = ? AND sandbox_checkpoint IS NOT NULL", sessionID).
		Limit(1).Pluck("id", &ids).Error
	return len(ids) > 0, err
}

func (r *sandboxAllocationRepository) ListUserMachinesByConfig(
	ctx context.Context, configTenantID uint64, configID string,
) ([]*types.SandboxUserMachine, error) {
	var rows []*types.SandboxUserMachine
	err := r.db.WithContext(ctx).
		Where("config_tenant_id = ? AND config_id = ?", configTenantID, configID).
		Order("machine_digest").Find(&rows).Error
	return rows, err
}

func (r *sandboxAllocationRepository) ListUserMachinesByOwner(
	ctx context.Context, ownerTenantID uint64, ownerID string,
) ([]*types.SandboxUserMachine, error) {
	q := r.db.WithContext(ctx).Where("owner_id = ?", ownerID)
	if ownerTenantID != 0 {
		q = q.Where("owner_tenant_id = ?", ownerTenantID)
	}
	var rows []*types.SandboxUserMachine
	return rows, q.Order("machine_digest").Find(&rows).Error
}

func (r *sandboxAllocationRepository) ListEmptyUserMachines(
	ctx context.Context, emptyBefore time.Time, after interfaces.EmptyMachineCursor, limit int,
) ([]*types.SandboxUserMachine, error) {
	q := r.db.WithContext(ctx).Where("empty_since IS NOT NULL AND empty_since <= ?", emptyBefore)
	if !after.EmptySince.IsZero() {
		q = q.Where("(empty_since > ? OR (empty_since = ? AND machine_digest > ?))",
			after.EmptySince, after.EmptySince, after.Digest)
	}
	var rows []*types.SandboxUserMachine
	err := q.Order("empty_since").Order("machine_digest").Limit(limit).Find(&rows).Error
	return rows, err
}

func (r *sandboxAllocationRepository) GetUserMachine(
	ctx context.Context, digest string,
) (*types.SandboxUserMachine, error) {
	var m types.SandboxUserMachine
	err := r.db.WithContext(ctx).Where("machine_digest = ?", digest).Take(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *sandboxAllocationRepository) CountLiveAssignments(ctx context.Context, digest string) (int64, error) {
	return countLive(r.db.WithContext(ctx), digest)
}

func (r *sandboxAllocationRepository) DeleteUserMachineIfEmptySince(
	ctx context.Context, digest string, emptySince time.Time,
) (bool, error) {
	var deleted bool
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockMachineRow(tx, digest); err != nil {
			return err
		}
		live, err := countLive(tx, digest)
		if err != nil || live > 0 {
			return err
		}
		res := tx.Where("machine_digest = ? AND empty_since = ?", digest, emptySince).
			Delete(&types.SandboxUserMachine{})
		deleted = res.RowsAffected > 0
		return res.Error
	})
	return deleted, err
}

func (r *sandboxAllocationRepository) DeleteUserMachinesByConfig(
	ctx context.Context, configTenantID uint64, configID string,
) error {
	return r.db.WithContext(ctx).
		Where("config_tenant_id = ? AND config_id = ?", configTenantID, configID).
		Delete(&types.SandboxUserMachine{}).Error
}

// GormDB is the database this repository writes, so a caller can delete
// machine rows and another table in one transaction.
func (r *sandboxAllocationRepository) GormDB() *gorm.DB { return r.db }

// LockEmptyMachine locks a machine that is actually empty. See the interface.
func (r *sandboxAllocationRepository) LockEmptyMachine(
	ctx context.Context, digest string,
) (interfaces.MachineReclaimLock, error) {
	tx := r.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	fail := func(err error) (interfaces.MachineReclaimLock, error) {
		tx.Rollback()
		return nil, err
	}
	if err := lockMachineRow(tx, digest); err != nil {
		return fail(err)
	}
	var row types.SandboxUserMachine
	err := tx.Where("machine_digest = ?", digest).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && row.EmptySince == nil) {
		tx.Rollback()
		return nil, nil
	}
	if err != nil {
		return fail(err)
	}
	live, err := countLive(tx, digest)
	if err != nil {
		return fail(err)
	}
	if live > 0 {
		tx.Rollback()
		return nil, nil
	}
	since := *row.EmptySince
	return &machineReclaimLock{
		tx: tx, digest: digest, emptySince: since,
		configTenantID: row.ConfigTenantID, configID: row.ConfigID,
	}, nil
}

// machineReclaimLock holds one registry row until the provider delete finishes.
type machineReclaimLock struct {
	tx             *gorm.DB
	digest         string
	emptySince     time.Time
	configTenantID uint64
	configID       string
	done           bool
}

func (l *machineReclaimLock) Claim() interfaces.EmptyMachineClaim {
	return interfaces.EmptyMachineClaim{
		EmptySince: l.emptySince, ConfigTenantID: l.configTenantID, ConfigID: l.configID,
	}
}

func (l *machineReclaimLock) Abort() {
	if l == nil || l.done {
		return
	}
	l.done = true
	l.tx.Rollback()
}

func (l *machineReclaimLock) CommitDelete() (bool, error) {
	if l == nil || l.done {
		return false, errors.New("sandbox allocation: reclaim lock is already closed")
	}
	l.done = true
	live, err := countLive(l.tx, l.digest)
	if err != nil {
		l.tx.Rollback()
		return false, err
	}
	if live > 0 {
		l.tx.Rollback()
		return false, nil
	}
	res := l.tx.Where("machine_digest = ? AND empty_since = ?", l.digest, l.emptySince).
		Delete(&types.SandboxUserMachine{})
	if res.Error != nil || res.RowsAffected == 0 {
		l.tx.Rollback()
		return false, res.Error
	}
	if err := l.tx.Commit().Error; err != nil {
		l.tx.Rollback()
		return false, err
	}
	return true, nil
}

// DeleteUserMachinesAndSandboxConfig removes the config's user-machine rows
// and soft-deletes the config in one transaction. A missing config rolls
// the machine delete back, so a failed config delete cannot leave sessions
// pointing at a registry that cleanup treats as already gone.
func DeleteUserMachinesAndSandboxConfig(
	ctx context.Context, db *gorm.DB, tenantID uint64, configID string,
) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("config_tenant_id = ? AND config_id = ?", tenantID, configID).
			Delete(&types.SandboxUserMachine{}).Error; err != nil {
			return err
		}
		res := tx.Where("tenant_id = ? AND id = ?", tenantID, configID).
			Delete(&types.TenantSandboxConfigEntity{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func (r *sandboxAllocationRepository) ListDueWorkspaceCleanups(
	ctx context.Context, now time.Time, limit int,
) ([]*types.SessionSandboxAssignment, error) {
	var rows []*types.SessionSandboxAssignment
	err := r.db.WithContext(ctx).Where("cleanup_due_at IS NOT NULL AND cleanup_due_at <= ?", now).
		Order("cleanup_due_at").Order("session_id").Limit(limit).Find(&rows).Error
	return rows, err
}

func (r *sandboxAllocationRepository) CompleteWorkspaceCleanup(ctx context.Context, sessionID string) error {
	return r.db.WithContext(ctx).Model(&types.SessionSandboxAssignment{}).
		Where("session_id = ?", sessionID).Update("cleanup_due_at", nil).Error
}

func (r *sandboxAllocationRepository) RetryWorkspaceCleanup(
	ctx context.Context, sessionID string, next time.Time,
) error {
	// Only a cleanup still pending moves; a completed one stays done.
	return r.db.WithContext(ctx).Model(&types.SessionSandboxAssignment{}).
		Where("session_id = ? AND cleanup_due_at IS NOT NULL", sessionID).
		Update("cleanup_due_at", next.UTC().Truncate(time.Microsecond)).Error
}

var _ interfaces.SandboxAllocationRepository = (*sandboxAllocationRepository)(nil)
