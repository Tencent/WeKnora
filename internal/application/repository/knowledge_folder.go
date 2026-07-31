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

// ErrKnowledgeFolderNotFound is returned when a knowledge folder is not found.
var ErrKnowledgeFolderNotFound = errors.New("knowledge folder not found")

// ErrKnowledgeFolderConflict is returned when a sibling folder with the same
// name already exists under the same parent.
var ErrKnowledgeFolderConflict = errors.New("knowledge folder name conflict")

// ErrKnowledgeFolderNotEmpty is returned when a folder still has a live
// document or child folder at the instant an atomic delete is attempted.
var ErrKnowledgeFolderNotEmpty = errors.New("knowledge folder is not empty")

type knowledgeFolderRepository struct {
	db *gorm.DB
}

// NewKnowledgeFolderRepository creates a new knowledge folder repository
func NewKnowledgeFolderRepository(db *gorm.DB) interfaces.KnowledgeFolderRepository {
	return &knowledgeFolderRepository{db: db}
}

func (r *knowledgeFolderRepository) Create(ctx context.Context, folder *types.KnowledgeFolder) error {
	return r.db.WithContext(ctx).Create(folder).Error
}

func (r *knowledgeFolderRepository) GetByID(
	ctx context.Context, kbID string, id string,
) (*types.KnowledgeFolder, error) {
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

func (r *knowledgeFolderRepository) GetChildByName(
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

func (r *knowledgeFolderRepository) ListAll(
	ctx context.Context, kbID string,
) ([]*types.KnowledgeFolder, error) {
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

// SearchFoldersInScopes returns non-empty folders in the requested scopes.
func (r *knowledgeFolderRepository) SearchFoldersInScopes(
	ctx context.Context,
	scopes []types.KnowledgeSearchScope,
	keyword string,
	offset, limit int,
) ([]*types.KnowledgeFolderSearchResult, bool, int64, error) {
	if len(scopes) == 0 {
		return nil, false, 0, nil
	}
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	keyword = strings.TrimSpace(keyword)

	placeholders := make([]string, len(scopes))
	args := make([]interface{}, 0, len(scopes)*2)
	for i, s := range scopes {
		placeholders[i] = "(?,?)"
		args = append(args, s.TenantID, s.KBID)
	}
	scopeCondition := "(knowledge_folders.tenant_id, knowledge_folders.knowledge_base_id) IN (" +
		strings.Join(placeholders, ",") + ")"

	baseQuery := func() *gorm.DB {
		q := r.db.WithContext(ctx).
			Table("knowledge_folders").
			Joins(`JOIN knowledges
				ON knowledges.knowledge_base_id = knowledge_folders.knowledge_base_id
				AND knowledges.folder_id = knowledge_folders.id
				AND knowledges.deleted_at IS NULL`).
			Where(scopeCondition, args...).
			Where("knowledge_folders.deleted_at IS NULL").
			Group("knowledge_folders.id")
		if keyword != "" {
			escaped := strings.ToLower(escapeLikeKeyword(keyword))
			q = q.Where(
				"(LOWER(knowledge_folders.name) LIKE ? OR LOWER(knowledge_folders.path) LIKE ?)",
				"%"+escaped+"%", "%"+escaped+"%",
			)
		}
		return q
	}

	var total int64
	if err := r.db.WithContext(ctx).
		Table("(?) AS grouped", baseQuery().Select("knowledge_folders.id")).
		Count(&total).Error; err != nil {
		return nil, false, 0, err
	}

	type folderRow struct {
		types.KnowledgeFolder
		KnowledgeCount    int64  `gorm:"column:knowledge_count"`
		KnowledgeBaseName string `gorm:"column:knowledge_base_name"`
	}
	var rows []folderRow
	if err := baseQuery().
		Select(`knowledge_folders.*,
			COUNT(knowledges.id) AS knowledge_count,
			MAX(knowledge_bases.name) AS knowledge_base_name`).
		Joins(`JOIN knowledge_bases
			ON knowledge_bases.id = knowledge_folders.knowledge_base_id
			AND knowledge_bases.tenant_id = knowledge_folders.tenant_id`).
		Order("knowledge_folders.path ASC, knowledge_folders.id ASC").
		Offset(offset).
		Limit(limit + 1).
		Scan(&rows).Error; err != nil {
		return nil, false, 0, err
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	results := make([]*types.KnowledgeFolderSearchResult, len(rows))
	for i, row := range rows {
		results[i] = &types.KnowledgeFolderSearchResult{
			KnowledgeFolder:   row.KnowledgeFolder,
			KnowledgeCount:    row.KnowledgeCount,
			KnowledgeBaseName: row.KnowledgeBaseName,
		}
	}
	return results, hasMore, total, nil
}

// ListChildren returns direct children with document counts and child flags.
func (r *knowledgeFolderRepository) ListChildren(
	ctx context.Context, kbID string, parentID string,
) ([]*types.KnowledgeFolderNode, error) {
	var folders []*types.KnowledgeFolder
	if err := r.db.WithContext(ctx).
		Where("knowledge_base_id = ? AND parent_id = ?", kbID, parentID).
		Order("sort_order ASC").
		Order("name ASC").
		Find(&folders).Error; err != nil {
		return nil, err
	}
	if len(folders) == 0 {
		return nil, nil
	}

	ids := make([]string, 0, len(folders))
	for _, f := range folders {
		ids = append(ids, f.ID)
	}

	type folderCount struct {
		FolderID string
		Total    int64
	}
	var counts []folderCount
	if err := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Select("folder_id AS folder_id", "COUNT(*) AS total").
		Where("knowledge_base_id = ? AND folder_id IN ?", kbID, ids).
		Group("folder_id").
		Scan(&counts).Error; err != nil {
		return nil, err
	}
	countByID := make(map[string]int64, len(counts))
	for _, c := range counts {
		countByID[c.FolderID] = c.Total
	}

	var parentsWithChildren []string
	if err := r.db.WithContext(ctx).
		Model(&types.KnowledgeFolder{}).
		Distinct("parent_id").
		Where("knowledge_base_id = ? AND parent_id IN ?", kbID, ids).
		Pluck("parent_id", &parentsWithChildren).Error; err != nil {
		return nil, err
	}
	hasChildSet := make(map[string]bool, len(parentsWithChildren))
	for _, id := range parentsWithChildren {
		hasChildSet[id] = true
	}

	nodes := make([]*types.KnowledgeFolderNode, 0, len(folders))
	for _, f := range folders {
		nodes = append(nodes, &types.KnowledgeFolderNode{
			KnowledgeFolder: *f,
			KnowledgeCount:  countByID[f.ID],
			HasChildren:     hasChildSet[f.ID],
		})
	}
	return nodes, nil
}

func (r *knowledgeFolderRepository) Update(ctx context.Context, folder *types.KnowledgeFolder) error {
	return updateKnowledgeFolderRow(r.db.WithContext(ctx), folder)
}

// UpdateSubtree persists the given folder rows atomically, so a failed move
// cannot leave the materialized paths half-rewritten and permanently out of
// sync with the parent adjacency.
func (r *knowledgeFolderRepository) UpdateSubtree(
	ctx context.Context, folders []*types.KnowledgeFolder,
) error {
	if len(folders) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, folder := range folders {
			if err := updateKnowledgeFolderRow(tx, folder); err != nil {
				return err
			}
		}
		return nil
	})
}

// updateKnowledgeFolderRow writes one folder on the given handle (a plain
// connection or a transaction), mirroring the helper split in wiki_page.go so
// Update and UpdateSubtree can never drift on the column set.
func updateKnowledgeFolderRow(db *gorm.DB, folder *types.KnowledgeFolder) error {
	result := db.
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

// Delete soft-deletes a folder. The emptiness test lives in the same SQL
// statement as the delete: a document move or child-folder create can race
// the service's earlier checks, and a check-then-delete sequence would leave
// documents pointing at a dead folder_id.
func (r *knowledgeFolderRepository) Delete(ctx context.Context, kbID string, id string) error {
	result := r.db.WithContext(ctx).Exec(`
UPDATE knowledge_folders
SET deleted_at = ?
WHERE knowledge_base_id = ? AND id = ? AND deleted_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM knowledges
    WHERE knowledge_base_id = ? AND folder_id = ? AND deleted_at IS NULL
  )
  AND NOT EXISTS (
    SELECT 1 FROM knowledge_folders AS child
    WHERE child.knowledge_base_id = ? AND child.parent_id = ? AND child.deleted_at IS NULL
  )`, time.Now(), kbID, id, kbID, id, kbID, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		// Distinguish "gone" from "not empty" for a usable API error.
		if _, err := r.GetByID(ctx, kbID, id); err != nil {
			return ErrKnowledgeFolderNotFound
		}
		return ErrKnowledgeFolderNotEmpty
	}
	return nil
}

func (r *knowledgeFolderRepository) DeleteByKnowledgeBase(ctx context.Context, kbID string) error {
	return r.db.WithContext(ctx).
		Where("knowledge_base_id = ?", kbID).
		Delete(&types.KnowledgeFolder{}).Error
}

func (r *knowledgeFolderRepository) CountKnowledgeInFolders(
	ctx context.Context, kbID string, folderIDs []string,
) (int64, error) {
	if len(folderIDs) == 0 {
		return 0, nil
	}
	var total int64
	if err := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Where("knowledge_base_id = ? AND folder_id IN ?", kbID, folderIDs).
		Count(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

func (r *knowledgeFolderRepository) ListKnowledgeIDsInFolders(
	ctx context.Context, tenantID uint64, kbID string, folderIDs []string,
) ([]string, error) {
	if len(folderIDs) == 0 {
		return nil, nil
	}
	var ids []string
	if err := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND folder_id IN ?", tenantID, kbID, folderIDs).
		Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

func (r *knowledgeFolderRepository) BatchUpdateKnowledgeFolder(
	ctx context.Context, kbID string, knowledgeIDs []string, folderID string,
) (int64, error) {
	if len(knowledgeIDs) == 0 {
		return 0, nil
	}
	result := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Where("knowledge_base_id = ? AND id IN ?", kbID, knowledgeIDs).
		Update("folder_id", folderID)
	return result.RowsAffected, result.Error
}

func (r *knowledgeFolderRepository) MoveKnowledgeBetweenFolders(
	ctx context.Context, kbID string, fromFolderID string, toFolderID string,
) (int64, error) {
	result := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Where("knowledge_base_id = ? AND folder_id = ?", kbID, fromFolderID).
		Update("folder_id", toFolderID)
	return result.RowsAffected, result.Error
}

func (r *knowledgeFolderRepository) ResetKnowledgeFolders(ctx context.Context, kbID string) (int64, error) {
	result := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Where("knowledge_base_id = ? AND folder_id <> ?", kbID, types.KnowledgeFolderRootID).
		Update("folder_id", types.KnowledgeFolderRootID)
	return result.RowsAffected, result.Error
}

func (r *knowledgeFolderRepository) ListPathedRootKnowledge(
	ctx context.Context, kbID string, limit int,
) ([]*types.Knowledge, error) {
	var rows []*types.Knowledge
	if err := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Select("id", "file_name").
		Where("knowledge_base_id = ? AND folder_id = ? AND file_name LIKE ?",
			kbID, types.KnowledgeFolderRootID, "%/%").
		Order("id ASC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
