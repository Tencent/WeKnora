package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
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
	// UpdateSkill writes the mutable projection fields after install/remove state changes.
	UpdateSkill(ctx context.Context, e *types.TenantSkillEntity) error
	// DeleteSkill soft-deletes metadata only; image cleanup is represented by snapshots.
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

// UpdateSkill writes the mutable columns explicitly so a zero-valued field on
// the passed entity cannot silently wipe state written by a concurrent job.
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

func (r *tenantSkillRepository) DeleteSkill(
	ctx context.Context, tenantID uint64, configID, skillID string,
) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND sandbox_config_id = ? AND id = ?", tenantID, configID, skillID).
		Delete(&types.TenantSkillEntity{}).Error
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
