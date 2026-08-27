package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TenantSkillRepository persists skills installed onto sandbox configs and the
// snapshot chain ledger.
type TenantSkillRepository interface {
	// CreateSkill inserts a metadata projection row before provider-side image work starts.
	CreateSkill(ctx context.Context, e *types.TenantSkillEntity) error
	// GetSkill returns nil (no error) when the skill does not exist or belongs
	// to another workspace/config, so callers can render a 404 directly.
	GetSkill(ctx context.Context, tenantID uint64, configID, skillID string) (*types.TenantSkillEntity, error)
	// GetSkillByName scopes lookup by config because skill names are only unique within a config.
	GetSkillByName(ctx context.Context, tenantID uint64, configID, name string) (*types.TenantSkillEntity, error)
	// ListSkillsByConfig returns the installed skill projection for one sandbox config.
	ListSkillsByConfig(ctx context.Context, tenantID uint64, configID string) ([]*types.TenantSkillEntity, error)
	// ListSkillsByTenant spans every sandbox config of one workspace, for the
	// views that are about the user rather than about a config.
	ListSkillsByTenant(ctx context.Context, tenantID uint64) ([]*types.TenantSkillEntity, error)
	// UpdateSkill writes the mutable projection fields after install/remove
	// state changes. It never touches envs; see the implementation.
	UpdateSkill(ctx context.Context, e *types.TenantSkillEntity) error
	// UpdateSkillEnvs writes the declared environment variables alone.
	UpdateSkillEnvs(
		ctx context.Context, tenantID uint64, configID, skillID string, envs types.SkillEnvVars,
	) error
	// UpdateSkillAdminState writes visibility and declared values together, as
	// one admin request.
	UpdateSkillAdminState(
		ctx context.Context,
		tenantID uint64,
		configID, skillID string,
		enabled bool,
		envs types.SkillEnvVars,
	) error
	// DeleteSkill soft-deletes the metadata row and, in the same transaction,
	// hard-deletes the per-principal env values hanging off it. Image cleanup is
	// represented by snapshots. When the scoped key matches no skill, nothing is
	// deleted and the call succeeds.
	DeleteSkill(ctx context.Context, tenantID uint64, configID, skillID string) error
	// ListStaleInstalling finds abandoned install/remove runs for the reaper.
	ListStaleInstalling(ctx context.Context, olderThan time.Time) ([]*types.TenantSkillEntity, error)

	// CreateSnapshotRow records provider work before creating the billable snapshot.
	CreateSnapshotRow(ctx context.Context, e *types.TenantSkillSnapshotEntity) error
	// MarkSnapshotState updates ledger state and stores the provider snapshot ID once known.
	MarkSnapshotState(ctx context.Context, tenantID uint64, id, state, snapshotID string) error
	// ListSnapshotsByConfig returns the full chain for audit and troubleshooting.
	ListSnapshotsByConfig(ctx context.Context, tenantID uint64, configID string) ([]*types.TenantSkillSnapshotEntity, error)
	// DeleteSnapshotRowsByConfig removes ledger rows only when an entire sandbox
	// config is deleted and its provider-side snapshots are already gone; never
	// call this during an ordinary image switch (old snapshots stay in the ledger).
	DeleteSnapshotRowsByConfig(ctx context.Context, tenantID uint64, configID string) error

	// ListUserEnvVars returns one principal's own values for one scope,
	// decrypted. An empty skillID selects the config-wide variables.
	ListUserEnvVars(
		ctx context.Context, tenantID uint64, p types.Principal, configID, skillID string,
	) ([]*types.TenantUserEnvVar, error)
	// ListUserEnvVarsByConfig returns every scope at once, for the settings
	// page that renders a whole config in one go.
	ListUserEnvVarsByConfig(
		ctx context.Context, tenantID uint64, p types.Principal, configID string,
	) ([]*types.TenantUserEnvVar, error)
	// UpsertUserEnvVar writes a principal's value, replacing any previous one
	// for the same (tenant, principal, config, skill, name).
	UpsertUserEnvVar(ctx context.Context, e *types.TenantUserEnvVar) error
	// DeleteUserEnvVar removes one value, returning types.ErrEnvVarNotFound
	// when there was nothing to remove.
	DeleteUserEnvVar(
		ctx context.Context, tenantID uint64, p types.Principal, configID, skillID, name string,
	) error
	// DeleteUserEnvVarsByConfig removes every principal's values for a config,
	// including the config-wide ones DeleteSkill never sees.
	DeleteUserEnvVarsByConfig(ctx context.Context, tenantID uint64, configID string) error
}

type tenantSkillRepository struct{ db *gorm.DB }

// NewTenantSkillRepository returns a GORM-backed implementation.
func NewTenantSkillRepository(db *gorm.DB) TenantSkillRepository {
	return &tenantSkillRepository{db: db}
}

