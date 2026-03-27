package repository

import (
	"context"
	"slices"
	"time"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// messageRepository implements the message repository interface
type messageRepository struct {
	db *gorm.DB
}

// NewMessageRepository creates a new message repository
func NewMessageRepository(db *gorm.DB) interfaces.MessageRepository {
	return &messageRepository{
		db: db,
	}
}

// CreateMessage creates a new message
func (r *messageRepository) CreateMessage(
	ctx context.Context, message *types.Message,
) (*types.Message, error) {
	if err := r.db.WithContext(ctx).Create(message).Error; err != nil {
		return nil, err
	}
	return message, nil
}

// GetMessage retrieves a message
func (r *messageRepository) GetMessage(
	ctx context.Context, sessionID string, messageID string,
) (*types.Message, error) {
	var message types.Message
	if err := r.db.WithContext(ctx).Where(
		"id = ? AND session_id = ?", messageID, sessionID,
	).First(&message).Error; err != nil {
		return nil, err
	}
	return &message, nil
}

// GetMessagesBySession retrieves all messages for a session with pagination
func (r *messageRepository) GetMessagesBySession(
	ctx context.Context, sessionID string, page int, pageSize int,
) ([]*types.Message, error) {
	var messages []*types.Message
	if err := r.db.WithContext(ctx).Where("session_id = ?", sessionID).Order("created_at ASC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&messages).Error; err != nil {
		return nil, err
	}
	return messages, nil
}

// GetRecentMessagesBySession retrieves recent messages for a session
func (r *messageRepository) GetRecentMessagesBySession(
	ctx context.Context, sessionID string, limit int,
) ([]*types.Message, error) {
	var messages []*types.Message
	if err := r.db.WithContext(ctx).Where(
		"session_id = ?", sessionID,
	).Order("created_at DESC").Limit(limit).Find(&messages).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	slices.SortFunc(messages, func(a, b *types.Message) int {
		cmp := a.CreatedAt.Compare(b.CreatedAt)
		if cmp == 0 {
			if a.Role == "user" { // User messages come first
				return -1
			}
			return 1 // Assistant messages come last
		}
		return cmp
	})
	return messages, nil
}

// GetMessagesBySessionBeforeTime retrieves messages from a session created before a specific time
func (r *messageRepository) GetMessagesBySessionBeforeTime(
	ctx context.Context, sessionID string, beforeTime time.Time, limit int,
) ([]*types.Message, error) {
	var messages []*types.Message
	if err := r.db.WithContext(ctx).Where(
		"session_id = ? AND created_at < ?", sessionID, beforeTime,
	).Order("created_at DESC").Limit(limit).Find(&messages).Error; err != nil {
		return nil, err
	}
	slices.SortFunc(messages, func(a, b *types.Message) int {
		cmp := a.CreatedAt.Compare(b.CreatedAt)
		if cmp == 0 {
			if a.Role == "user" { // User messages come first
				return -1
			}
			return 1 // Assistant messages come last
		}
		return cmp
	})
	return messages, nil
}

// UpdateMessage updates an existing message
func (r *messageRepository) UpdateMessage(ctx context.Context, message *types.Message) error {
	return r.db.WithContext(ctx).Model(&types.Message{}).Where(
		"id = ? AND session_id = ?", message.ID, message.SessionID,
	).Updates(message).Error
}

// DeleteMessage deletes a message
func (r *messageRepository) DeleteMessage(ctx context.Context, sessionID string, messageID string) error {
	return r.db.WithContext(ctx).Where(
		"id = ? AND session_id = ?", messageID, sessionID,
	).Delete(&types.Message{}).Error
}

// GetFirstMessageOfUser retrieves the first message from a user in a session
func (r *messageRepository) GetFirstMessageOfUser(ctx context.Context, sessionID string) (*types.Message, error) {
	var message types.Message
	if err := r.db.WithContext(ctx).Where(
		"session_id = ? and role = ?", sessionID, "user",
	).Order("created_at ASC").First(&message).Error; err != nil {
		return nil, err
	}
	return &message, nil
}

// GetMessageByRequestID retrieves a message by request ID
func (r *messageRepository) GetMessageByRequestID(
	ctx context.Context, sessionID string, requestID string,
) (*types.Message, error) {
	var message types.Message

	result := r.db.WithContext(ctx).
		Where("session_id = ? AND request_id = ?", sessionID, requestID).
		First(&message)

	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, result.Error
	}

	return &message, nil
}

