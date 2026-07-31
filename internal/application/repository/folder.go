package repository

import (
	"context"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

type knowledgeFolderRepository struct{ db *gorm.DB }

func NewKnowledgeFolderRepository(db *gorm.DB) interfaces.KnowledgeFolderRepository {
	return &knowledgeFolderRepository{db: db}
}

func (r *knowledgeFolderRepository) Create(ctx context.Context, folder *types.KnowledgeFolder) error {
	return r.db.WithContext(ctx).Create(folder).Error
}

func (r *knowledgeFolderRepository) Update(ctx context.Context, folder *types.KnowledgeFolder) error {
	return r.db.WithContext(ctx).Save(folder).Error
}

func (r *knowledgeFolderRepository) GetByID(ctx context.Context, tenantID uint64, id string) (*types.KnowledgeFolder, error) {
	var folder types.KnowledgeFolder
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ? AND deleted_at IS NULL", tenantID, id).First(&folder).Error
	if err != nil {
		return nil, err
	}
	return &folder, nil
}

func (r *knowledgeFolderRepository) GetByName(ctx context.Context, tenantID uint64, kbID string, parentID string, name string) (*types.KnowledgeFolder, error) {
	var folder types.KnowledgeFolder
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND knowledge_base_id = ? AND parent_id = ? AND name = ? AND deleted_at IS NULL", tenantID, kbID, parentID, name).First(&folder).Error
	if err != nil {
		return nil, err
	}
	return &folder, nil
}

func (r *knowledgeFolderRepository) ListByParent(ctx context.Context, tenantID uint64, kbID string, parentID string) ([]*types.KnowledgeFolder, error) {
	var folders []*types.KnowledgeFolder
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND knowledge_base_id = ? AND parent_id = ? AND deleted_at IS NULL", tenantID, kbID, parentID).Order("sort_order ASC, name ASC").Find(&folders).Error
	if err != nil {
		return nil, err
	}
	return folders, nil
}

func (r *knowledgeFolderRepository) ListByKB(ctx context.Context, tenantID uint64, kbID string) ([]*types.KnowledgeFolder, error) {
	var folders []*types.KnowledgeFolder
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND knowledge_base_id = ? AND deleted_at IS NULL", tenantID, kbID).Order("depth ASC, sort_order ASC, name ASC").Find(&folders).Error
	if err != nil {
		return nil, err
	}
	return folders, nil
}

func (r *knowledgeFolderRepository) Delete(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&types.KnowledgeFolder{}).Error
}

func (r *knowledgeFolderRepository) GetDescendantIDs(ctx context.Context, tenantID uint64, kbID string, folderID string) ([]string, error) {
	if folderID == "" {
		return nil, nil
	}
	var ids []string
	err := r.db.WithContext(ctx).Model(&types.KnowledgeFolder{}).Select("id").Where("tenant_id = ? AND knowledge_base_id = ? AND deleted_at IS NULL AND (id = ? OR path LIKE ?)", tenantID, kbID, folderID, folderID+"/%").Pluck("id", &ids).Error
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if id == folderID {
			return ids, nil
		}
	}
	return append([]string{folderID}, ids...), nil
}

func (r *knowledgeFolderRepository) CountKnowledgeInFolder(ctx context.Context, tenantID uint64, kbID string, folderID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&types.Knowledge{}).Where("tenant_id = ? AND knowledge_base_id = ? AND folder_id = ? AND deleted_at IS NULL", tenantID, kbID, folderID).Count(&count).Error
	return count, err
}

func (r *knowledgeFolderRepository) CountChildFolders(ctx context.Context, tenantID uint64, kbID string, parentID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&types.KnowledgeFolder{}).Where("tenant_id = ? AND knowledge_base_id = ? AND parent_id = ? AND deleted_at IS NULL", tenantID, kbID, parentID).Count(&count).Error
	return count, err
}
