package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	// ErrKnowledgeFolderNotFound indicates a missing folder or knowledge base.
	ErrKnowledgeFolderNotFound = errors.New("knowledge folder not found")
	// ErrKnowledgeFolderConflict indicates an existing explicit or inferred folder.
	ErrKnowledgeFolderConflict = errors.New("knowledge folder already exists")
	// ErrKnowledgeFolderNotEmpty indicates a folder still contains documents or child folders.
	ErrKnowledgeFolderNotEmpty = errors.New("knowledge folder is not empty")
	// ErrKnowledgeFolderInvalidPath indicates an invalid or noncanonical folder path.
	ErrKnowledgeFolderInvalidPath = errors.New("knowledge folder path exceeds limits or is not canonical")
)

// withKnowledgeFolderMutation serializes folder mutations within one tenant/KB.
// A transaction-scoped advisory lock works across all application instances and
// is released on both commit and rollback. Read committed is explicit: after a
// lock wait, the following reads must see the preceding mutation's commit.
// SQLite uses its existing single-writer transaction configuration instead.
func (r *knowledgeRepository) withKnowledgeFolderMutation(
	ctx context.Context, tenantID uint64, kbID string, fn func(*gorm.DB) error,
) error {
	return r.withKnowledgeFolderMutations(ctx, tenantID, []string{kbID}, fn)
}

// Sort the actual advisory keys, so opposite-direction transfers acquire the
// same source/target locks in the same order. No lock is held over remote I/O.
func (r *knowledgeRepository) withKnowledgeFolderMutations(
	ctx context.Context, tenantID uint64, kbIDs []string, fn func(*gorm.DB) error,
) error {
	if r.db.Name() != "postgres" {
		return r.db.WithContext(ctx).Transaction(fn)
	}
	keys := make([]int64, 0, len(kbIDs))
	seen := map[int64]bool{}
	for _, kbID := range kbIDs {
		digest := sha256.Sum256([]byte("weknora:knowledge-folder:v1:" + strconv.FormatUint(tenantID, 10) + ":" + kbID))
		key := int64(binary.BigEndian.Uint64(digest[:8]))
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, key := range keys {
			if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", key).Error; err != nil {
				return err
			}
		}
		return fn(tx)
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
}

// A subtree rewrite must be lossless. Upload normalization intentionally caps
// paths, but doing that during a move can collapse two distinct descendants.
func validateKnowledgeFolderRewrite(path string) error {
	if path == "" || !utf8.ValidString(path) || strings.ContainsFunc(path, unicode.IsControl) ||
		len(path) > types.MaxKnowledgeFolderPathLength ||
		strings.Count(path, "/")+1 > types.MaxKnowledgeFolderDepth ||
		types.NormalizeKnowledgeFolderPath(path) != path {
		return ErrKnowledgeFolderInvalidPath
	}
	return nil
}

func knowledgeFolderScope(db *gorm.DB, tenantID uint64, kbID, path string) *gorm.DB {
	return db.Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID).
		Where("(path = ? OR path LIKE ? ESCAPE ?)", path, escapeLikeKeyword(path)+"/%", likeEscapeChar)
}

// A path can be an explicit folder or an ancestor inferred from stored files.
func knowledgeFolderExists(db *gorm.DB, tenantID uint64, kbID, path string) (bool, error) {
	if path == "" {
		return true, nil
	}
	var count int64
	if err := knowledgeFolderScope(db.Model(&types.KnowledgeFolder{}), tenantID, kbID, path).
		Count(&count).Error; err != nil || count > 0 {
		return count > 0, err
	}
	err := db.Model(&types.Knowledge{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND parse_status <> ?",
			tenantID, kbID, types.ParseStatusDeleting).
		Where("(folder_path = ? OR folder_path LIKE ? ESCAPE ?)", path, escapeLikeKeyword(path)+"/%", likeEscapeChar).
		Count(&count).Error
	return count > 0, err
}

// CreateKnowledgeFolder persists only explicitly created folders. The unique
// key also covers concurrent submissions for the same canonical path.
func (r *knowledgeRepository) CreateKnowledgeFolder(ctx context.Context, tenantID uint64, kbID, path string) error {
	if err := validateKnowledgeFolderRewrite(path); err != nil {
		return err
	}
	return r.withKnowledgeFolderMutation(ctx, tenantID, kbID, func(tx *gorm.DB) error {
		// Service authorization may predate KB deletion and its completed cleanup.
		// Recheck liveness under the same lock as marker cleanup, and hold a row
		// lock until insertion commits so a concurrent soft-delete runs afterwards.
		var kb types.KnowledgeBase
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Select("id").
			Where("id = ? AND tenant_id = ?", kbID, tenantID).Take(&kb).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrKnowledgeFolderNotFound
			}
			return err
		}
		exists, err := knowledgeFolderExists(tx, tenantID, kbID, types.KnowledgeFolderParent(path))
		if err != nil {
			return err
		}
		if !exists {
			return ErrKnowledgeFolderNotFound
		}
		exists, err = knowledgeFolderExists(tx, tenantID, kbID, path)
		if err != nil {
			return err
		}
		if exists {
			return ErrKnowledgeFolderConflict
		}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&types.KnowledgeFolder{
			TenantID: tenantID, KnowledgeBaseID: kbID, Path: path,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrKnowledgeFolderConflict
		}
		return nil
	})
}

