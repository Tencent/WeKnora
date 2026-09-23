package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrKnowledgeFileVersionConflict rejects a source change based on stale state.
var ErrKnowledgeFileVersionConflict = errors.New("knowledge file changed or is being processed; refresh and retry")

func (r *knowledgeRepository) ReplaceKnowledgeSource(
	ctx context.Context, before *types.Knowledge, columns map[string]interface{},
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// updated_at is intentionally not part of the predicate. A metadata-only
		// write bumps it without changing the source this swap is guarding.
		result := tx.Model(&types.Knowledge{}).
			Where("id = ? AND tenant_id = ? AND file_version = ? AND file_path = ? AND parse_status = ?",
				before.ID, before.TenantID, before.CurrentFileVersion(), before.FilePath,
				before.ParseStatus).
			Updates(columns)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrKnowledgeFileVersionConflict
		}
		snapshot := before.FileVersionSnapshot()
		snapshot.ID = uuid.NewString()
		return tx.Create(snapshot).Error
	})
}

func (r *knowledgeRepository) RestoreKnowledgeSource(
	ctx context.Context, before *types.Knowledge, replacementPath string, columns map[string]interface{},
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&types.Knowledge{}).
			Where("id = ? AND tenant_id = ? AND file_version = ? AND file_path = ?",
				before.ID, before.TenantID, before.CurrentFileVersion()+1, replacementPath).Updates(columns)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrKnowledgeFileVersionConflict
		}
		return tx.Where("tenant_id = ? AND knowledge_id = ? AND version = ?",
			before.TenantID, before.ID, before.CurrentFileVersion()).Delete(&types.KnowledgeFileVersion{}).Error
	})
}

func (r *knowledgeRepository) ListKnowledgeFileVersions(
	ctx context.Context, tenantID uint64, id string, limit, offset int,
) ([]*types.KnowledgeFileVersion, int64, error) {
	rows := make([]*types.KnowledgeFileVersion, 0)
	var total int64
	query := r.db.WithContext(ctx).Model(&types.KnowledgeFileVersion{}).
		Where("tenant_id = ? AND knowledge_id = ?", tenantID, id)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit == 0 {
		return rows, total, nil
	}
	err := query.Order("version DESC").Limit(limit).Offset(offset).Find(&rows).Error
	return rows, total, err
}

func (r *knowledgeRepository) GetKnowledgeFileVersion(
	ctx context.Context, tenantID uint64, id string, version int,
) (*types.KnowledgeFileVersion, error) {
	var row types.KnowledgeFileVersion
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_id = ? AND version = ?", tenantID, id, version).First(&row).Error
	return &row, err
}

func (r *knowledgeRepository) DeleteKnowledgeFileVersions(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).Where("tenant_id = ? AND knowledge_id = ?", tenantID, id).
		Delete(&types.KnowledgeFileVersion{}).Error
}
