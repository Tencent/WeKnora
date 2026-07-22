package repository

import (
	"context"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrKnowledgeFolderNotFound = errors.New("knowledge folder not found")

type knowledgeFolderRepository struct {
	db *gorm.DB
}

func NewKnowledgeFolderRepository(db *gorm.DB) interfaces.KnowledgeFolderRepository {
	return &knowledgeFolderRepository{db: db}
}

func (r *knowledgeFolderRepository) Transaction(
	ctx context.Context, fn func(interfaces.KnowledgeFolderRepository) error,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&knowledgeFolderRepository{db: tx})
	})
}

func (r *knowledgeFolderRepository) LockKnowledgeBase(ctx context.Context, tenantID uint64, kbID string) error {
	db := r.db.WithContext(ctx)
	if db.Dialector.Name() == "sqlite" {
		result := db.Model(&types.KnowledgeBase{}).
			Where("tenant_id = ? AND id = ?", tenantID, kbID).
			UpdateColumn("id", gorm.Expr("id"))
		return knowledgeBaseLockResult(result)
	}
	var kb types.KnowledgeBase
	result := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("id").Where("tenant_id = ? AND id = ?", tenantID, kbID).Take(&kb)
	return knowledgeBaseLockResult(result)
}

func knowledgeBaseLockResult(result *gorm.DB) error {
	if errors.Is(result.Error, gorm.ErrRecordNotFound) || result.RowsAffected == 0 {
		return ErrKnowledgeFolderNotFound
	}
	return result.Error
}

func (r *knowledgeFolderRepository) CreateKnowledge(ctx context.Context, knowledge *types.Knowledge) error {
	return r.db.WithContext(ctx).Create(knowledge).Error
}

func (r *knowledgeFolderRepository) GetKnowledgeByIDForUpdate(
	ctx context.Context, tenantID uint64, id string,
) (*types.Knowledge, error) {
	var knowledge types.Knowledge
	err := r.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND id = ?", tenantID, id).First(&knowledge).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrKnowledgeNotFound
	}
	return &knowledge, err
}

func (r *knowledgeFolderRepository) GetKnowledgeBatchForUpdate(
	ctx context.Context, tenantID uint64, ids []string,
) ([]*types.Knowledge, error) {
	var knowledges []*types.Knowledge
	err := r.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND id IN ?", tenantID, ids).Find(&knowledges).Error
	return knowledges, err
}

func (r *knowledgeFolderRepository) MoveKnowledgeToFolder(
	ctx context.Context, tenantID uint64, kbID string, knowledgeIDs []string, folderID string,
) error {
	if len(knowledgeIDs) == 0 {
		return nil
	}
	result := r.db.WithContext(ctx).Model(&types.Knowledge{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND id IN ?", tenantID, kbID, knowledgeIDs).
		Update("folder_id", folderID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != int64(len(knowledgeIDs)) {
		return ErrKnowledgeNotFound
	}
	return nil
}

func (r *knowledgeFolderRepository) CreateIfAbsent(
	ctx context.Context, folder *types.KnowledgeFolder,
) (*types.KnowledgeFolder, bool, error) {
	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(folder)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected > 0 {
		return folder, true, nil
	}
	var existing types.KnowledgeFolder
	err := r.db.WithContext(ctx).Where(
		"tenant_id = ? AND knowledge_base_id = ? AND parent_id = ? AND name = ?",
		folder.TenantID, folder.KnowledgeBaseID, folder.ParentID, folder.Name,
	).First(&existing).Error
	return &existing, false, err
}

func (r *knowledgeFolderRepository) Create(ctx context.Context, folder *types.KnowledgeFolder) error {
	return r.db.WithContext(ctx).Create(folder).Error
}

func (r *knowledgeFolderRepository) GetByID(
	ctx context.Context, tenantID uint64, kbID, id string,
) (*types.KnowledgeFolder, error) {
	var folder types.KnowledgeFolder
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?", tenantID, kbID, id).
		First(&folder).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrKnowledgeFolderNotFound
	}
	if err != nil {
		return nil, err
	}
	return &folder, nil
}

func (r *knowledgeFolderRepository) GetByIDForUpdate(
	ctx context.Context, tenantID uint64, kbID, id string,
) (*types.KnowledgeFolder, error) {
	var folder types.KnowledgeFolder
	err := r.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?", tenantID, kbID, id).
		First(&folder).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrKnowledgeFolderNotFound
	}
	return &folder, err
}

func (r *knowledgeFolderRepository) ListByParent(
	ctx context.Context, tenantID uint64, kbID, parentID string,
) ([]*types.KnowledgeFolder, error) {
	var folders []*types.KnowledgeFolder
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND parent_id = ?", tenantID, kbID, parentID).
		Order("sort_order ASC").Order("name ASC").Find(&folders).Error
	return folders, err
}

func (r *knowledgeFolderRepository) ListAll(
	ctx context.Context, tenantID uint64, kbID string,
) ([]*types.KnowledgeFolder, error) {
	var folders []*types.KnowledgeFolder
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID).
		Order("depth ASC").Order("path ASC").Find(&folders).Error
	return folders, err
}

func (r *knowledgeFolderRepository) ListAllForUpdate(
	ctx context.Context, tenantID uint64, kbID string,
) ([]*types.KnowledgeFolder, error) {
	var folders []*types.KnowledgeFolder
	err := r.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID).
		Order("depth ASC").Order("path ASC").Find(&folders).Error
	return folders, err
}

