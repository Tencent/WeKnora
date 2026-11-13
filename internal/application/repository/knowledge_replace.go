package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockKnowledgeTreeKB(tx, before.TenantID, before.KnowledgeBaseID); err != nil {
			return err
		}
		var current types.Knowledge
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"id = ? AND tenant_id = ? AND knowledge_base_id = ? AND updated_at = ? AND NOT deletion_pending",
			before.ID, before.TenantID, before.KnowledgeBaseID, before.UpdatedAt,
		).First(&current).Error; err != nil {
			return fmt.Errorf("knowledge changed during replacement: %w", err)
		}
		if current.ParseStatus == types.ParseStatusDeleting {
			return fmt.Errorf("knowledge deletion is in progress")
		}
		if err := access.RejectMovingKnowledge(&current); err != nil {
			return err
		}
		var children []*types.Knowledge
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("parent_knowledge_id = ?", before.ID).Find(&children).Error; err != nil {
			return err
		}
		var childIDs []string
		for _, child := range children {
			if child.TenantID != before.TenantID || child.KnowledgeBaseID != before.KnowledgeBaseID {
				return fmt.Errorf("knowledge parent scope changed")
			}
			if err := access.RejectMovingKnowledge(child); err != nil {
				return err
			}
			childIDs = append(childIDs, child.ID)
		}
		after.ParentKnowledgeID = current.ParentKnowledgeID
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
		if len(childIDs) > 0 {
			if err := tx.Model(&types.Knowledge{}).Where(
				"id IN ? AND tenant_id = ? AND knowledge_base_id = ?",
				childIDs, before.TenantID, before.KnowledgeBaseID,
			).Updates(map[string]any{
				"parent_knowledge_id": after.ID, "updated_at": time.Now().UTC(),
			}).Error; err != nil {
				return err
			}
		}
		if len(tagIDs) > 0 {
			return repo.SetKnowledgeTags(ctx, after.ID, tagIDs)
		}
		return nil
	})
	return nativeKnowledgeError(err)
}
