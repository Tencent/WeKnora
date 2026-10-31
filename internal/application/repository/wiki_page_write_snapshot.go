package repository

import (
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// checkWikiPageWriteSnapshot 在短事务内锁定当前行，再检查模型实际读取的快照。
// PostgreSQL 的行锁覆盖检查到写入的间隙；SQLite 保留同一事务的读取快照。
func checkWikiPageWriteSnapshot(tx *gorm.DB, page *types.WikiPage) error {
	expected, guarded := types.WikiPageWriteSnapshotFromContext(tx.Statement.Context)
	if !guarded {
		return nil
	}
	if page == nil || page.ID != expected.ID || page.TenantID != expected.TenantID ||
		page.KnowledgeBaseID != expected.KnowledgeBaseID || page.Slug != expected.Slug ||
		page.Version != expected.Version {
		return ErrWikiPageConflict
	}
	var current types.WikiPage
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND tenant_id = ? AND knowledge_base_id = ? AND slug = ?",
			expected.ID, expected.TenantID, expected.KnowledgeBaseID, expected.Slug).
		First(&current).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrWikiPageNotFound
	}
	if err != nil {
		return err
	}
	if !types.WikiPageWriteSnapshotMatches(expected, &current) {
		return ErrWikiPageConflict
	}
	return nil
}