func (r *tenantSkillRepository) CreateSkill(ctx context.Context, e *types.TenantSkillEntity) error {
	return r.db.WithContext(ctx).Create(e).Error
}

func (r *tenantSkillRepository) GetSkill(
	ctx context.Context, tenantID uint64, configID, skillID string,
) (*types.TenantSkillEntity, error) {
	var e types.TenantSkillEntity
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND sandbox_config_id = ? AND id = ?", tenantID, configID, skillID).
		First(&e).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (r *tenantSkillRepository) GetSkillByName(
	ctx context.Context, tenantID uint64, configID, name string,
) (*types.TenantSkillEntity, error) {
	var e types.TenantSkillEntity
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND sandbox_config_id = ? AND name = ?", tenantID, configID, name).
		First(&e).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (r *tenantSkillRepository) ListSkillsByConfig(
	ctx context.Context, tenantID uint64, configID string,
) ([]*types.TenantSkillEntity, error) {
	var list []*types.TenantSkillEntity
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND sandbox_config_id = ?", tenantID, configID).
		Order("created_at ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

func (r *tenantSkillRepository) ListSkillsByTenant(
	ctx context.Context, tenantID uint64,
) ([]*types.TenantSkillEntity, error) {
	var list []*types.TenantSkillEntity
	err := r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("created_at ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

// UpdateSkill writes the mutable columns explicitly so a zero-valued field on
// the passed entity cannot silently wipe state written by a concurrent job.
//
// envs is deliberately absent: it is the one column an install-progress write
// has no opinion about, and every such write is a read-modify-write of a row
// the install heartbeat reloads every 30 seconds. Including it would let a
// heartbeat that read the row a moment before a declaration was recorded put
// the stale list — or a NULL — back. UpdateSkillEnvs and UpdateSkillAdminState
// are the only writers of that column.
func (r *tenantSkillRepository) UpdateSkill(ctx context.Context, e *types.TenantSkillEntity) error {
	return r.db.WithContext(ctx).
		Model(&types.TenantSkillEntity{}).
		Where("tenant_id = ? AND sandbox_config_id = ? AND id = ?", e.TenantID, e.SandboxConfigID, e.ID).
		Updates(map[string]any{
			"name":                  e.Name,
			"version":               e.Version,
			"description":           e.Description,
			"instructions":          e.Instructions,
			"bundle_ref":            e.BundleRef,
			"bundle_sha256":         e.BundleSHA256,
			"enabled":               e.Enabled,
			"installed_snapshot_id": e.InstalledSnapshotID,
			"install_session_id":    e.InstallSessionID,
			"install_message_id":    e.InstallMessageID,
			"status":                e.Status,
			"error":                 e.Error,
			"installing_since":      e.InstallingSince,
			"updated_at":            time.Now(),
		}).Error
}

// UpdateSkillEnvs writes the declaration column alone, so recording what a
// skill needs cannot disturb the install state written next to it.
func (r *tenantSkillRepository) UpdateSkillEnvs(
	ctx context.Context, tenantID uint64, configID, skillID string, envs types.SkillEnvVars,
) error {
	return r.db.WithContext(ctx).
		Model(&types.TenantSkillEntity{}).
		Where("tenant_id = ? AND sandbox_config_id = ? AND id = ?", tenantID, configID, skillID).
		Updates(map[string]any{"envs": envs, "updated_at": time.Now()}).Error
}

// UpdateSkillAdminState writes the two columns an admin request owns, together,
// so a rotation that disables the skill and clears its value is one statement
// rather than two that can half apply.
func (r *tenantSkillRepository) UpdateSkillAdminState(
	ctx context.Context,
	tenantID uint64,
	configID, skillID string,
	enabled bool,
	envs types.SkillEnvVars,
) error {
	return r.db.WithContext(ctx).
		Model(&types.TenantSkillEntity{}).
		Where("tenant_id = ? AND sandbox_config_id = ? AND id = ?", tenantID, configID, skillID).
		Updates(map[string]any{
			"enabled":    enabled,
			"envs":       envs,
			"updated_at": time.Now(),
		}).Error
}

// DeleteSkill soft-deletes the skill and hard-deletes the user values that
// belonged to it, in one transaction. The cleanup cannot be a cascading foreign
// key: tenant_skills is soft-deleted, so ON DELETE CASCADE would never fire and
// the credentials would outlive the skill they were entered for.
func (r *tenantSkillRepository) DeleteSkill(
	ctx context.Context, tenantID uint64, configID, skillID string,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.
			Where("tenant_id = ? AND sandbox_config_id = ? AND id = ?", tenantID, configID, skillID).
			Delete(&types.TenantSkillEntity{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		if err := tx.
			Where("tenant_id = ? AND skill_id = ?", tenantID, skillID).
			Delete(&types.TenantUserEnvVar{}).Error; err != nil {
			return err
		}
		return nil
	})
}

func (r *tenantSkillRepository) ListStaleInstalling(
	ctx context.Context, olderThan time.Time,
) ([]*types.TenantSkillEntity, error) {
	var list []*types.TenantSkillEntity
	err := r.db.WithContext(ctx).
		Where("status IN ? AND installing_since IS NOT NULL AND installing_since < ?",
			[]string{types.SkillStatusInstalling, types.SkillStatusRemoving}, olderThan).
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

func (r *tenantSkillRepository) CreateSnapshotRow(
	ctx context.Context, e *types.TenantSkillSnapshotEntity,
) error {
	return r.db.WithContext(ctx).Create(e).Error
}

// MarkSnapshotState moves a ledger row and, when the snapshot has just been
// created, records its provider-side ID.
func (r *tenantSkillRepository) MarkSnapshotState(
	ctx context.Context, tenantID uint64, id, state, snapshotID string,
) error {
	updates := map[string]any{"state": state, "updated_at": time.Now()}
	if snapshotID != "" {
		updates["snapshot_id"] = snapshotID
	}
	if state == types.SkillSnapshotStateSuperseded {
		now := time.Now()
		updates["superseded_at"] = &now
	}
	return r.db.WithContext(ctx).
		Model(&types.TenantSkillSnapshotEntity{}).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Updates(updates).Error
}

func (r *tenantSkillRepository) ListSnapshotsByConfig(
	ctx context.Context, tenantID uint64, configID string,
) ([]*types.TenantSkillSnapshotEntity, error) {
	var list []*types.TenantSkillSnapshotEntity
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND sandbox_config_id = ?", tenantID, configID).
		Order("generation ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

// DeleteSnapshotRowsByConfig removes all ledger rows for a sandbox config.
// This is only legitimate when the entire sandbox config is being deleted and
// its provider-side snapshots have already been destroyed. During an ordinary
// image switch, old snapshots are never deleted—the ledger records their IDs
// and deleting rows would leave those IDs dangling. Here the config itself no
// longer exists, so keeping rows would point at a deleted config instead.
func (r *tenantSkillRepository) DeleteSnapshotRowsByConfig(
	ctx context.Context, tenantID uint64, configID string,
) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND sandbox_config_id = ?", tenantID, configID).
		Delete(&types.TenantSkillSnapshotEntity{}).Error
}

func (r *tenantSkillRepository) ListUserEnvVars(
	ctx context.Context, tenantID uint64, p types.Principal, configID, skillID string,
) ([]*types.TenantUserEnvVar, error) {
	p = p.Normalize()
	var list []*types.TenantUserEnvVar
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND principal_type = ? AND principal_id = ?"+
			" AND sandbox_config_id = ? AND skill_id = ?",
			tenantID, p.Type, p.ID, configID, skillID).
		Order("name ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

func (r *tenantSkillRepository) ListUserEnvVarsByConfig(
	ctx context.Context, tenantID uint64, p types.Principal, configID string,
) ([]*types.TenantUserEnvVar, error) {
	p = p.Normalize()
	var list []*types.TenantUserEnvVar
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND principal_type = ? AND principal_id = ? AND sandbox_config_id = ?",
			tenantID, p.Type, p.ID, configID).
		Order("skill_id ASC, name ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

// UpsertUserEnvVar conflicts on the unique index columns so a re-entered value
// replaces the previous one instead of accumulating rows.
func (r *tenantSkillRepository) UpsertUserEnvVar(
	ctx context.Context, e *types.TenantUserEnvVar,
) error {
	// Persist a copy: BeforeSave encrypts the receiver in place, and the
	// caller keeps holding the plaintext it passed in.
	stored := *e
	p := types.Principal{Type: stored.PrincipalType, ID: stored.PrincipalID}.Normalize()
	stored.PrincipalType, stored.PrincipalID = p.Type, p.ID
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "tenant_id"}, {Name: "principal_type"}, {Name: "principal_id"},
				{Name: "sandbox_config_id"}, {Name: "skill_id"}, {Name: "name"},
			},
			DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
		}).
		Create(&stored).Error
}

func (r *tenantSkillRepository) DeleteUserEnvVar(
	ctx context.Context, tenantID uint64, p types.Principal, configID, skillID, name string,
) error {
	p = p.Normalize()
	res := r.db.WithContext(ctx).
		Where("tenant_id = ? AND principal_type = ? AND principal_id = ?"+
			" AND sandbox_config_id = ? AND skill_id = ? AND name = ?",
			tenantID, p.Type, p.ID, configID, skillID, name).
		Delete(&types.TenantUserEnvVar{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return types.ErrEnvVarNotFound
	}
	return nil
}

func (r *tenantSkillRepository) DeleteUserEnvVarsByConfig(
	ctx context.Context, tenantID uint64, configID string,
) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND sandbox_config_id = ?", tenantID, configID).
		Delete(&types.TenantUserEnvVar{}).Error
}