func (r *knowledgeFolderRepository) Update(ctx context.Context, folder *types.KnowledgeFolder) error {
	result := r.db.WithContext(ctx).Model(&types.KnowledgeFolder{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?", folder.TenantID, folder.KnowledgeBaseID, folder.ID).
		Updates(map[string]interface{}{
			"parent_id": folder.ParentID, "name": folder.Name, "path": folder.Path,
			"depth": folder.Depth, "sort_order": folder.SortOrder,
		})
	return folderWriteResult(result)
}

func (r *knowledgeFolderRepository) UpdateName(
	ctx context.Context, tenantID uint64, kbID, id, name string,
) error {
	result := r.db.WithContext(ctx).Model(&types.KnowledgeFolder{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?", tenantID, kbID, id).
		Update("name", name)
	if result.Error != nil && isFolderSiblingNameConflict(result.Error) {
		return types.ErrFolderAlreadyExists
	}
	return result.Error
}

func isFolderSiblingNameConflict(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") ||
		strings.Contains(message, "duplicate entry") ||
		strings.Contains(message, "duplicate key")
}

func (r *knowledgeFolderRepository) Delete(ctx context.Context, tenantID uint64, kbID, id string) error {
	result := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?", tenantID, kbID, id).
		Delete(&types.KnowledgeFolder{})
	return folderWriteResult(result)
}

func (r *knowledgeFolderRepository) MoveKnowledgeToRoot(
	ctx context.Context, tenantID uint64, kbID string, folderIDs []string,
) error {
	if len(folderIDs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&types.Knowledge{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND folder_id IN ?", tenantID, kbID, folderIDs).
		Update("folder_id", types.FolderRootID).Error
}

func (r *knowledgeFolderRepository) DeleteSubtree(
	ctx context.Context, tenantID uint64, kbID string, folderIDs []string,
) error {
	if len(folderIDs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Unscoped().
		Where("tenant_id = ? AND knowledge_base_id = ? AND id IN ?", tenantID, kbID, folderIDs).
		Delete(&types.KnowledgeFolder{}).Error
}

func folderWriteResult(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrKnowledgeFolderNotFound
	}
	return nil
}

func escapeFolderLike(value string) string {
	value = strings.ReplaceAll(value, "!", "!!")
	value = strings.ReplaceAll(value, "%", "!%")
	return strings.ReplaceAll(value, "_", "!_")
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func (r *knowledgeFolderRepository) GetDescendantIDs(
	ctx context.Context, tenantID uint64, kbID string, folderIDs []string,
) ([]string, error) {
	folderIDs = uniqueNonEmpty(folderIDs)
	if len(folderIDs) == 0 {
		return nil, nil
	}

	var roots []*types.KnowledgeFolder
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND id IN ?", tenantID, kbID, folderIDs).
		Find(&roots).Error; err != nil {
		return nil, err
	}
	if len(roots) != len(folderIDs) {
		return nil, ErrKnowledgeFolderNotFound
	}

	query := r.db.WithContext(ctx).Model(&types.KnowledgeFolder{}).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID)
	conditions := make([]string, 0, len(roots)*2)
	args := make([]interface{}, 0, len(roots)*2)
	for _, root := range roots {
		conditions = append(conditions, "id = ?", "path LIKE ? ESCAPE '!'")
		args = append(args, root.ID, escapeFolderLike(root.Path)+"/%")
	}
	var ids []string
	err := query.Where("("+strings.Join(conditions, " OR ")+")", args...).Distinct("id").Pluck("id", &ids).Error
	return ids, err
}

func (r *knowledgeFolderRepository) CountKnowledgeByFolder(
	ctx context.Context, tenantID uint64, kbID string,
) (map[string]int64, error) {
	type folderCount struct {
		FolderID string
		Count    int64
	}
	var rows []folderCount
	err := r.db.WithContext(ctx).Model(&types.Knowledge{}).
		Select("folder_id, COUNT(*) AS count").
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID).
		Group("folder_id").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int64, len(rows))
	for _, row := range rows {
		counts[row.FolderID] = row.Count
	}
	return counts, nil
}

func (r *knowledgeFolderRepository) CheckSiblingName(
	ctx context.Context, tenantID uint64, kbID, parentID, name, excludeID string,
) (bool, error) {
	query := r.db.WithContext(ctx).Model(&types.KnowledgeFolder{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND parent_id = ? AND name = ?",
			tenantID, kbID, parentID, name)
	if excludeID != "" {
		query = query.Where("id <> ?", excludeID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *knowledgeFolderRepository) MoveSubtree(
	ctx context.Context, folder *types.KnowledgeFolder, oldPath, newPath string, depthDelta int,
) error {
	tx := r.db.WithContext(ctx)
	var descendants []*types.KnowledgeFolder
	if err := tx.Where(
		"tenant_id = ? AND knowledge_base_id = ? AND path LIKE ? ESCAPE '!'",
		folder.TenantID, folder.KnowledgeBaseID, escapeFolderLike(oldPath)+"/%",
	).Find(&descendants).Error; err != nil {
		return err
	}
	if err := tx.Model(&types.KnowledgeFolder{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?",
			folder.TenantID, folder.KnowledgeBaseID, folder.ID).
		Updates(map[string]interface{}{
			"parent_id": folder.ParentID, "path": newPath, "depth": folder.Depth,
		}).Error; err != nil {
		return err
	}
	for _, descendant := range descendants {
		descendantPath := newPath + strings.TrimPrefix(descendant.Path, oldPath)
		result := tx.Model(&types.KnowledgeFolder{}).
			Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?",
				folder.TenantID, folder.KnowledgeBaseID, descendant.ID).
			Updates(map[string]interface{}{"path": descendantPath, "depth": descendant.Depth + depthDelta})
		if result.Error != nil {
			return result.Error
		}
	}
	return nil
}
