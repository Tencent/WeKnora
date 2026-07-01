package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// Sentinel errors for folder operations.
var (
	ErrFolderNotFound     = errors.New("folder not found")
	ErrFolderNameExists   = errors.New("folder name already exists")
	ErrFolderNotEmpty     = errors.New("folder is not empty")
	ErrMaxDepthExceeded   = errors.New("maximum folder depth exceeded")
	ErrCircularReference  = errors.New("cannot move folder to its own descendant")
)

// knowledgeFolderRepository implements interfaces.KnowledgeFolderRepository.
type knowledgeFolderRepository struct {
	db *gorm.DB
}

// NewKnowledgeFolderRepository creates a new knowledge folder repository.
func NewKnowledgeFolderRepository(db *gorm.DB) interfaces.KnowledgeFolderRepository {
	return &knowledgeFolderRepository{db: db}
}

// Create inserts a new folder record.
func (r *knowledgeFolderRepository) Create(ctx context.Context, folder *types.KnowledgeFolder) error {
	return r.db.WithContext(ctx).Create(folder).Error
}

// GetByID retrieves a folder by ID, scoped to tenant.
func (r *knowledgeFolderRepository) GetByID(ctx context.Context, tenantID uint64, id string) (*types.KnowledgeFolder, error) {
	var folder types.KnowledgeFolder
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		First(&folder).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrFolderNotFound
		}
		return nil, err
	}
	return &folder, nil
}

// ListByParent lists all folders directly under the given parent within a knowledge base.
func (r *knowledgeFolderRepository) ListByParent(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	parentID *string,
) ([]*types.KnowledgeFolder, error) {
	var folders []*types.KnowledgeFolder
	query := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID)
	if parentID == nil {
		query = query.Where("parent_folder_id IS NULL")
	} else {
		query = query.Where("parent_folder_id = ?", *parentID)
	}
	if err := query.Order("sort_order ASC, name ASC").Find(&folders).Error; err != nil {
		return nil, err
	}
	return folders, nil
}

// GetAllInKB returns all non-deleted folders in a knowledge base, ordered by path.
func (r *knowledgeFolderRepository) GetAllInKB(
	ctx context.Context,
	tenantID uint64,
	kbID string,
) ([]*types.KnowledgeFolder, error) {
	var folders []*types.KnowledgeFolder
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID).
		Order("path ASC").
		Find(&folders).Error; err != nil {
		return nil, err
	}
	return folders, nil
}

// Update updates folder properties.
func (r *knowledgeFolderRepository) Update(ctx context.Context, folder *types.KnowledgeFolder) error {
	return r.db.WithContext(ctx).Save(folder).Error
}

// Delete soft-deletes a folder by ID, scoped to tenant.
func (r *knowledgeFolderRepository) Delete(ctx context.Context, tenantID uint64, id string) error {
	result := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Delete(&types.KnowledgeFolder{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrFolderNotFound
	}
	return nil
}

// Move updates the parent_folder_id, path, and depth of a folder.
func (r *knowledgeFolderRepository) Move(
	ctx context.Context,
	id string,
	newParentID *string,
	newPath string,
	newDepth int,
) error {
	return r.db.WithContext(ctx).Model(&types.KnowledgeFolder{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"parent_folder_id": newParentID,
			"path":             newPath,
			"depth":            newDepth,
		}).Error
}

// GetByPath retrieves a folder by its exact path within a knowledge base.
func (r *knowledgeFolderRepository) GetByPath(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	path string,
) (*types.KnowledgeFolder, error) {
	var folder types.KnowledgeFolder
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND path = ?", tenantID, kbID, path).
		First(&folder).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &folder, nil
}

// GetDescendants returns all descendant folders of the given folder.
func (r *knowledgeFolderRepository) GetDescendants(
	ctx context.Context,
	folderID string,
) ([]*types.KnowledgeFolder, error) {
	// First get the folder's path
	var folder types.KnowledgeFolder
	if err := r.db.WithContext(ctx).
		Select("path").
		Where("id = ?", folderID).
		First(&folder).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrFolderNotFound
		}
		return nil, err
	}

	var descendants []*types.KnowledgeFolder
	// Use LIKE on the path to find all descendants
	if err := r.db.WithContext(ctx).
		Where("path LIKE ?", folder.Path+"%").
		Where("id != ?", folderID).
		Find(&descendants).Error; err != nil {
		return nil, err
	}
	return descendants, nil
}

