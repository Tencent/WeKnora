package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

var (
	ErrKnowledgeFolderNotFound = errors.New("knowledge folder not found")
	ErrKnowledgeFolderConflict = errors.New("knowledge folder name conflict")
	ErrKnowledgeFolderNotEmpty = errors.New("knowledge folder is not empty")
	ErrKnowledgeFolderMove     = errors.New("one or more knowledge items cannot be moved")
)

type knowledgeFolderRepository struct {
	db *gorm.DB
}

func NewKnowledgeFolderRepository(db *gorm.DB) interfaces.KnowledgeFolderRepository {
	return &knowledgeFolderRepository{db: db}
}

func (r *knowledgeFolderRepository) Create(ctx context.Context, folder *types.KnowledgeFolder) error {
	if err := r.db.WithContext(ctx).Create(folder).Error; err != nil {
		if isFolderUniqueViolation(err) {
			return ErrKnowledgeFolderConflict
		}
		return err
	}
	return nil
}

func (r *knowledgeFolderRepository) GetByID(
	ctx context.Context, tenantID uint64, kbID, folderID string,
) (*types.KnowledgeFolder, error) {
	var folder types.KnowledgeFolder
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?", tenantID, kbID, folderID).
		First(&folder).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrKnowledgeFolderNotFound
	}
	if err != nil {
		return nil, err
	}
	return &folder, nil
}

func (r *knowledgeFolderRepository) ListByKnowledgeBase(
	ctx context.Context, tenantID uint64, kbID string,
) ([]*types.KnowledgeFolder, error) {
	var folders []*types.KnowledgeFolder
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID).
		Order("depth ASC").Order("sort_order ASC").Order("name ASC").
		Find(&folders).Error
	return folders, err
}

func (r *knowledgeFolderRepository) GetChildByName(
	ctx context.Context, tenantID uint64, kbID string, parentID *string, name string,
) (*types.KnowledgeFolder, error) {
	var folder types.KnowledgeFolder
	q := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND LOWER(name) = LOWER(?)", tenantID, kbID, name)
	if parentID == nil {
		q = q.Where("parent_folder_id IS NULL")
	} else {
		q = q.Where("parent_folder_id = ?", *parentID)
	}
	if err := q.First(&folder).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrKnowledgeFolderNotFound
		}
		return nil, err
	}
	return &folder, nil
}

func (r *knowledgeFolderRepository) UpdateTree(ctx context.Context, folders []*types.KnowledgeFolder) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, folder := range folders {
			result := tx.Model(&types.KnowledgeFolder{}).
				Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?", folder.TenantID, folder.KnowledgeBaseID, folder.ID).
				Updates(map[string]any{
					"parent_folder_id": folder.ParentFolderID,
					"name":             folder.Name,
					"description":      folder.Description,
					"depth":            folder.Depth,
					"sort_order":       folder.SortOrder,
					"updated_at":       folder.UpdatedAt,
				})
			if result.Error != nil {
				if isFolderUniqueViolation(result.Error) {
					return ErrKnowledgeFolderConflict
				}
				return result.Error
			}
			if result.RowsAffected == 0 {
				return ErrKnowledgeFolderNotFound
			}
		}
		return nil
	})
}

