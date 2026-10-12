package repository

import (
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// invalidateDependentCheckpoints clears summaries covering the first deleted
// turn. The caller's transaction also deletes the rewound messages.
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
	return tx.Model(&types.Message{}).
		Where("session_id = ? AND role = 'assistant' AND context_checkpoint IS NOT NULL", message.SessionID).
		Where("created_at > ? OR (created_at = ? AND id >= ?)", boundary.CreatedAt, boundary.CreatedAt, boundary.ID).
		UpdateColumn("context_checkpoint", nil).Error
}
