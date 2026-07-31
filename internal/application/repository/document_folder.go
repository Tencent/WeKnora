package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// --- Document folder tree (document_folders) ---

// ErrDocumentFolderNotFound is returned when a document folder is not found
// (or does not belong to the queried KB — IDOR fail-closed so existence is
// not leaked to a caller from a different KB).
var ErrDocumentFolderNotFound = errors.New("document folder not found")

// documentFolderRepository implements interfaces.DocumentFolderRepository on
// top of a *gorm.DB. Methods mirror the wikiPageRepository folder methods in
// shape — the two features share the same adjacency-list + materialized-path
// storage model.
type documentFolderRepository struct {
	db *gorm.DB
}

// NewDocumentFolderRepository returns a DocumentFolderRepository backed by db.
// Returns the interface type (not the concrete pointer) so dig wires it into
// consumers that depend on interfaces.DocumentFolderRepository. Mirrors
// NewWikiPageRepository.
func NewDocumentFolderRepository(db *gorm.DB) interfaces.DocumentFolderRepository {
	return &documentFolderRepository{db: db}
}

func (r *documentFolderRepository) CreateFolder(ctx context.Context, folder *types.DocumentFolder) error {
	return r.db.WithContext(ctx).Create(folder).Error
}

func (r *documentFolderRepository) GetFolderByID(
	ctx context.Context, kbID string, id string,
) (*types.DocumentFolder, error) {
	var folder types.DocumentFolder
	if err := r.db.WithContext(ctx).
		Where("knowledge_base_id = ? AND id = ?", kbID, id).
		First(&folder).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrDocumentFolderNotFound
		}
		return nil, err
	}
	return &folder, nil
}

func (r *documentFolderRepository) GetChildFolderByName(
	ctx context.Context, kbID string, parentID string, name string,
) (*types.DocumentFolder, error) {
	var folder types.DocumentFolder
	if err := r.db.WithContext(ctx).
		Where("knowledge_base_id = ? AND parent_id = ? AND name = ?", kbID, parentID, name).
		First(&folder).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrDocumentFolderNotFound
		}
		return nil, err
	}
	return &folder, nil
}

func (r *documentFolderRepository) ListChildFolders(
	ctx context.Context,
	kbID string,
	parentID string,
	keyword string,
	after *types.DocumentFolderPageCursor,
	limit int,
) ([]*types.DocumentFolder, bool, error) {
	var folders []*types.DocumentFolder
	query := r.db.WithContext(ctx).
		Where("knowledge_base_id = ? AND parent_id = ?", kbID, parentID)
	if keyword = strings.TrimSpace(keyword); keyword != "" {
		escaped := strings.ToLower(escapeLikeKeyword(keyword))
		query = query.Where(
			"(LOWER(name) LIKE ? OR LOWER(path) LIKE ?)",
			"%"+escaped+"%",
			"%"+escaped+"%",
		)
	}
	if after != nil {
		query = query.Where(
			`(
				name > ?
				OR (name = ? AND id > ?)
			)`,
			after.Name,
			after.Name,
			after.ID,
		)
	}
	if err := query.
		Order("name ASC").
		Order("id ASC").
		Limit(limit + 1).
		Find(&folders).Error; err != nil {
		return nil, false, err
	}
	hasMore := len(folders) > limit
	if hasMore {
		folders = folders[:limit]
	}
	return folders, hasMore, nil
}

func (r *documentFolderRepository) ListAllFolders(
	ctx context.Context, kbID string,
) ([]*types.DocumentFolder, error) {
	var folders []*types.DocumentFolder
	if err := r.db.WithContext(ctx).
		Where("knowledge_base_id = ?", kbID).
		Order("depth ASC").
		Order("path ASC").
		Find(&folders).Error; err != nil {
		return nil, err
	}
	return folders, nil
}

func (r *documentFolderRepository) UpdateFolder(ctx context.Context, folder *types.DocumentFolder) error {
	result := r.db.WithContext(ctx).
		Model(&types.DocumentFolder{}).
		Where("id = ?", folder.ID).
		Updates(map[string]interface{}{
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
		return ErrDocumentFolderNotFound
	}
	return nil
}

// UpdateFoldersInTransaction serializes every folder-tree mutation for a KB by
// locking its stable knowledge_bases row before running fn. The fn receives a
// tx-scoped repository so all validation reads and writes share the lock and
// commit or roll back together.
func (r *documentFolderRepository) UpdateFoldersInTransaction(
	ctx context.Context,
	kbID string,
	fn func(txFolderRepo interfaces.DocumentFolderRepository) error,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockKnowledgeBaseForUpdate(ctx, tx, kbID); err != nil {
			return err
		}
		txRepo := &documentFolderRepository{db: tx}
		return fn(txRepo)
	})
}