// SearchMessagesByKeyword searches messages by keyword (ILIKE) across sessions for a tenant
func (r *messageRepository) SearchMessagesByKeyword(
	ctx context.Context, tenantID uint64, keyword string, sessionIDs []string, limit int,
) ([]*types.MessageWithSession, error) {
	if limit <= 0 {
		limit = 20
	}

	var results []*types.MessageWithSession

	query := r.db.WithContext(ctx).
		Table("messages").
		Select("messages.*, sessions.title as session_title").
		Joins("INNER JOIN sessions ON sessions.id = messages.session_id AND sessions.deleted_at IS NULL").
		Where("sessions.tenant_id = ?", tenantID).
		Where("messages.deleted_at IS NULL").
		Where("messages.content ILIKE ?", "%"+escapeLikeKeyword(keyword)+"%")

	if len(sessionIDs) > 0 {
		query = query.Where("messages.session_id IN ?", sessionIDs)
	}

	if err := query.Order("messages.created_at DESC").Limit(limit).Find(&results).Error; err != nil {
		return nil, err
	}

	return results, nil
}

// GetMessagesByKnowledgeIDs retrieves messages by their associated Knowledge IDs
func (r *messageRepository) GetMessagesByKnowledgeIDs(
	ctx context.Context, knowledgeIDs []string,
) ([]*types.MessageWithSession, error) {
	if len(knowledgeIDs) == 0 {
		return nil, nil
	}
	var results []*types.MessageWithSession
	if err := r.db.WithContext(ctx).
		Table("messages").
		Select("messages.*, sessions.title as session_title").
		Joins("INNER JOIN sessions ON sessions.id = messages.session_id AND sessions.deleted_at IS NULL").
		Where("messages.deleted_at IS NULL").
		Where("messages.knowledge_id IN ?", knowledgeIDs).
		Find(&results).Error; err != nil {
		return nil, err
	}
	return results, nil
}

// GetMessagesByRequestIDs retrieves messages by their request IDs (used to fetch Q&A pair partners)
func (r *messageRepository) GetMessagesByRequestIDs(
	ctx context.Context, requestIDs []string,
) ([]*types.MessageWithSession, error) {
	if len(requestIDs) == 0 {
		return nil, nil
	}
	var results []*types.MessageWithSession
	if err := r.db.WithContext(ctx).
		Table("messages").
		Select("messages.*, sessions.title as session_title").
		Joins("INNER JOIN sessions ON sessions.id = messages.session_id AND sessions.deleted_at IS NULL").
		Where("messages.deleted_at IS NULL").
		Where("messages.request_id IN ?", requestIDs).
		Find(&results).Error; err != nil {
		return nil, err
	}
	return results, nil
}

// GetKnowledgeIDsBySessionID retrieves all knowledge IDs for messages in a session
func (r *messageRepository) GetKnowledgeIDsBySessionID(
	ctx context.Context, sessionID string,
) ([]string, error) {
	var knowledgeIDs []string
	if err := r.db.WithContext(ctx).
		Model(&types.Message{}).
		Where("session_id = ? AND knowledge_id != '' AND knowledge_id IS NOT NULL AND deleted_at IS NULL", sessionID).
		Pluck("knowledge_id", &knowledgeIDs).Error; err != nil {
		return nil, err
	}
	return knowledgeIDs, nil
}

// UpdateMessageImages updates only the images JSONB column for a message.
// Uses Select to force GORM to include the column even when struct-based
// Updates would otherwise skip custom Valuer types.
func (r *messageRepository) UpdateMessageImages(ctx context.Context, sessionID, messageID string, images types.MessageImages) error {
	return r.db.WithContext(ctx).
		Model(&types.Message{}).
		Where("id = ? AND session_id = ?", messageID, sessionID).
		Update("images", images).Error
}

// UpdateMessageRenderedContent updates only the rendered_content column for a message.
func (r *messageRepository) UpdateMessageRenderedContent(ctx context.Context, sessionID, messageID string, renderedContent string) error {
	return r.db.WithContext(ctx).
		Model(&types.Message{}).
		Where("id = ? AND session_id = ?", messageID, sessionID).
		Update("rendered_content", renderedContent).Error
}

// DeleteMessagesBySessionID deletes all messages belonging to a session (soft delete)
func (r *messageRepository) DeleteMessagesBySessionID(ctx context.Context, sessionID string) error {
	return r.db.WithContext(ctx).Where("session_id = ?", sessionID).Delete(&types.Message{}).Error
}

// UpdateMessageKnowledgeID updates the knowledge_id field for a message
func (r *messageRepository) UpdateMessageKnowledgeID(
	ctx context.Context, messageID string, knowledgeID string,
) error {
	return r.db.WithContext(ctx).
		Model(&types.Message{}).
		Where("id = ?", messageID).
		Update("knowledge_id", knowledgeID).Error
}
