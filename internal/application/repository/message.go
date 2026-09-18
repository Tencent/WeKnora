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
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(message).Error; err != nil {
			return err
		}
		return insertMessageArtifacts(tx, []*types.Message{message})
	})
	if err != nil {
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
	if err := attachArtifacts(ctx, r.db, &message); err != nil {
		return nil, err
	}
	return &message, nil
}

// GetMessagesBySession retrieves all messages for a session with pagination.
//
// The secondary sort on id is not cosmetic: messages created in the same
// millisecond would otherwise come back in an order the database is free to
// vary between calls, which makes a fork boundary drawn at one of them
// irreproducible.
func (r *messageRepository) GetMessagesBySession(
	ctx context.Context, sessionID string, page int, pageSize int,
) ([]*types.Message, error) {
	var messages []*types.Message
	if err := r.db.WithContext(ctx).Where("session_id = ?", sessionID).
		Order("created_at ASC, id ASC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&messages).Error; err != nil {
		return nil, err
	}
	if err := attachArtifacts(ctx, r.db, messages...); err != nil {
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
	if err := attachArtifacts(ctx, r.db, messages...); err != nil {
		return nil, err
	}
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
	if err := attachArtifacts(ctx, r.db, messages...); err != nil {
		return nil, err
	}
	return messages, nil
}

// ListMessagesBySessionAfterTime returns the oldest messages created after
// afterTime, so a caller holding a watermark can walk a session forward
// without skipping anything when it has more new messages than one page.
func (r *messageRepository) ListMessagesBySessionAfterTime(
	ctx context.Context, sessionID string, afterTime time.Time, limit int,
) ([]*types.Message, error) {
	var messages []*types.Message
	query := r.db.WithContext(ctx).Where("session_id = ?", sessionID)
	if !afterTime.IsZero() {
		query = query.Where("created_at > ?", afterTime)
	}
	if err := query.Order("created_at ASC").Limit(limit).Find(&messages).Error; err != nil {
		return nil, err
	}
	if err := attachArtifacts(ctx, r.db, messages...); err != nil {
		return nil, err
	}
	return messages, nil
}

// ListMessagesBySessionAfterCursor uses a stable tie-breaker for memory paging.
func (r *messageRepository) ListMessagesBySessionAfterCursor(ctx context.Context, sessionID string, cursor types.MemoryMessageCursor, limit int) ([]*types.Message, error) {
	var messages []*types.Message
	query := r.db.WithContext(ctx).Where("session_id = ?", sessionID)
	if !cursor.At.IsZero() || cursor.ID != "" {
		query = query.Where("created_at > ? OR (created_at = ? AND id > ?)", cursor.At, cursor.At, cursor.ID)
	}
	if err := query.Order("created_at ASC, id ASC").Limit(limit).Find(&messages).Error; err != nil {
		return nil, err
	}
	if err := attachArtifacts(ctx, r.db, messages...); err != nil {
		return nil, err
	}
	return messages, nil
}

// ListMessagesBySessionUpTo returns every message of a session that sorts
// strictly before the (boundary, boundaryID) cursor, oldest first.
//
// The cursor is composite rather than a bare timestamp because a fork boundary
// must be reproducible: two messages written in the same millisecond are
// ordered by ID, and the caller's boundary message must be excluded regardless
// of how many peers share its timestamp.
func (r *messageRepository) ListMessagesBySessionUpTo(
	ctx context.Context, sessionID string, boundary time.Time, boundaryID string,
) ([]*types.Message, error) {
	var messages []*types.Message
	if err := r.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Where("created_at < ? OR (created_at = ? AND id < ?)", boundary, boundary, boundaryID).
		Order("created_at ASC, id ASC").
		Find(&messages).Error; err != nil {
		return nil, err
	}
	if err := attachArtifacts(ctx, r.db, messages...); err != nil {
		return nil, err
	}
	return messages, nil
}

// UpdateMessage updates an existing message. Artifacts are rewritten only
// when message.Artifacts is non-nil (see writeMessageArtifacts).
func (r *messageRepository) UpdateMessage(ctx context.Context, message *types.Message) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&types.Message{}).Where(
			"id = ? AND session_id = ?", message.ID, message.SessionID,
		).Updates(message).Error; err != nil {
			return err
		}
		return writeMessageArtifacts(tx, message)
	})
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
	if err := attachArtifacts(ctx, r.db, &message); err != nil {
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
	if err := attachArtifacts(ctx, r.db, &message); err != nil {
		return nil, err
	}

	return &message, nil
}

