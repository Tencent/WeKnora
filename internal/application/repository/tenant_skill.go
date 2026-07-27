package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrTenantSkillNotFound = errors.New("repository: tenant skill not found")

type tenantSkillRepository struct {
	db *gorm.DB
}

func NewTenantSkillRepository(db *gorm.DB) interfaces.TenantSkillRepository {
	return &tenantSkillRepository{db: db}
}

func (r *tenantSkillRepository) CreateStaging(
	ctx context.Context,
	skill *types.TenantSkill,
	version *types.TenantSkillVersion,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(skill).Error; err != nil {
			return err
		}
		return tx.Create(version).Error
	})
}

func (r *tenantSkillRepository) CreateVersion(ctx context.Context, version *types.TenantSkillVersion) error {
	return r.db.WithContext(ctx).Create(version).Error
}

func (r *tenantSkillRepository) GetByID(
	ctx context.Context,
	tenantID uint64,
	skillID string,
) (*types.TenantSkill, error) {
	var skill types.TenantSkill
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, skillID).
		First(&skill).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTenantSkillNotFound
	}
	if err != nil {
		return nil, err
	}
	return &skill, nil
}

func (r *tenantSkillRepository) GetCurrentVersion(
	ctx context.Context,
	tenantID uint64,
	skillID string,
) (*types.TenantSkillVersion, error) {
	var version types.TenantSkillVersion
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND skill_id = ? AND state = ?", tenantID, skillID, types.SkillVersionCurrent).
		First(&version).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTenantSkillNotFound
	}
	if err != nil {
		return nil, err
	}
	return &version, nil
}

func (r *tenantSkillRepository) List(
	ctx context.Context,
	tenantID uint64,
	includeDisabled bool,
) ([]*types.TenantSkill, error) {
	var skills []*types.TenantSkill
	query := r.db.WithContext(ctx).Where("tenant_id = ?", tenantID)
	if !includeDisabled {
		query = query.Where("status = ?", types.TenantSkillEnabled)
	}
	err := query.Order("name ASC, id ASC").Find(&skills).Error
	return skills, err
}

func (r *tenantSkillRepository) SetVersionReady(
	ctx context.Context,
	tenantID uint64,
	skillID string,
	versionID string,
	storagePath string,
	contentHash string,
	manifestJSON []byte,
) error {
	result := r.db.WithContext(ctx).Model(&types.TenantSkillVersion{}).
		Where("tenant_id = ? AND skill_id = ? AND id = ? AND state = ?",
			tenantID, skillID, versionID, types.SkillVersionStaging).
		Updates(map[string]any{
			"state": types.SkillVersionReady, "storage_path": storagePath,
			"content_hash": contentHash, "manifest_json": manifestJSON,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrTenantSkillNotFound
	}
	return nil
}

func (r *tenantSkillRepository) SwitchCurrentVersion(
	ctx context.Context,
	tenantID uint64,
	skillID string,
	oldVersionID string,
	newVersionID string,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var skill types.TenantSkill
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND id = ?", tenantID, skillID).
			First(&skill).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrTenantSkillNotFound
			}
			return err
		}
		now := time.Now()
		if oldVersionID != "" {
			result := tx.Model(&types.TenantSkillVersion{}).
				Where("tenant_id = ? AND skill_id = ? AND id = ? AND state = ?",
					tenantID, skillID, oldVersionID, types.SkillVersionCurrent).
				Updates(map[string]any{"state": types.SkillVersionGarbage, "garbage_at": now})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrTenantSkillNotFound
			}
		}
		result := tx.Model(&types.TenantSkillVersion{}).
			Where("tenant_id = ? AND skill_id = ? AND id = ? AND state = ?",
				tenantID, skillID, newVersionID, types.SkillVersionReady).
			Update("state", types.SkillVersionCurrent)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrTenantSkillNotFound
		}
		return tx.Model(&types.TenantSkill{}).
			Where("tenant_id = ? AND id = ?", tenantID, skillID).
			Update("current_version_id", newVersionID).Error
	})
}

func (r *tenantSkillRepository) SetStatus(
	ctx context.Context,
	tenantID uint64,
	skillID string,
	status types.TenantSkillStatus,
) error {
	result := r.db.WithContext(ctx).Model(&types.TenantSkill{}).
		Where("tenant_id = ? AND id = ?", tenantID, skillID).
		Update("status", status)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrTenantSkillNotFound
	}
	return nil
}

func (r *tenantSkillRepository) SoftDelete(ctx context.Context, tenantID uint64, skillID string) error {
	result := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, skillID).
		Delete(&types.TenantSkill{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrTenantSkillNotFound
	}
	return nil
}

func (r *tenantSkillRepository) ListReconciliationCandidates(
	ctx context.Context,
	olderThan time.Time,
) ([]*types.TenantSkillVersion, error) {
	var versions []*types.TenantSkillVersion
	err := r.db.WithContext(ctx).
		Where("state IN ? AND created_at < ?", []types.TenantSkillVersionState{
			types.SkillVersionStaging, types.SkillVersionReady, types.SkillVersionGarbage,
		}, olderThan).
		Order("created_at ASC, id ASC").
		Find(&versions).Error
	return versions, err
}

func (r *tenantSkillRepository) CreateExecutionAudit(
	ctx context.Context,
	audit *types.SkillExecutionAudit,
) error {
	return r.db.WithContext(ctx).Create(audit).Error
}

func (r *tenantSkillRepository) FinishExecutionAudit(
	ctx context.Context,
	tenantID uint64,
	auditID string,
	finish types.ExecutionAuditFinish,
) error {
	result := r.db.WithContext(ctx).Model(&types.SkillExecutionAudit{}).
		Where("tenant_id = ? AND id = ?", tenantID, auditID).
		Updates(map[string]any{
			"status": finish.Status, "finished_at": finish.FinishedAt,
			"duration_ms": finish.DurationMS, "exit_code": finish.ExitCode,
			"killed": finish.Killed, "truncated": finish.Truncated,
			"output_summary": finish.OutputSummary,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrTenantSkillNotFound
	}
	return nil
}
