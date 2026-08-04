package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrKnowledgeFolderNotFound = errors.New("knowledge folder not found")
	ErrKnowledgeFolderNotEmpty = errors.New("knowledge folder is not empty")
)

type knowledgeFolderRepository struct {
	db       *gorm.DB
	lockRows bool
}

func NewKnowledgeFolderRepository(db *gorm.DB) interfaces.KnowledgeFolderRepository {
	return &knowledgeFolderRepository{db: db}
}

func (r *knowledgeFolderRepository) WithinTransaction(
	ctx context.Context,
	fn func(interfaces.KnowledgeFolderRepository) error,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&knowledgeFolderRepository{db: tx, lockRows: true})
	})
}

func (r *knowledgeFolderRepository) Create(ctx context.Context, folder *types.KnowledgeFolder) error {
	return r.db.WithContext(ctx).Create(folder).Error
}

func (r *knowledgeFolderRepository) Get(
	ctx context.Context,
	tenantID uint64,
	kbID, folderID string,
) (*types.KnowledgeFolder, error) {
	var folder types.KnowledgeFolder
	query := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?", tenantID, kbID, folderID)
	if r.lockRows && r.db.Dialector.Name() == "postgres" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := query.First(&folder).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrKnowledgeFolderNotFound
		}
		return nil, err
	}
	return &folder, nil
}

func (r *knowledgeFolderRepository) List(
	ctx context.Context,
	tenantID uint64,
	kbID string,
) ([]*types.KnowledgeFolder, error) {
	var folders []*types.KnowledgeFolder
	query := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID)
	if r.lockRows && r.db.Dialector.Name() == "postgres" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := query.
		Order("depth ASC").
		Order("name ASC").
		Order("id ASC").
		Find(&folders).Error
	return folders, err
}

func (r *knowledgeFolderRepository) Save(ctx context.Context, folder *types.KnowledgeFolder) error {
	result := r.db.WithContext(ctx).
		Model(&types.KnowledgeFolder{}).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND id = ?",
			folder.TenantID,
			folder.KnowledgeBaseID,
			folder.ID,
		).
		Updates(map[string]any{
			"parent_id":  folder.ParentID,
			"name":       folder.Name,
			"path":       folder.Path,
			"depth":      folder.Depth,
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

func (r *knowledgeFolderRepository) DeleteEmpty(
	ctx context.Context,
	tenantID uint64,
	kbID, folderID string,
) error {
	now := time.Now()
	result := r.db.WithContext(ctx).Exec(`
UPDATE knowledge_folders
SET deleted_at = ?, updated_at = ?
WHERE tenant_id = ? AND knowledge_base_id = ? AND id = ? AND deleted_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM knowledge_folders child
    WHERE child.tenant_id = ? AND child.knowledge_base_id = ?
      AND child.parent_id = ? AND child.deleted_at IS NULL
  )
  AND NOT EXISTS (
    SELECT 1 FROM knowledges
    WHERE tenant_id = ? AND knowledge_base_id = ?
      AND folder_id = ? AND deleted_at IS NULL
  )`,
		now, now,
		tenantID, kbID, folderID,
		tenantID, kbID, folderID,
		tenantID, kbID, folderID,
	)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}
	if _, err := r.Get(ctx, tenantID, kbID, folderID); err != nil {
		return err
	}
	return ErrKnowledgeFolderNotEmpty
}

func (r *knowledgeFolderRepository) MoveKnowledge(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	knowledgeIDs []string,
	folderID *string,
) (int64, error) {
	if len(knowledgeIDs) == 0 {
		return 0, nil
	}
	result := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND id IN ?",
			tenantID,
			kbID,
			knowledgeIDs,
		).
		Updates(map[string]any{
			"folder_id":  folderID,
			"updated_at": time.Now(),
		})
	return result.RowsAffected, result.Error
}

func (r *knowledgeFolderRepository) ListKnowledgeIDs(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	folderIDs []string,
	includeDescendants bool,
) ([]string, error) {
	ids := make([]string, 0)
	if len(folderIDs) == 0 {
		return ids, nil
	}

	query := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Where("knowledges.tenant_id = ? AND knowledges.knowledge_base_id = ?", tenantID, kbID)
	if includeDescendants {
		var selectedFolders []types.KnowledgeFolder
		if err := r.db.WithContext(ctx).
			Where("tenant_id = ? AND knowledge_base_id = ? AND id IN ?", tenantID, kbID, folderIDs).
			Find(&selectedFolders).Error; err != nil {
			return nil, err
		}
		if len(selectedFolders) != len(folderIDs) {
			return nil, ErrKnowledgeFolderNotFound
		}

		folders := r.db.WithContext(ctx).
			Model(&types.KnowledgeFolder{}).
			Select("id").
			Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID)
		var subtree *gorm.DB
		for _, folder := range selectedFolders {
			pathPrefix := escapeLikePath(folder.Path) + "/%"
			branch := folders.Where("path = ? OR path LIKE ? ESCAPE '\\'", folder.Path, pathPrefix)
			if subtree == nil {
				subtree = branch
			} else {
				subtree = subtree.Or("path = ? OR path LIKE ? ESCAPE '\\'", folder.Path, pathPrefix)
			}
		}
		query = query.Where("folder_id IN (?)", subtree)
	} else {
		query = query.Where("folder_id IN ?", folderIDs)
	}
	err := query.Distinct("knowledges.id").Order("knowledges.id ASC").Pluck("knowledges.id", &ids).Error
	return ids, err
}

func escapeLikePath(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "%", `\%`)
	return strings.ReplaceAll(value, "_", `\_`)
}
