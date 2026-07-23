package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// --- Knowledge folder tree (knowledge_folders) ---

// ErrKnowledgeFolderNotFound is returned when a knowledge folder is not found.
var ErrKnowledgeFolderNotFound = errors.New("knowledge folder not found")

// ErrKnowledgeFolderConflict is returned when a sibling folder with the same
// name already exists under the same parent.
var ErrKnowledgeFolderConflict = errors.New("knowledge folder name conflict")

func (r *knowledgeRepository) CreateFolder(ctx context.Context, folder *types.KnowledgeFolder) error {
	return r.db.WithContext(ctx).Create(folder).Error
}

func (r *knowledgeRepository) GetFolderByID(ctx context.Context, kbID string, id string) (*types.KnowledgeFolder, error) {
	var folder types.KnowledgeFolder
	if err := r.db.WithContext(ctx).
		Where("knowledge_base_id = ? AND id = ?", kbID, id).
		First(&folder).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrKnowledgeFolderNotFound
		}
		return nil, err
	}
	return &folder, nil
}

// GetFolderByIDGlobal looks up a folder by its primary key without scoping to a
// specific knowledge base. Folder UUIDs are globally unique, so this is safe and
// is used by the QA pipeline to resolve a @mentioned folder's owning KB/tenant
// before expanding it to the knowledges beneath it.
func (r *knowledgeRepository) GetFolderByIDGlobal(ctx context.Context, id string) (*types.KnowledgeFolder, error) {
	var folder types.KnowledgeFolder
	if err := r.db.WithContext(ctx).
		Where("id = ?", id).
		First(&folder).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrKnowledgeFolderNotFound
		}
		return nil, err
	}
	return &folder, nil
}

func (r *knowledgeRepository) GetChildFolderByName(
	ctx context.Context, kbID string, parentID string, name string,
) (*types.KnowledgeFolder, error) {
	var folder types.KnowledgeFolder
	if err := r.db.WithContext(ctx).
		Where("knowledge_base_id = ? AND parent_id = ? AND name = ?", kbID, parentID, name).
		First(&folder).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrKnowledgeFolderNotFound
		}
		return nil, err
	}
	return &folder, nil
}

func (r *knowledgeRepository) ListChildFolders(
	ctx context.Context, kbID string, parentID string,
) ([]*types.KnowledgeFolder, error) {
	var folders []*types.KnowledgeFolder
	if err := r.db.WithContext(ctx).
		Where("knowledge_base_id = ? AND parent_id = ?", kbID, parentID).
		Order("sort_order ASC").
		Order("name ASC").
		Find(&folders).Error; err != nil {
		return nil, err
	}
	return folders, nil
}

func (r *knowledgeRepository) ListAllFolders(ctx context.Context, kbID string) ([]*types.KnowledgeFolder, error) {
	var folders []*types.KnowledgeFolder
	if err := r.db.WithContext(ctx).
		Where("knowledge_base_id = ?", kbID).
		Order("depth ASC").
		Order("path ASC").
		Find(&folders).Error; err != nil {
		return nil, err
	}
	return folders, nil
}

func (r *knowledgeRepository) UpdateFolder(ctx context.Context, folder *types.KnowledgeFolder) error {
	result := r.db.WithContext(ctx).
		Model(&types.KnowledgeFolder{}).
		Where("id = ?", folder.ID).
		Updates(map[string]interface{}{
			"parent_id":  folder.ParentID,
			"name":       folder.Name,
			"path":       folder.Path,
			"depth":      folder.Depth,
			"sort_order": folder.SortOrder,
			"updated_at": folder.UpdatedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrKnowledgeFolderNotFound
	}
	return nil
}

func (r *knowledgeRepository) DeleteFolder(ctx context.Context, kbID string, id string) error {
	result := r.db.WithContext(ctx).
		Where("knowledge_base_id = ? AND id = ?", kbID, id).
		Delete(&types.KnowledgeFolder{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrKnowledgeFolderNotFound
	}
	return nil
}

func (r *knowledgeRepository) CountKnowledgesInFolder(ctx context.Context, kbID string, folderID string) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Where("knowledge_base_id = ? AND folder_id = ? AND deleted_at IS NULL", kbID, folderID).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (r *knowledgeRepository) CountKnowledgesByFolder(ctx context.Context, kbID string) (map[string]int64, error) {
	type folderCount struct {
		FolderID string
		Cnt      int64
	}
	var rows []folderCount
	if err := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Select("folder_id, COUNT(*) as cnt").
		Where("knowledge_base_id = ? AND deleted_at IS NULL", kbID).
		Group("folder_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[row.FolderID] = row.Cnt
	}
	return out, nil
}

func (r *knowledgeRepository) ListKnowledgesByFolderIDs(
	ctx context.Context, kbID string, folderIDs []string,
) ([]*types.Knowledge, error) {
	if len(folderIDs) == 0 {
		return nil, nil
	}
	var knowledges []*types.Knowledge
	if err := r.db.WithContext(ctx).
		Where("knowledge_base_id = ? AND folder_id IN ? AND deleted_at IS NULL", kbID, folderIDs).
		Find(&knowledges).Error; err != nil {
		return nil, err
	}
	return knowledges, nil
}

// UpdateKnowledgeFolder sets the folder_id of a single knowledge entry. An
// empty folderID means "root level" (folder_id = '').
func (r *knowledgeRepository) UpdateKnowledgeFolder(ctx context.Context, id string, folderID string) error {
	return r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Where("id = ?", id).
		Update("folder_id", folderID).Error
}
