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

var ErrKnowledgeFolderNotFound = errors.New("knowledge folder not found")
var ErrKnowledgeFolderExists = errors.New("knowledge folder already exists")
var ErrKnowledgeFolderNotEmpty = errors.New("knowledge folder is not empty")

type knowledgeFolderRepository struct {
	db *gorm.DB
}

// NewKnowledgeFolderRepository creates a knowledge folder repository.
func NewKnowledgeFolderRepository(db *gorm.DB) interfaces.KnowledgeFolderRepository {
	return &knowledgeFolderRepository{db: db}
}

func (r *knowledgeFolderRepository) ListByParent(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	parentID string,
) ([]*types.KnowledgeFolder, error) {
	var folders []*types.KnowledgeFolder
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND parent_id = ?", tenantID, kbID, parentID).
		Order("sort_order ASC, name ASC, created_at ASC").
		Find(&folders).Error
	if err != nil {
		return nil, err
	}
	if len(folders) == 0 {
		return folders, nil
	}
	if err := r.attachKnowledgeCounts(ctx, tenantID, kbID, folders); err != nil {
		return nil, err
	}
	if err := r.MarkHasChildren(ctx, tenantID, kbID, folders); err != nil {
		return nil, err
	}
	return folders, nil
}

func (r *knowledgeFolderRepository) GetByID(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	folderID string,
) (*types.KnowledgeFolder, error) {
	var folder types.KnowledgeFolder
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?", tenantID, kbID, folderID).
		First(&folder).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrKnowledgeFolderNotFound
	}
	return &folder, err
}

func (r *knowledgeFolderRepository) GetByParentAndName(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	parentID string,
	name string,
) (*types.KnowledgeFolder, error) {
	var folder types.KnowledgeFolder
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND parent_id = ? AND name = ?", tenantID, kbID, parentID, name).
		First(&folder).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrKnowledgeFolderNotFound
	}
	return &folder, err
}

func (r *knowledgeFolderRepository) Create(ctx context.Context, folder *types.KnowledgeFolder) error {
	err := r.db.WithContext(ctx).Create(folder).Error
	if isUniqueConstraintError(err) {
		return ErrKnowledgeFolderExists
	}
	return err
}

func (r *knowledgeFolderRepository) Update(ctx context.Context, folder *types.KnowledgeFolder) error {
	err := r.db.WithContext(ctx).Save(folder).Error
	if isUniqueConstraintError(err) {
		return ErrKnowledgeFolderExists
	}
	return err
}

func (r *knowledgeFolderRepository) UpdateWithDescendantPaths(
	ctx context.Context,
	folder *types.KnowledgeFolder,
	oldPath string,
) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(folder).Error; err != nil {
			return err
		}
		if oldPath == "" || oldPath == folder.Path {
			return nil
		}
		return updateDescendantFolderPaths(ctx, tx, folder, oldPath)
	})
	if isUniqueConstraintError(err) {
		return ErrKnowledgeFolderExists
	}
	return err
}

