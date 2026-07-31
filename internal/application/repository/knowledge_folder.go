package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// Sentinel errors for the knowledge folder tree. Handlers map these onto HTTP
// status codes, so service code must wrap rather than replace them.
var (
	ErrKnowledgeFolderNotFound = errors.New("knowledge folder not found")
	ErrKnowledgeFolderConflict = errors.New("a folder with this name already exists here")
	ErrKnowledgeFolderNotEmpty = errors.New("knowledge folder is not empty")
	ErrKnowledgeFolderTooDeep  = errors.New("knowledge folder nesting limit reached")
)

// knowledgeFolderRepository implements the knowledge folder repository.
type knowledgeFolderRepository struct {
	db *gorm.DB
}

// NewKnowledgeFolderRepository creates a new knowledge folder repository.
func NewKnowledgeFolderRepository(db *gorm.DB) interfaces.KnowledgeFolderRepository {
	return &knowledgeFolderRepository{db: db}
}

// scope applies the tenant + knowledge base isolation every folder query needs.
// Folders are scoped by both, not just by knowledge base: relying on the
// handler alone to prove ownership leaves the data layer open to any future
// caller that forgets the check.
func (r *knowledgeFolderRepository) scope(ctx context.Context, tenantID uint64, kbID string) *gorm.DB {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID)
}

// CreateFolder inserts a new folder row.
func (r *knowledgeFolderRepository) CreateFolder(ctx context.Context, folder *types.KnowledgeFolder) error {
	return r.db.WithContext(ctx).Create(folder).Error
}

// GetFolderByID loads a single folder.
func (r *knowledgeFolderRepository) GetFolderByID(
	ctx context.Context, tenantID uint64, kbID string, id string,
) (*types.KnowledgeFolder, error) {
	var folder types.KnowledgeFolder
	if err := r.scope(ctx, tenantID, kbID).Where("id = ?", id).First(&folder).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrKnowledgeFolderNotFound
		}
		return nil, err
	}
	return &folder, nil
}

// GetChildFolderByName resolves a folder by its name within one parent, which
// is how sibling-name uniqueness and find-or-create path walks are checked.
func (r *knowledgeFolderRepository) GetChildFolderByName(
	ctx context.Context, tenantID uint64, kbID string, parentID string, name string,
) (*types.KnowledgeFolder, error) {
	var folder types.KnowledgeFolder
	if err := r.scope(ctx, tenantID, kbID).
		Where("parent_id = ? AND name = ?", parentID, name).
		First(&folder).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrKnowledgeFolderNotFound
		}
		return nil, err
	}
	return &folder, nil
}

// ListChildFolders returns the direct children of parentID.
func (r *knowledgeFolderRepository) ListChildFolders(
	ctx context.Context, tenantID uint64, kbID string, parentID string,
) ([]*types.KnowledgeFolder, error) {
	var folders []*types.KnowledgeFolder
	if err := r.scope(ctx, tenantID, kbID).
		Where("parent_id = ?", parentID).
		Order("sort_order ASC").Order("name ASC").
		Find(&folders).Error; err != nil {
		return nil, err
	}
	return folders, nil
}

// ListAllFolders returns every folder of a knowledge base ordered parent-first.
// The folder set is navigation-sized (orders of magnitude smaller than the
// document set), so one query plus in-memory assembly beats a request per level.
func (r *knowledgeFolderRepository) ListAllFolders(
	ctx context.Context, tenantID uint64, kbID string,
) ([]*types.KnowledgeFolder, error) {
	var folders []*types.KnowledgeFolder
	if err := r.scope(ctx, tenantID, kbID).
		Order("depth ASC").Order("sort_order ASC").Order("name ASC").
		Find(&folders).Error; err != nil {
		return nil, err
	}
	return folders, nil
}

// ListSubtreeFolders returns the folder identified by prefix together with all
// of its descendants, using the materialized path so depth costs nothing.
func (r *knowledgeFolderRepository) ListSubtreeFolders(
	ctx context.Context, tenantID uint64, kbID string, pathPrefix string,
) ([]*types.KnowledgeFolder, error) {
	if pathPrefix == "" {
		return nil, nil
	}
	var folders []*types.KnowledgeFolder
	if err := r.scope(ctx, tenantID, kbID).
		Where("path LIKE ?", escapeLikeKeyword(pathPrefix)+"%").
		Order("depth ASC").
		Find(&folders).Error; err != nil {
		return nil, err
	}
	return folders, nil
}

// UpdateFolder persists a full folder row.
func (r *knowledgeFolderRepository) UpdateFolder(ctx context.Context, folder *types.KnowledgeFolder) error {
	folder.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).Omit("DeletedAt", "CreatedAt").Save(folder).Error
}