func (r *knowledgeFolderRepository) DeleteEmpty(
	ctx context.Context, tenantID uint64, kbID, folderID string,
) error {
	result := r.db.WithContext(ctx).Exec(`UPDATE knowledge_folders
SET deleted_at = ?, updated_at = ?
WHERE tenant_id = ? AND knowledge_base_id = ? AND id = ? AND deleted_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM knowledges
    WHERE tenant_id = ? AND knowledge_base_id = ? AND folder_id = ? AND deleted_at IS NULL
  )
  AND NOT EXISTS (
    SELECT 1 FROM knowledge_folders child
    WHERE child.tenant_id = ? AND child.knowledge_base_id = ?
      AND child.parent_folder_id = ? AND child.deleted_at IS NULL
  )`, time.Now(), time.Now(), tenantID, kbID, folderID,
		tenantID, kbID, folderID, tenantID, kbID, folderID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}
	var count int64
	if err := r.db.WithContext(ctx).Model(&types.KnowledgeFolder{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?", tenantID, kbID, folderID).
		Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return ErrKnowledgeFolderNotFound
	}
	return ErrKnowledgeFolderNotEmpty
}

func (r *knowledgeFolderRepository) DeleteTree(
	ctx context.Context, tenantID uint64, kbID, folderID string,
) error {
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		moveResult := tx.Exec(`WITH RECURSIVE folder_tree(id) AS (
    SELECT id FROM knowledge_folders
    WHERE tenant_id = ? AND knowledge_base_id = ? AND id = ? AND deleted_at IS NULL
    UNION ALL
    SELECT child.id FROM knowledge_folders child
    JOIN folder_tree parent ON child.parent_folder_id = parent.id
    WHERE child.tenant_id = ? AND child.knowledge_base_id = ? AND child.deleted_at IS NULL
)
UPDATE knowledges
SET folder_id = NULL, updated_at = ?
WHERE tenant_id = ? AND knowledge_base_id = ?
  AND folder_id IN (SELECT id FROM folder_tree)
  AND deleted_at IS NULL`, tenantID, kbID, folderID, tenantID, kbID, now, tenantID, kbID)
		if moveResult.Error != nil {
			return moveResult.Error
		}

		deleteResult := tx.Exec(`WITH RECURSIVE folder_tree(id) AS (
    SELECT id FROM knowledge_folders
    WHERE tenant_id = ? AND knowledge_base_id = ? AND id = ? AND deleted_at IS NULL
    UNION ALL
    SELECT child.id FROM knowledge_folders child
    JOIN folder_tree parent ON child.parent_folder_id = parent.id
    WHERE child.tenant_id = ? AND child.knowledge_base_id = ? AND child.deleted_at IS NULL
)
UPDATE knowledge_folders
SET deleted_at = ?, updated_at = ?
WHERE tenant_id = ? AND knowledge_base_id = ?
  AND id IN (SELECT id FROM folder_tree)
  AND deleted_at IS NULL`, tenantID, kbID, folderID, tenantID, kbID, now, now, tenantID, kbID)
		if deleteResult.Error != nil {
			return deleteResult.Error
		}
		if deleteResult.RowsAffected == 0 {
			return ErrKnowledgeFolderNotFound
		}
		return nil
	})
}

func (r *knowledgeFolderRepository) ListKnowledgeIDsByScope(
	ctx context.Context, tenantID uint64, kbID, folderID string, includeDescendants bool,
) ([]string, error) {
	if !includeDescendants {
		var ids []string
		err := r.db.WithContext(ctx).Model(&types.Knowledge{}).
			Where("tenant_id = ? AND knowledge_base_id = ? AND folder_id = ?", tenantID, kbID, folderID).
			Pluck("id", &ids).Error
		return ids, err
	}

	var ids []string
	err := r.db.WithContext(ctx).Raw(`WITH RECURSIVE folder_tree(id) AS (
    SELECT id FROM knowledge_folders
    WHERE tenant_id = ? AND knowledge_base_id = ? AND id = ? AND deleted_at IS NULL
    UNION ALL
    SELECT child.id FROM knowledge_folders child
    JOIN folder_tree parent ON child.parent_folder_id = parent.id
    WHERE child.tenant_id = ? AND child.knowledge_base_id = ? AND child.deleted_at IS NULL
)
SELECT knowledge.id
FROM knowledges knowledge
WHERE knowledge.tenant_id = ? AND knowledge.knowledge_base_id = ?
  AND knowledge.folder_id IN (SELECT id FROM folder_tree)
  AND knowledge.deleted_at IS NULL`, tenantID, kbID, folderID, tenantID, kbID, tenantID, kbID).
		Scan(&ids).Error
	return ids, err
}

func (r *knowledgeFolderRepository) MoveKnowledge(
	ctx context.Context, tenantID uint64, kbID string, knowledgeIDs []string, folderID *string,
) error {
	if len(knowledgeIDs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&types.Knowledge{}).
			Where("tenant_id = ? AND knowledge_base_id = ? AND id IN ?", tenantID, kbID, knowledgeIDs).
			Count(&count).Error; err != nil {
			return err
		}
		if count != int64(len(knowledgeIDs)) {
			return ErrKnowledgeFolderMove
		}
		result := tx.Model(&types.Knowledge{}).
			Where("tenant_id = ? AND knowledge_base_id = ? AND id IN ?", tenantID, kbID, knowledgeIDs).
			Update("folder_id", folderID)
		if result.Error != nil {
			return result.Error
		}
		return nil
	})
}

func (r *knowledgeFolderRepository) CountKnowledgeByFolder(
	ctx context.Context, tenantID uint64, kbID string,
) (map[string]int64, error) {
	type row struct {
		FolderID string
		Count    int64
	}
	var rows []row
	if err := r.db.WithContext(ctx).Model(&types.Knowledge{}).
		Select("COALESCE(folder_id, '') AS folder_id, COUNT(*) AS count").
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID).
		Group("folder_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	counts := make(map[string]int64, len(rows))
	for _, item := range rows {
		counts[item.FolderID] = item.Count
	}
	return counts, nil
}

func isFolderUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate")
}