func (r *knowledgeFolderRepository) Delete(ctx context.Context, tenantID uint64, kbID string, folderID string) error {
	res := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?", tenantID, kbID, folderID).
		Delete(&types.KnowledgeFolder{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrKnowledgeFolderNotFound
	}
	return nil
}

func (r *knowledgeFolderRepository) DeleteEmpty(ctx context.Context, tenantID uint64, kbID string, folderID string) error {
	now := time.Now()
	res := r.db.WithContext(ctx).
		Model(&types.KnowledgeFolder{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?", tenantID, kbID, folderID).
		Where(
			`NOT EXISTS (
				SELECT 1 FROM knowledge_folders child
				WHERE child.tenant_id = ?
					AND child.knowledge_base_id = ?
					AND child.parent_id = knowledge_folders.id
					AND child.deleted_at IS NULL
			)`,
			tenantID,
			kbID,
		).
		Where(
			`NOT EXISTS (
				SELECT 1 FROM knowledges k
				WHERE k.tenant_id = ?
					AND k.knowledge_base_id = ?
					AND k.folder_id = knowledge_folders.id
					AND k.deleted_at IS NULL
			)`,
			tenantID,
			kbID,
		).
		Updates(map[string]interface{}{
			"deleted_at": now,
			"updated_at": now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		return nil
	}
	if _, err := r.GetByID(ctx, tenantID, kbID, folderID); err != nil {
		return err
	}
	return ErrKnowledgeFolderNotEmpty
}

func (r *knowledgeFolderRepository) CountKnowledgeByParents(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	folderIDs []string,
) (map[string]int64, error) {
	counts := make(map[string]int64, len(folderIDs))
	if len(folderIDs) == 0 {
		return counts, nil
	}
	type row struct {
		FolderID string
		Count    int64
	}
	var rows []row
	err := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Select("folder_id, COUNT(*) AS count").
		Where("tenant_id = ? AND knowledge_base_id = ? AND folder_id IN ?", tenantID, kbID, folderIDs).
		Group("folder_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		counts[r.FolderID] = r.Count
	}
	return counts, nil
}

func (r *knowledgeFolderRepository) MarkHasChildren(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	folders []*types.KnowledgeFolder,
) error {
	if len(folders) == 0 {
		return nil
	}
	parentIDs := make([]string, 0, len(folders))
	for _, folder := range folders {
		parentIDs = append(parentIDs, folder.ID)
	}
	type row struct {
		ParentID string
		Count    int64
	}
	var rows []row
	err := r.db.WithContext(ctx).
		Model(&types.KnowledgeFolder{}).
		Select("parent_id, COUNT(*) AS count").
		Where("tenant_id = ? AND knowledge_base_id = ? AND parent_id IN ?", tenantID, kbID, parentIDs).
		Group("parent_id").
		Scan(&rows).Error
	if err != nil {
		return err
	}
	hasChildren := make(map[string]bool, len(rows))
	for _, r := range rows {
		hasChildren[r.ParentID] = r.Count > 0
	}
	for _, folder := range folders {
		folder.HasChildren = hasChildren[folder.ID]
	}
	return nil
}

func (r *knowledgeFolderRepository) attachKnowledgeCounts(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	folders []*types.KnowledgeFolder,
) error {
	folderIDs := make([]string, 0, len(folders))
	for _, folder := range folders {
		folderIDs = append(folderIDs, folder.ID)
	}
	counts, err := r.CountKnowledgeByParents(ctx, tenantID, kbID, folderIDs)
	if err != nil {
		return err
	}
	for _, folder := range folders {
		folder.KnowledgeCount = counts[folder.ID]
	}
	return nil
}

func updateDescendantFolderPaths(
	ctx context.Context,
	tx *gorm.DB,
	folder *types.KnowledgeFolder,
	oldPath string,
) error {
	oldPrefix := oldPath + "/"
	newPrefix := folder.Path + "/"

	var descendants []*types.KnowledgeFolder
	if err := tx.WithContext(ctx).
		Where(
			`tenant_id = ? AND knowledge_base_id = ? AND path LIKE ? ESCAPE '\'`,
			folder.TenantID,
			folder.KnowledgeBaseID,
			escapeKnowledgeFolderPathLikePattern(oldPrefix)+"%",
		).
		Find(&descendants).Error; err != nil {
		return err
	}
	for _, descendant := range descendants {
		descendant.Path = newPrefix + strings.TrimPrefix(descendant.Path, oldPrefix)
		if err := tx.Save(descendant).Error; err != nil {
			return err
		}
	}
	return nil
}

func escapeKnowledgeFolderPathLikePattern(pattern string) string {
	pattern = strings.ReplaceAll(pattern, `\`, `\\`)
	pattern = strings.ReplaceAll(pattern, `%`, `\%`)
	pattern = strings.ReplaceAll(pattern, `_`, `\_`)
	return pattern
}

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "Duplicate entry")
}