func (r *documentFolderRepository) DeleteFolder(ctx context.Context, kbID string, id string) error {
	result := r.db.WithContext(ctx).
		Where("knowledge_base_id = ? AND id = ?", kbID, id).
		Delete(&types.DocumentFolder{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrDocumentFolderNotFound
	}
	return nil
}

// DeleteFoldersByKnowledgeBase soft-deletes every folder row scoped to kbID.
// Used by KB-delete cleanup so the folder tree does not outlive its KB. The
// operation is idempotent: deleting an already-empty folder set is a no-op.
func (r *documentFolderRepository) DeleteFoldersByKnowledgeBase(ctx context.Context, kbID string) error {
	return r.db.WithContext(ctx).
		Where("knowledge_base_id = ?", kbID).
		Delete(&types.DocumentFolder{}).Error
}

func (r *documentFolderRepository) HasChildFolders(
	ctx context.Context, kbID string, parentID string,
) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).
		Model(&types.DocumentFolder{}).
		Where("knowledge_base_id = ? AND parent_id = ?", kbID, parentID).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *documentFolderRepository) HasChildFoldersBatch(
	ctx context.Context, kbID string, parentIDs []string,
) (map[string]bool, error) {
	out := make(map[string]bool, len(parentIDs))
	if len(parentIDs) == 0 {
		return out, nil
	}
	type childCount struct {
		ParentID string
		Cnt      int64
	}
	var rows []childCount
	if err := r.db.WithContext(ctx).
		Model(&types.DocumentFolder{}).
		Select("parent_id, COUNT(*) AS cnt").
		Where("knowledge_base_id = ? AND parent_id IN ?", kbID, parentIDs).
		Group("parent_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ParentID] = row.Cnt > 0
	}
	return out, nil
}

func (r *documentFolderRepository) CountDocumentsInFolders(
	ctx context.Context, tenantID uint64, kbID string, folderIDs []string,
) (map[string]int64, error) {
	out := make(map[string]int64, len(folderIDs))
	if len(folderIDs) == 0 {
		return out, nil
	}
	type folderCount struct {
		FolderID string
		Cnt      int64
	}
	var rows []folderCount
	if err := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Select("folder_id, COUNT(*) as cnt").
		Where("tenant_id = ? AND knowledge_base_id = ? AND folder_id IN ?", tenantID, kbID, folderIDs).
		Group("folder_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.FolderID] = row.Cnt
	}
	return out, nil
}

func (r *documentFolderRepository) CountAllFolders(ctx context.Context, kbID string) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).
		Model(&types.DocumentFolder{}).
		Where("knowledge_base_id = ?", kbID).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (r *documentFolderRepository) HasDocumentsInSubtree(
	ctx context.Context, tenantID uint64, kbID string, subtreeIDs []string,
) (bool, error) {
	if len(subtreeIDs) == 0 {
		return false, nil
	}
	var count int64
	if err := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND folder_id IN ?", tenantID, kbID, subtreeIDs).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *documentFolderRepository) ListKnowledgeInFolders(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	folderIDs []string,
) ([]*types.Knowledge, error) {
	if len(folderIDs) == 0 {
		return nil, nil
	}
	var knowledges []*types.Knowledge
	if err := r.db.WithContext(ctx).
		Select("id", "tenant_id", "knowledge_base_id", "folder_id", "parse_status").
		Where("tenant_id = ? AND knowledge_base_id = ? AND folder_id IN ?", tenantID, kbID, folderIDs).
		Find(&knowledges).Error; err != nil {
		return nil, err
	}
	return knowledges, nil
}

func (r *documentFolderRepository) SetKnowledgeFolderID(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	knowledgeIDs []string,
	folderID string,
) (int64, error) {
	if len(knowledgeIDs) == 0 {
		return 0, nil
	}
	result := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND id IN ?", tenantID, kbID, knowledgeIDs).
		Update("folder_id", folderID)
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

func (r *documentFolderRepository) SearchFoldersInScopes(
	ctx context.Context,
	scopes []types.KnowledgeSearchScope,
	keyword string,
	offset int,
	limit int,
) ([]*types.DocumentFolderSearchResult, bool, int64, error) {
	if len(scopes) == 0 {
		return nil, false, 0, nil
	}

	scopeParts := make([]string, 0, len(scopes))
	scopeArgs := make([]interface{}, 0, len(scopes)*2)
	for _, scope := range scopes {
		if scope.TenantID == 0 || scope.KBID == "" {
			continue
		}
		scopeParts = append(scopeParts, "(document_folders.tenant_id = ? AND document_folders.knowledge_base_id = ?)")
		scopeArgs = append(scopeArgs, scope.TenantID, scope.KBID)
	}
	if len(scopeParts) == 0 {
		return nil, false, 0, nil
	}

	query := r.db.WithContext(ctx).
		Table("document_folders").
		Joins("JOIN knowledge_bases ON knowledge_bases.id = document_folders.knowledge_base_id AND knowledge_bases.tenant_id = document_folders.tenant_id").
		Where("document_folders.deleted_at IS NULL").
		Where("knowledge_bases.type = ?", types.KnowledgeBaseTypeDocument).
		Where("knowledge_bases.deleted_at IS NULL").
		Where("("+strings.Join(scopeParts, " OR ")+")", scopeArgs...)
	if keyword = strings.TrimSpace(keyword); keyword != "" {
		escaped := strings.ToLower(escapeLikeKeyword(keyword))
		query = query.Where(
			"LOWER(document_folders.name) LIKE ?",
			"%"+escaped+"%",
		)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, false, 0, fmt.Errorf("count folder search results: %w", err)
	}

	var results []*types.DocumentFolderSearchResult
	if err := query.
		Select(`
			document_folders.id,
			document_folders.name,
			document_folders.path,
			document_folders.parent_id,
			document_folders.knowledge_base_id,
			knowledge_bases.name AS knowledge_base_name
		`).
		Order("document_folders.name ASC").
		Order("document_folders.path ASC").
		Order("document_folders.id ASC").
		Offset(offset).
		Limit(limit + 1).
		Scan(&results).Error; err != nil {
		return nil, false, 0, fmt.Errorf("search folders: %w", err)
	}
	hasMore := len(results) > limit
	if hasMore {
		results = results[:limit]
	}
	return results, hasMore, total, nil
}