// CountKnowledge counts knowledge entries directly in a folder.
func (r *knowledgeFolderRepository) CountKnowledge(
	ctx context.Context,
	tenantID uint64,
	folderID string,
) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&types.Knowledge{}).
		Where("tenant_id = ? AND folder_id = ?", tenantID, folderID).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// CountKnowledgeByKB returns a map from folder_id to knowledge count for all folders in a KB.
// Uses a single GROUP BY query for efficiency.
func (r *knowledgeFolderRepository) CountKnowledgeByKB(
	ctx context.Context,
	tenantID uint64,
	kbID string,
) (map[string]int64, error) {
	type row struct {
		FolderID *string
		Count    int64
	}
	var rows []row
	if err := r.db.WithContext(ctx).Model(&types.Knowledge{}).
		Select("folder_id, count(*) as count").
		Where("tenant_id = ? AND knowledge_base_id = ? AND folder_id IS NOT NULL", tenantID, kbID).
		Group("folder_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string]int64, len(rows))
	for _, r := range rows {
		if r.FolderID != nil {
			result[*r.FolderID] = r.Count
		}
	}
	return result, nil
}

// CountKnowledgeRecursive counts knowledge entries in a folder and all its descendants.
func (r *knowledgeFolderRepository) CountKnowledgeRecursive(
	ctx context.Context,
	tenantID uint64,
	folderID string,
) (int64, error) {
	// Get the folder to obtain its path
	var folder types.KnowledgeFolder
	if err := r.db.WithContext(ctx).
		Select("path").
		Where("id = ?", folderID).
		First(&folder).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, ErrFolderNotFound
		}
		return 0, err
	}

	// Collect all descendant folder IDs
	var folderIDs []string
	if err := r.db.WithContext(ctx).Model(&types.KnowledgeFolder{}).
		Where("path LIKE ?", folder.Path+"%").
		Pluck("id", &folderIDs).Error; err != nil {
		return 0, err
	}

	// Count knowledge entries in any of these folders
	var count int64
	if err := r.db.WithContext(ctx).Model(&types.Knowledge{}).
		Where("tenant_id = ? AND folder_id IN ?", tenantID, folderIDs).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// CheckNameExists checks if a folder with the given name already exists under a parent in the same KB.
func (r *knowledgeFolderRepository) CheckNameExists(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	parentID *string,
	name string,
	excludeID string,
) (bool, error) {
	var count int64
	query := r.db.WithContext(ctx).Model(&types.KnowledgeFolder{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND name = ?", tenantID, kbID, name)

	if parentID == nil {
		query = query.Where("parent_folder_id IS NULL")
	} else {
		query = query.Where("parent_folder_id = ?", *parentID)
	}

	if excludeID != "" {
		query = query.Where("id != ?", excludeID)
	}

	if err := query.Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// BatchUpdateDescendantPaths updates path and depth for all descendants of a folder.
func (r *knowledgeFolderRepository) BatchUpdateDescendantPaths(
	ctx context.Context,
	oldPath string,
	newPath string,
	depthDelta int,
) error {
	// Use REPLACE to update the path prefix and adjust depth.
	// PostgreSQL: REPLACE(path, oldPath, newPath)
	// SQLite: REPLACE(path, oldPath, newPath) — both support REPLACE.
	query := r.db.WithContext(ctx).Model(&types.KnowledgeFolder{}).
		Where("path LIKE ?", oldPath+"%").
		Where("path != ?", oldPath). // Don't update the folder itself (handled separately)
		Updates(map[string]interface{}{
			"path":  gorm.Expr("REPLACE(path, ?, ?)", oldPath, newPath),
			"depth": gorm.Expr(fmt.Sprintf("depth + %d", depthDelta)),
		})
	return query.Error
}