// UpdateFoldersTx applies a batch of folder rows inside a single transaction.
// Moving a folder rewrites the path of every descendant; doing that row by row
// without a transaction can leave the tree half-rewritten if any write fails,
// stranding descendants under a prefix that no longer resolves.
func (r *knowledgeFolderRepository) UpdateFoldersTx(
	ctx context.Context, folders []*types.KnowledgeFolder,
) error {
	if len(folders) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		for _, folder := range folders {
			folder.UpdatedAt = now
			if err := tx.Model(&types.KnowledgeFolder{}).
				Where("id = ?", folder.ID).
				Updates(map[string]interface{}{
					"parent_id":  folder.ParentID,
					"name":       folder.Name,
					"path":       folder.Path,
					"depth":      folder.Depth,
					"updated_at": now,
				}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteFolder soft-deletes a folder only when it holds no live documents and
// no live child folders.
//
// The emptiness test lives in the same statement as the delete on purpose. A
// concurrent upload or folder creation can slip between a separate check and
// the delete, which would leave those rows pointing at a folder_id that no
// longer resolves — documents would vanish from every folder view. Letting the
// database evaluate both conditions atomically makes that race impossible.
func (r *knowledgeFolderRepository) DeleteFolder(
	ctx context.Context, tenantID uint64, kbID string, id string,
) error {
	result := r.db.WithContext(ctx).Exec(`
UPDATE knowledge_folders
SET deleted_at = ?
WHERE tenant_id = ? AND knowledge_base_id = ? AND id = ? AND deleted_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM knowledges
    WHERE knowledge_base_id = ? AND folder_id = ? AND deleted_at IS NULL
  )
  AND NOT EXISTS (
    SELECT 1 FROM knowledge_folders AS child
    WHERE child.knowledge_base_id = ? AND child.parent_id = ? AND child.deleted_at IS NULL
  )`, time.Now(), tenantID, kbID, id, kbID, id, kbID, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		// Distinguish "gone" from "still populated" so the caller can report
		// something actionable instead of a generic failure.
		var count int64
		if err := r.scope(ctx, tenantID, kbID).Model(&types.KnowledgeFolder{}).
			Where("id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return ErrKnowledgeFolderNotFound
		}
		return ErrKnowledgeFolderNotEmpty
	}
	return nil
}

// DeleteFolderTree removes a folder and its descendants, relocating the
// documents they hold to reparentTo. It runs as one transaction so documents
// can never be left referencing a deleted folder.
func (r *knowledgeFolderRepository) DeleteFolderTree(
	ctx context.Context, tenantID uint64, kbID string, folderIDs []string, reparentTo string,
) error {
	if len(folderIDs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		// Relocate documents first: if this fails the delete never happens and
		// the tree is left exactly as it was.
		if err := tx.Model(&types.Knowledge{}).
			Where("tenant_id = ? AND knowledge_base_id = ? AND folder_id IN ?", tenantID, kbID, folderIDs).
			Updates(map[string]interface{}{"folder_id": reparentTo, "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(&types.KnowledgeFolder{}).
			Where("tenant_id = ? AND knowledge_base_id = ? AND id IN ?", tenantID, kbID, folderIDs).
			Update("deleted_at", now).Error
	})
}

// CountDocumentsByFolder returns the direct document count per folder id in a
// single grouped query. The root bucket is keyed by the empty string.
func (r *knowledgeFolderRepository) CountDocumentsByFolder(
	ctx context.Context, tenantID uint64, kbID string,
) (map[string]int64, error) {
	type folderCount struct {
		FolderID string
		Cnt      int64
	}
	var rows []folderCount
	if err := r.db.WithContext(ctx).Model(&types.Knowledge{}).
		Select("folder_id, COUNT(*) as cnt").
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID).
		Where("parse_status <> ?", types.ParseStatusDeleting).
		Group("folder_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	counts := make(map[string]int64, len(rows))
	for _, row := range rows {
		counts[row.FolderID] = row.Cnt
	}
	return counts, nil
}

// ListKnowledgeIDsByFolderIDs returns the ids of documents filed in any of the
// given folders. This is what turns a folder scope into the concrete document
// set the retrieval layer already knows how to filter on.
func (r *knowledgeFolderRepository) ListKnowledgeIDsByFolderIDs(
	ctx context.Context, tenantID uint64, kbID string, folderIDs []string,
) ([]string, error) {
	if len(folderIDs) == 0 {
		return nil, nil
	}
	var ids []string
	if err := r.db.WithContext(ctx).Model(&types.Knowledge{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND folder_id IN ?", tenantID, kbID, folderIDs).
		Where("parse_status <> ?", types.ParseStatusDeleting).
		Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// MoveKnowledgeToFolder repoints a batch of documents at targetFolderID.
// Restricting the UPDATE to the tenant and knowledge base means ids belonging
// elsewhere are silently skipped rather than yanked across a boundary.
func (r *knowledgeFolderRepository) MoveKnowledgeToFolder(
	ctx context.Context, tenantID uint64, kbID string, knowledgeIDs []string, targetFolderID string,
) (int64, error) {
	if len(knowledgeIDs) == 0 {
		return 0, nil
	}
	result := r.db.WithContext(ctx).Model(&types.Knowledge{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND id IN ?", tenantID, kbID, knowledgeIDs).
		Updates(map[string]interface{}{"folder_id": targetFolderID, "updated_at": time.Now()})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

// ClearFolderAssignments resets documents in the given folders back to the
// root. It is used when a knowledge base's folder tree is torn down.
func (r *knowledgeFolderRepository) ClearFolderAssignments(
	ctx context.Context, tenantID uint64, kbID string,
) error {
	return r.db.WithContext(ctx).Model(&types.Knowledge{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND folder_id <> ?",
			tenantID, kbID, types.KnowledgeFolderRootID).
		Updates(map[string]interface{}{
			"folder_id":  types.KnowledgeFolderRootID,
			"updated_at": time.Now(),
		}).Error
}
