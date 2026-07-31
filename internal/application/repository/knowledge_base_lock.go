package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// lockKnowledgeBaseForUpdate is the shared serialization point for folder-tree
// mutations and document creation into a folder. Locking a stable parent row
// avoids races on rows that may not exist yet (for example a new child folder).
func lockKnowledgeBaseForUpdate(
	ctx context.Context,
	tx *gorm.DB,
	kbID string,
) (*types.KnowledgeBase, error) {
	query := tx.WithContext(ctx)
	if query.Dialector.Name() == "sqlite" {
		// SQLite has no SELECT FOR UPDATE. A no-op write acquires its database
		// write lock before validation reads, providing the same fail-closed
		// serialization contract.
		result := query.Exec("UPDATE knowledge_bases SET id = id WHERE id = ?", kbID)
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected == 0 {
			return nil, ErrKnowledgeBaseNotFound
		}
	} else {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}

	var knowledgeBase types.KnowledgeBase
	if err := query.
		Select("id", "tenant_id").
		Where("id = ?", kbID).
		Take(&knowledgeBase).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrKnowledgeBaseNotFound
		}
		return nil, err
	}
	return &knowledgeBase, nil
}
