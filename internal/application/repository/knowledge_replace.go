package repository

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// ReplaceKnowledge keeps the previous row live if inserting its replacement
// fails. No file, index, queue or other remote operation runs in this transaction.
func (r *knowledgeRepository) ReplaceKnowledge(
	ctx context.Context, before, after *types.Knowledge, tagIDs []string,
) error {
	if before == nil || after == nil || before.ID == "" || after.ID == "" || before.ID == after.ID ||
		before.TenantID != after.TenantID || before.KnowledgeBaseID != after.KnowledgeBaseID {
		return fmt.Errorf("invalid knowledge replacement")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The primary-key lookup locks only the source row. Comparing the snapshot
		// prevents a second sync from replacing a row another writer has retired
		// or changed while this sync was preparing its replacement.
		result := tx.Where("id = ? AND tenant_id = ? AND knowledge_base_id = ? AND updated_at = ?",
			before.ID, before.TenantID, before.KnowledgeBaseID, before.UpdatedAt).
			Delete(&types.Knowledge{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("knowledge changed during replacement: %s", before.ID)
		}
		repo := &knowledgeRepository{db: tx}
		if err := repo.CreateKnowledge(ctx, after); err != nil {
			return err
		}
		if len(tagIDs) > 0 {
			return repo.SetKnowledgeTags(ctx, after.ID, tagIDs)
		}
		return nil
	})
}
