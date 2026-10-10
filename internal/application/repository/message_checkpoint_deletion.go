package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DeleteMessage invalidates derived history in the same transaction as the
// source deletion. A failed invalidation must not acknowledge the deletion.
func (r *messageRepository) DeleteMessage(ctx context.Context, sessionID, messageID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var message types.Message
		err := tx.Where("id = ? AND session_id = ?", messageID, sessionID).First(&message).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := invalidateDependentCheckpoints(tx, &message); err != nil {
			return err
		}
		return tx.Delete(&message).Error
	})
}

func invalidateDependentCheckpoints(tx *gorm.DB, message *types.Message) error {
	boundary := *message
	if message.RequestID != "" {
		// A steered user message can be newer than its assistant row. The
		// checkpoint on that row still covers it, so invalidate from the turn.
		boundary = types.Message{}
		if err := tx.Where("session_id = ? AND request_id = ?", message.SessionID, message.RequestID).
			Order("created_at ASC, id ASC").First(&boundary).Error; err != nil {
			return err
		}
	}
	// Include rows without a checkpoint: updating them also serializes against
	// a first checkpoint being saved by an in-flight compactor.
	return tx.Model(&types.Message{}).
		Where("session_id = ? AND role = 'assistant'", message.SessionID).
		Where("created_at > ? OR (created_at = ? AND id >= ?)", boundary.CreatedAt, boundary.CreatedAt, boundary.ID).
		UpdateColumn("context_checkpoint", nil).Error
}

func (r *messageRepository) CountDeletedMessagesBySession(ctx context.Context, sessionID string) (int64, error) {
	return countDeletedMessages(r.db.WithContext(ctx), sessionID)
}

func countDeletedMessages(db *gorm.DB, sessionID string) (int64, error) {
	var count int64
	err := db.Unscoped().Model(&types.Message{}).
		Where("session_id = ? AND deleted_at IS NOT NULL", sessionID).Count(&count).Error
	return count, err
}

// UpdateMessageContextCheckpoint fences a late summary against deletion.
// Lock before checking tombstones: either the write commits first and deletion
// clears it, or deletion commits first and the old snapshot is rejected.
func (r *messageRepository) UpdateMessageContextCheckpoint(
	ctx context.Context, sessionID, messageID string, checkpoint *types.ContextCheckpoint,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var target types.Message
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").
			Where("id = ? AND session_id = ? AND role = 'assistant'", messageID, sessionID).
			First(&target).Error; err != nil {
			return err
		}
		if checkpoint != nil {
			count, err := countDeletedMessages(tx, sessionID)
			if err != nil {
				return err
			}
			if count != checkpoint.SourceDeletedCount {
				return types.ErrStaleContextCheckpoint
			}
		}
		return tx.Model(&target).UpdateColumn("context_checkpoint", checkpoint).Error
	})
}