// SearchMessagesByKeyword searches messages by keyword across sessions for a tenant
func (r *messageRepository) SearchMessagesByKeyword(
	ctx context.Context, tenantID uint64, ownerID, keyword string, sessionIDs []string, limit int,
) ([]*types.MessageWithSession, error) {
	if limit <= 0 {
		limit = 20
	}

	// ILIKE is Postgres-only; SQLite (Lite build) and MySQL lack the keyword,
	// so mirror the session-list search with LOWER() on both sides. ESCAPE ?
	// pairs with escapeLikeKeyword: SQLite has no default LIKE escape
	// character, so without it \% and \_ would never match a literal wildcard.
	contentLikeExpr := "LOWER(messages.content) LIKE LOWER(?) ESCAPE ?"
	if r.db.Name() == "postgres" {
		contentLikeExpr = "messages.content ILIKE ? ESCAPE ?"
	}

	var results []*types.MessageWithSession

	query := r.db.WithContext(ctx).
		Table("messages").
		Select("messages.*, sessions.title as session_title").
		Joins("INNER JOIN sessions ON sessions.id = messages.session_id AND sessions.deleted_at IS NULL").
		Where("sessions.tenant_id = ?", tenantID).
		Where("messages.deleted_at IS NULL").
		Where(contentLikeExpr, "%"+escapeLikeKeyword(keyword)+"%", likeEscapeChar)

	// Matches the scoping used when listing sessions, including the legacy
	// allowance for tenant-level sessions created before per-user ownership.
	if ownerID != "" {
		query = query.Where("(sessions.user_id = ? OR sessions.user_id IS NULL OR sessions.user_id = '')", ownerID)
	}

	if len(sessionIDs) > 0 {
		query = query.Where("messages.session_id IN ?", sessionIDs)
	}

	if err := query.Order("messages.created_at DESC").Limit(limit).Find(&results).Error; err != nil {
		return nil, err
	}
	if err := attachArtifactsWithSession(ctx, r.db, results); err != nil {
		return nil, err
	}

	return results, nil
}

// OwnedSessionIDs narrows a set of session ids to the ones this person owns.
//
// The vector path finds messages through a shared knowledge base that has no
// notion of who wrote them, so ownership has to be re-established here before
// anything is returned.
func (r *messageRepository) OwnedSessionIDs(
	ctx context.Context, tenantID uint64, ownerID string, sessionIDs []string,
) (map[string]bool, error) {
	owned := make(map[string]bool, len(sessionIDs))
	if len(sessionIDs) == 0 {
		return owned, nil
	}
	var ids []string
	query := r.db.WithContext(ctx).
		Table("sessions").
		Select("id").
		Where("tenant_id = ?", tenantID).
		Where("deleted_at IS NULL").
		Where("id IN ?", sessionIDs)
	if ownerID != "" {
		query = query.Where("(user_id = ? OR user_id IS NULL OR user_id = '')", ownerID)
	}
	if err := query.Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	for _, id := range ids {
		owned[id] = true
	}
	return owned, nil
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
	if err := attachArtifactsWithSession(ctx, r.db, results); err != nil {
		return nil, err
	}
	return results, nil
}

// GetMessagesByRequestIDs retrieves messages by request ID inside one session
// (used to fetch Q&A pair partners). Empty sessionID is treated as no match.
func (r *messageRepository) GetMessagesByRequestIDs(
	ctx context.Context, sessionID string, requestIDs []string,
) ([]*types.MessageWithSession, error) {
	if sessionID == "" || len(requestIDs) == 0 {
		return nil, nil
	}
	var results []*types.MessageWithSession
	if err := r.db.WithContext(ctx).
		Table("messages").
		Select("messages.*, sessions.title as session_title").
		Joins("INNER JOIN sessions ON sessions.id = messages.session_id AND sessions.deleted_at IS NULL").
		Where("messages.deleted_at IS NULL").
		Where("messages.session_id = ?", sessionID).
		Where("messages.request_id IN ?", requestIDs).
		Find(&results).Error; err != nil {
		return nil, err
	}
	if err := attachArtifactsWithSession(ctx, r.db, results); err != nil {
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

// RewriteSandboxCheckpoints retargets copied checkpoints onto the forked
// session's live sandbox. CommitSHA / CommittedAt are left untouched: the git
// objects are in the snapshot, only the sandbox identity changed.
func (r *messageRepository) RewriteSandboxCheckpoints(
	ctx context.Context, sessionID, oldSandboxID, newSandboxID string,
) error {
	if sessionID == "" || oldSandboxID == "" || newSandboxID == "" || oldSandboxID == newSandboxID {
		return nil
	}
	var messages []*types.Message
	if err := r.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Find(&messages).Error; err != nil {
		return err
	}
	for _, message := range messages {
		if message == nil || message.SandboxCheckpoint == nil {
			continue
		}
		if message.SandboxCheckpoint.SandboxID != oldSandboxID {
			continue
		}
		updated := *message.SandboxCheckpoint
		updated.SandboxID = newSandboxID
		if err := r.db.WithContext(ctx).
			Model(&types.Message{}).
			Where("id = ? AND session_id = ?", message.ID, sessionID).
			Update("sandbox_checkpoint", updated).Error; err != nil {
			return err
		}
	}
	return nil
}

// GetSessionAttachments returns every user-uploaded attachment in creation
// order while projecting only the attachments JSON column.
func (r *messageRepository) GetSessionAttachments(
	ctx context.Context, sessionID string,
) (types.MessageAttachments, error) {
	if sessionID == "" {
		return nil, nil
	}
	var rows []struct {
		Attachments types.MessageAttachments `gorm:"column:attachments"`
		CreatedAt   time.Time                `gorm:"column:created_at"`
	}
	if err := r.db.WithContext(ctx).
		Model(&types.Message{}).
		Select("attachments", "created_at").
		Where("session_id = ? AND deleted_at IS NULL", sessionID).
		Order("created_at ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(types.MessageAttachments, 0, len(rows))
	for _, row := range rows {
		result = append(result, row.Attachments...)
	}
	return result, nil
}
