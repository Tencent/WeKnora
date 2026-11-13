package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/application/access"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UpdateKnowledgeForTransfer is a compare-and-swap checkpoint. Full-row Save
// could resurrect a deleted document or overwrite a concurrently moved row.
// Accounting changes are committed with the checkpoint, making retry cleanup
// safe even if the worker crashes between the database and queue operations.
func (r *knowledgeRepository) UpdateKnowledgeForTransfer(ctx context.Context, before, after *types.Knowledge) error {
	if before == nil || after == nil || before.ID == "" || before.ID != after.ID || before.TenantID == 0 ||
		before.TenantID != after.TenantID {
		return fmt.Errorf("invalid knowledge transfer checkpoint")
	}
	// PostgreSQL persists timestamps at microsecond precision. Keep the returned
	// checkpoint identical to storage so the next CAS in this task can match it.
	after.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)
	var storedUpdatedAt time.Time
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Claim moving under the same KB lock as native child creation, before
		// any vector/file/chunk side effects. A prior admission check can race.
		if moveErr := access.RejectMovingKnowledge(after); moveErr != nil {
			var appErr *apperrors.AppError
			if !errors.As(moveErr, &appErr) || appErr.HTTPCode != 409 {
				return moveErr
			}
			if err := lockKnowledgeTreeKB(tx, before.TenantID, before.KnowledgeBaseID); err != nil {
				return err
			}
			var current types.Knowledge
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
				"id = ? AND tenant_id = ? AND knowledge_base_id = ?",
				before.ID, before.TenantID, before.KnowledgeBaseID,
			).First(&current).Error; err != nil {
				return err
			}
			// An empty target rejects links in every KB when claiming a move.
			treeRepo := &knowledgeRepository{db: tx}
			if err := treeRepo.CheckKnowledgeTreeMove(ctx, before.TenantID, before.ID, ""); err != nil {
				return err
			}
		}
		result := tx.Model(&types.Knowledge{}).
			Where("id = ? AND tenant_id = ? AND knowledge_base_id = ? AND parse_status = ? AND "+
				"updated_at = ?",
				before.ID,
				before.TenantID,
				before.KnowledgeBaseID,
				before.ParseStatus,
				before.UpdatedAt).
			Updates(map[string]any{
				"knowledge_base_id": after.KnowledgeBaseID, "parse_status": after.ParseStatus,
				"metadata": after.Metadata, "storage_size": after.StorageSize, "updated_at": after.UpdatedAt,
				"enable_status": after.EnableStatus, "embedding_model_id": after.EmbeddingModelID,
				"description":   after.Description,
				"processed_at":  after.ProcessedAt,
				"error_message": after.ErrorMessage,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("knowledge changed during transfer")
		}
		delta := after.StorageSize - before.StorageSize
		if delta != 0 {
			result := tx.Model(&types.Tenant{}).Where("id = ?", before.TenantID).
				UpdateColumn("storage_used",
					gorm.Expr("CASE WHEN storage_used + ? < 0 THEN 0 ELSE storage_used + ? END",
						delta,
						delta))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("transfer tenant not found")
			}
		}
		var stored struct{ UpdatedAt time.Time }
		if err := tx.Model(&types.Knowledge{}).Select("updated_at").
			Where("id = ? AND tenant_id = ?", after.ID, after.TenantID).Take(&stored).Error; err != nil {
			return err
		}
		storedUpdatedAt = stored.UpdatedAt
		return nil
	})
	if err == nil {
		after.UpdatedAt = storedUpdatedAt
	}
	return nativeKnowledgeError(err)
}