func (r *knowledgeRepository) appendExplicitKnowledgeFolders(ctx context.Context, tenantID uint64, kbID string,
	counts []*types.KnowledgeFolderCount,
) ([]*types.KnowledgeFolderCount, error) {
	var folders []*types.KnowledgeFolder
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID).
		Find(&folders).Error; err != nil {
		return nil, err
	}
	for _, folder := range folders {
		counts = append(counts, &types.KnowledgeFolderCount{FolderPath: folder.Path})
	}
	return counts, nil
}

// DeleteKnowledgeFolder never touches documents. Its emptiness conditions are
// part of the DELETE itself so a stale UI cannot delete a populated folder.
func (r *knowledgeRepository) DeleteKnowledgeFolder(ctx context.Context, tenantID uint64, kbID, path string) error {
	return r.withKnowledgeFolderMutation(ctx, tenantID, kbID, func(tx *gorm.DB) error {
		result := tx.Exec(`DELETE FROM knowledge_folders
WHERE tenant_id = ? AND knowledge_base_id = ? AND path = ?
AND NOT EXISTS (SELECT 1 FROM knowledges WHERE tenant_id = ? AND knowledge_base_id = ?
    AND deleted_at IS NULL AND (folder_path = ? OR folder_path LIKE ? ESCAPE ?))
AND NOT EXISTS (SELECT 1 FROM knowledge_folders AS child WHERE child.tenant_id = ? AND child.knowledge_base_id = ?
    AND child.path LIKE ? ESCAPE ?)`,
			tenantID, kbID, path, tenantID, kbID, path, escapeLikeKeyword(path)+"/%", likeEscapeChar,
			tenantID, kbID, escapeLikeKeyword(path)+"/%", likeEscapeChar)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 {
			return nil
		}
		exists, err := knowledgeFolderExists(tx, tenantID, kbID, path)
		if err != nil {
			return err
		}
		if exists {
			return ErrKnowledgeFolderNotEmpty
		}
		return ErrKnowledgeFolderNotFound
	})
}

// DeleteKnowledgeFolders is used only by admitted whole-KB cleanup tasks.
func (r *knowledgeRepository) DeleteKnowledgeFolders(ctx context.Context, tenantID uint64, kbID string) error {
	return r.withKnowledgeFolderMutation(ctx, tenantID, kbID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID).
			Delete(&types.KnowledgeFolder{}).Error
	})
}

// renameExplicitKnowledgeFolders shares the document rewrite transaction.
// Delete old markers before upserting targets: a move into an ancestor can
// otherwise delete a freshly inserted path that still matches the old prefix.
func renameExplicitKnowledgeFolders(tx *gorm.DB, tenantID uint64, kbID, from, to string) error {
	var folders []*types.KnowledgeFolder
	if err := knowledgeFolderScope(tx, tenantID, kbID, from).Find(&folders).Error; err != nil {
		return err
	}
	if len(folders) == 0 {
		return nil
	}
	// Validate every marker before deleting any of them. The caller validates
	// the complete document rewrite first, under the same lock and transaction.
	for _, folder := range folders {
		target := to + strings.TrimPrefix(folder.Path, from)
		if err := validateKnowledgeFolderRewrite(target); err != nil {
			return fmt.Errorf("%w: descendant would exceed folder limits", err)
		}
		folder.Path = target
	}
	if err := knowledgeFolderScope(tx, tenantID, kbID, from).Delete(&types.KnowledgeFolder{}).Error; err != nil {
		return err
	}
	for _, folder := range folders {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(folder).Error; err != nil {
			return err
		}
	}
	return nil
}
