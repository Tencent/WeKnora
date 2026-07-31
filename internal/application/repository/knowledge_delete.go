package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var _ interfaces.KnowledgeDeleteCoordinatorRepository = (*knowledgeRepository)(nil)

// BeginKnowledgeDelete atomically marks active knowledge as deleting and
// removes stale folder-index work.
func (r *knowledgeRepository) BeginKnowledgeDelete(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID string,
	knowledgeID string,
) (*types.Knowledge, error) {
	if tenantID == 0 || knowledgeBaseID == "" {
		return nil, ErrKnowledgeBaseNotFound
	}
	if knowledgeID == "" {
		return nil, ErrKnowledgeNotFound
	}

	var db *gorm.DB
	if r != nil {
		db = r.db
	}
	updatedAt := time.Now().UTC()
	var snapshot *types.Knowledge
	err := runKnowledgeBaseScopedWriteTransaction(
		ctx,
		db,
		tenantID,
		knowledgeBaseID,
		knowledgeBaseScopedWriteOptions{
			lockMode: knowledgeBaseLockIncludeSoftDeleted,
		},
		func(tx *gorm.DB) error {
			return beginKnowledgeDeleteAttempt(
				ctx,
				tx,
				tenantID,
				knowledgeBaseID,
				knowledgeID,
				updatedAt,
				&snapshot,
			)
		},
	)
	if err != nil {
		return nil, mapKnowledgeDeleteCoordinatorError(err)
	}
	if snapshot == nil {
		return nil, ErrKnowledgeNotFound
	}
	return snapshot, nil
}

func beginKnowledgeDeleteAttempt(
	ctx context.Context,
	tx *gorm.DB,
	tenantID uint64,
	knowledgeBaseID string,
	knowledgeID string,
	updatedAt time.Time,
	snapshot **types.Knowledge,
) error {
	*snapshot = nil
	knowledge, err := lockActiveKnowledgeForDelete(
		ctx,
		tx,
		tenantID,
		knowledgeBaseID,
		knowledgeID,
		"",
	)
	if err != nil {
		return err
	}
	*snapshot = knowledge

	if knowledge.ParseStatus != types.ParseStatusDeleting {
		result := tx.WithContext(ctx).
			Model(&types.Knowledge{}).
			Where(
				"tenant_id = ? AND knowledge_base_id = ? AND id = ?",
				tenantID,
				knowledgeBaseID,
				knowledgeID,
			).
			Where("parse_status = ?", knowledge.ParseStatus).
			Updates(map[string]interface{}{
				"parse_status": types.ParseStatusDeleting,
				"updated_at":   updatedAt,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrKnowledgeNotFound
		}
	}

	return hardDeleteKnowledgeFolderIndexPending(
		ctx,
		tx,
		tenantID,
		knowledgeBaseID,
		knowledgeID,
	)
}

// FinalizeKnowledgeDelete soft-deletes one active deleting row after cleanup.
func (r *knowledgeRepository) FinalizeKnowledgeDelete(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID string,
	knowledgeID string,
) error {
	if tenantID == 0 || knowledgeBaseID == "" {
		return ErrKnowledgeBaseNotFound
	}
	if knowledgeID == "" {
		return ErrKnowledgeNotFound
	}

	var db *gorm.DB
	if r != nil {
		db = r.db
	}
	err := runKnowledgeBaseScopedWriteTransaction(
		ctx,
		db,
		tenantID,
		knowledgeBaseID,
		knowledgeBaseScopedWriteOptions{
			lockMode: knowledgeBaseLockIncludeSoftDeleted,
		},
		func(tx *gorm.DB) error {
			if _, err := lockActiveKnowledgeForDelete(
				ctx,
				tx,
				tenantID,
				knowledgeBaseID,
				knowledgeID,
				types.ParseStatusDeleting,
			); err != nil {
				return err
			}
			if err := hardDeleteKnowledgeFolderIndexPending(
				ctx,
				tx,
				tenantID,
				knowledgeBaseID,
				knowledgeID,
			); err != nil {
				return err
			}

			result := tx.WithContext(ctx).
				Where(
					"tenant_id = ? AND knowledge_base_id = ? AND id = ?",
					tenantID,
					knowledgeBaseID,
					knowledgeID,
				).
				Where("parse_status = ?", types.ParseStatusDeleting).
				Delete(&types.Knowledge{})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrKnowledgeNotFound
			}
			return nil
		},
	)
	return mapKnowledgeDeleteCoordinatorError(err)
}

func lockActiveKnowledgeForDelete(
	ctx context.Context,
	tx *gorm.DB,
	tenantID uint64,
	knowledgeBaseID string,
	knowledgeID string,
	requiredStatus string,
) (*types.Knowledge, error) {
	var knowledge types.Knowledge
	query := tx.WithContext(ctx).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND id = ?",
			tenantID,
			knowledgeBaseID,
			knowledgeID,
		)
	if requiredStatus != "" {
		query = query.Where("parse_status = ?", requiredStatus)
	}
	if tx.Dialector != nil && tx.Dialector.Name() == "postgres" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := query.Take(&knowledge).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrKnowledgeNotFound
	}
	if err != nil {
		return nil, err
	}
	return &knowledge, nil
}

func hardDeleteKnowledgeFolderIndexPending(
	ctx context.Context,
	tx *gorm.DB,
	tenantID uint64,
	knowledgeBaseID string,
	knowledgeID string,
) error {
	return tx.WithContext(ctx).
		Unscoped().
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND knowledge_id = ?",
			tenantID,
			knowledgeBaseID,
			knowledgeID,
		).
		Delete(&types.KnowledgeFolderIndexPending{}).Error
}

func mapKnowledgeDeleteCoordinatorError(err error) error {
	if errors.Is(err, ErrKnowledgeFolderKnowledgeBaseNotFound) {
		return ErrKnowledgeBaseNotFound
	}
	return err
}
