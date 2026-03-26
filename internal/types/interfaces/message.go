package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// MessageService defines the message service interface
type MessageService interface {
	// CreateMessage creates a message
	CreateMessage(ctx context.Context, message *types.Message) (*types.Message, error)

	// GetMessage gets a message
	GetMessage(ctx context.Context, sessionID string, id string) (*types.Message, error)

	// GetMessagesBySession gets all messages of a session
	GetMessagesBySession(ctx context.Context, sessionID string, page int, pageSize int) ([]*types.Message, error)

	// GetRecentMessagesBySession gets recent messages of a session
	GetRecentMessagesBySession(ctx context.Context, sessionID string, limit int) ([]*types.Message, error)

	// GetMessagesBySessionBeforeTime gets messages before a specific time of a session
	GetMessagesBySessionBeforeTime(
		ctx context.Context, sessionID string, beforeTime time.Time, limit int,
	) ([]*types.Message, error)

	// UpdateMessage updates a message
	UpdateMessage(ctx context.Context, message *types.Message) error

	// UpdateMessageImages updates only the images JSONB column for a message.
	UpdateMessageImages(ctx context.Context, sessionID, messageID string, images types.MessageImages) error

	// UpdateMessageRenderedContent updates the rendered_content column for a user message.
	UpdateMessageRenderedContent(ctx context.Context, sessionID, messageID string, renderedContent string) error

	// DeleteMessage deletes a message
	DeleteMessage(ctx context.Context, sessionID string, id string) error

	// ClearSessionMessages deletes all messages in a session, along with their chat history KB entries
	ClearSessionMessages(ctx context.Context, sessionID string) error

	// SearchMessages searches messages by keyword and/or vector similarity across all sessions of the current tenant.
	// Uses the chat history knowledge base for vector search instead of in-memory computation.
	SearchMessages(ctx context.Context, params *types.MessageSearchParams) (*types.MessageSearchResult, error)

	// IndexMessageToKB indexes a message (Q&A pair) into the chat history knowledge base asynchronously.
	// Called after assistant message is created to enable future vector search.
	IndexMessageToKB(ctx context.Context, userQuery string, assistantAnswer string, messageID string, sessionID string)

	// DeleteMessageKnowledge deletes the Knowledge entry associated with a message from the chat history KB.
	DeleteMessageKnowledge(ctx context.Context, knowledgeID string)

	// DeleteSessionKnowledge deletes all Knowledge entries for messages in a session from the chat history KB.
	DeleteSessionKnowledge(ctx context.Context, sessionID string)

	// GetChatHistoryKBStats returns statistics about the chat history knowledge base (indexed message count, etc.)
	GetChatHistoryKBStats(ctx context.Context) (*types.ChatHistoryKBStats, error)
}

// MessageRepository defines the message repository interface
type MessageRepository interface {
	// CreateMessage creates a message
	CreateMessage(ctx context.Context, message *types.Message) (*types.Message, error)
	// GetMessage gets a message
	GetMessage(ctx context.Context, sessionID string, id string) (*types.Message, error)
	// GetMessagesBySession gets all messages of a session
	GetMessagesBySession(ctx context.Context, sessionID string, page int, pageSize int) ([]*types.Message, error)
	// GetRecentMessagesBySession gets recent messages of a session
	GetRecentMessagesBySession(ctx context.Context, sessionID string, limit int) ([]*types.Message, error)
	// GetMessagesBySessionBeforeTime gets messages before a specific time of a session
	GetMessagesBySessionBeforeTime(
		ctx context.Context, sessionID string, beforeTime time.Time, limit int,
	) ([]*types.Message, error)
	// UpdateMessage updates a message
	UpdateMessage(ctx context.Context, message *types.Message) error
	// UpdateMessageImages updates only the images JSONB column for a message
	UpdateMessageImages(ctx context.Context, sessionID, messageID string, images types.MessageImages) error
	// UpdateMessageRenderedContent updates the rendered_content column for a user message
	UpdateMessageRenderedContent(ctx context.Context, sessionID, messageID string, renderedContent string) error
	// DeleteMessage deletes a message
	DeleteMessage(ctx context.Context, sessionID string, id string) error
	// DeleteMessagesBySessionID deletes all messages belonging to a session
	DeleteMessagesBySessionID(ctx context.Context, sessionID string) error
	// GetFirstMessageOfUser gets the first message of a user
	GetFirstMessageOfUser(ctx context.Context, sessionID string) (*types.Message, error)
	// SearchMessagesByKeyword searches messages by keyword (ILIKE) across sessions for a tenant
	SearchMessagesByKeyword(ctx context.Context, tenantID uint64, keyword string, sessionIDs []string, limit int) ([]*types.MessageWithSession, error)
	// GetMessagesByKnowledgeIDs retrieves messages by their associated Knowledge IDs
	GetMessagesByKnowledgeIDs(ctx context.Context, knowledgeIDs []string) ([]*types.MessageWithSession, error)
	// GetMessagesByRequestIDs retrieves messages by their request IDs (used to fetch Q&A pair partners)
	GetMessagesByRequestIDs(ctx context.Context, requestIDs []string) ([]*types.MessageWithSession, error)
	// GetKnowledgeIDsBySessionID retrieves all knowledge IDs for messages in a session
	GetKnowledgeIDsBySessionID(ctx context.Context, sessionID string) ([]string, error)
	// UpdateMessageKnowledgeID updates the knowledge_id field for a message
	UpdateMessageKnowledgeID(ctx context.Context, messageID string, knowledgeID string) error
}
