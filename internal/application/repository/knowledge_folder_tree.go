package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

const knowledgeFolderPathLikeClause = "path LIKE ? ESCAPE '!'"

func escapeKnowledgeFolderLikeLiteral(value string) string {
	return strings.NewReplacer(
		"!", "!!",
		"\\", "!\\",
		"%", "!%",
		"_", "!_",
	).Replace(value)
}

func knowledgeFolderPathPrefixPattern(pathPrefix string) string {
	return escapeKnowledgeFolderLikeLiteral(pathPrefix) + "%"
}

func (r *knowledgeFolderReader) ListByIDs(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	folderIDs []string,
) ([]*types.KnowledgeFolder, error) {
	if len(folderIDs) == 0 {
		return []*types.KnowledgeFolder{}, nil
	}

	var folders []*types.KnowledgeFolder
	err := r.db.WithContext(ctx).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND id IN ?",
			tenantID,
			kbID,
			folderIDs,
		).
		Find(&folders).Error
	if err != nil {
		return nil, err
	}
	return folders, nil
}

func (r *knowledgeFolderReader) ListSubtreeFolders(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	rootID string,
	pathPrefix string,
) ([]*types.KnowledgeFolder, error) {
	if (rootID == types.KnowledgeFolderRootID) != (pathPrefix == "") {
		return nil, fmt.Errorf("%w: incomplete subtree scope", ErrKnowledgeFolderInvalid)
	}

	var folders []*types.KnowledgeFolder
	if rootID == types.KnowledgeFolderRootID {
		err := r.db.WithContext(ctx).
			Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID).
			Order("depth ASC").
			Order("path ASC").
			Order("id ASC").
			Find(&folders).Error
		if err != nil {
			return nil, err
		}
		return folders, nil
	}

	err := r.db.WithContext(ctx).Raw(`
		WITH RECURSIVE reachable(id) AS (
			SELECT id
			FROM knowledge_folders
			WHERE tenant_id = ?
				AND knowledge_base_id = ?
				AND id = ?
				AND deleted_at IS NULL
			UNION
			SELECT child.id
			FROM knowledge_folders AS child
			INNER JOIN reachable AS parent ON child.parent_id = parent.id
			WHERE child.tenant_id = ?
				AND child.knowledge_base_id = ?
				AND child.deleted_at IS NULL
		),
		candidates(id) AS (
			SELECT id FROM reachable
			UNION
			SELECT id
			FROM knowledge_folders
			WHERE tenant_id = ?
				AND knowledge_base_id = ?
				AND deleted_at IS NULL
				AND path LIKE ? ESCAPE '!'
		)
		SELECT folder.*
		FROM knowledge_folders AS folder
		INNER JOIN candidates ON candidates.id = folder.id
		WHERE folder.tenant_id = ?
			AND folder.knowledge_base_id = ?
			AND folder.deleted_at IS NULL
		ORDER BY folder.depth ASC, folder.path ASC, folder.id ASC
	`,
		tenantID,
		kbID,
		rootID,
		tenantID,
		kbID,
		tenantID,
		kbID,
		knowledgeFolderPathPrefixPattern(pathPrefix),
		tenantID,
		kbID,
	).Scan(&folders).Error
	if err != nil {
		return nil, err
	}
	return folders, nil
}

func (r *knowledgeFolderTreeRepository) UpdateFolderAttributes(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	folderID string,
	name *string,
	sortOrder *int,
) error {
	updates := make(map[string]interface{}, 3)
	if name != nil {
		updates["name"] = *name
	}
	if sortOrder != nil {
		updates["sort_order"] = *sortOrder
	}
	if len(updates) == 0 {
		return nil
	}
	updates["updated_at"] = time.Now().UTC()

	result := r.db.WithContext(ctx).
		Model(&types.KnowledgeFolder{}).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND id = ?",
			tenantID,
			kbID,
			folderID,
		).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrKnowledgeFolderNotFound
	}
	return nil
}

// MoveSubtree updates a folder and every active descendant in the current transaction.
func (r *knowledgeFolderTreeRepository) MoveSubtree(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	params interfaces.KnowledgeFolderMoveSubtreeParams,
) error {
	if params.FolderID == "" ||
		params.ExpectedPath == "" ||
		params.NewPath == "" ||
		params.ExpectedDepth < 1 ||
		params.ExpectedFolderCount < 1 {
		return fmt.Errorf("%w: incomplete subtree move", ErrKnowledgeFolderInvalid)
	}

	now := time.Now().UTC()
	rootResult := r.db.WithContext(ctx).
		Model(&types.KnowledgeFolder{}).
		Where(
			`tenant_id = ? AND knowledge_base_id = ? AND id = ?
				AND parent_id = ? AND path = ? AND depth = ?`,
			tenantID,
			kbID,
			params.FolderID,
			params.ExpectedParentID,
			params.ExpectedPath,
			params.ExpectedDepth,
		).
		Updates(map[string]interface{}{
			"parent_id":  params.NewParentID,
			"name":       params.NewName,
			"sort_order": params.NewSortOrder,
			"updated_at": now,
		})
	if rootResult.Error != nil {
		return rootResult.Error
	}
	if rootResult.RowsAffected == 0 {
		return ErrKnowledgeFolderDataIntegrity
	}

	subtreeResult := r.db.WithContext(ctx).
		Model(&types.KnowledgeFolder{}).
		Where(
			"tenant_id = ? AND knowledge_base_id = ?",
			tenantID,
			kbID,
		).
		Where(
			knowledgeFolderPathLikeClause,
			knowledgeFolderPathPrefixPattern(params.ExpectedPath),
		).
		Updates(map[string]interface{}{
			"path": gorm.Expr(
				"? || substr(path, length(CAST(? AS TEXT)) + 1)",
				params.NewPath,
				params.ExpectedPath,
			),
			"depth":      gorm.Expr("depth + ?", params.DepthDelta),
			"updated_at": now,
		})
	if subtreeResult.Error != nil {
		return subtreeResult.Error
	}
	if subtreeResult.RowsAffected != params.ExpectedFolderCount {
		return ErrKnowledgeFolderDataIntegrity
	}
	return nil
}
