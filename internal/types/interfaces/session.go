package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
)

// SessionService defines the session service interface
type SessionService interface {
	// CreateSession creates a session
	CreateSession(ctx context.Context, session *types.Session) (*types.Session, error)
	// GetSession gets a session
	GetSession(ctx context.Context, id string) (*types.Session, error)
	// GetSessionsByTenant gets all sessions of a tenant
	GetSessionsByTenant(ctx context.Context) ([]*types.Session, error)
	// GetPagedSessionsByTenant gets paged sessions of a tenant
	GetPagedSessionsByTenant(ctx context.Context, page *types.Pagination) (*types.PageResult, error)
	// UpdateSession updates a session
	UpdateSession(ctx context.Context, session *types.Session) error
	// DeleteSession deletes a session
	DeleteSession(ctx context.Context, id string) error
	// GenerateTitle generates a title for the current conversation
	// modelID: optional model ID to use for title generation (if empty, uses first available KnowledgeQA model)
	GenerateTitle(ctx context.Context, session *types.Session, messages []types.Message, modelID string) (string, error)
	// GenerateTitleAsync generates a title for the session asynchronously
	// It emits an event when the title is generated
	// modelID: optional model ID to use for title generation (if empty, uses first available KnowledgeQA model)
	GenerateTitleAsync(ctx context.Context, session *types.Session, userQuery string, modelID string, eventBus *event.EventBus)
	// KnowledgeQA performs knowledge-based question answering
	// knowledgeBaseIDs: list of knowledge base IDs to search (supports multi-KB)
	// knowledgeIDs: list of specific knowledge (file) IDs to search
	// summaryModelID: optional summary model ID override (if empty, uses session/KB default)
	// webSearchEnabled: whether to enable web search to supplement knowledge base results
	// customAgent: optional custom agent for config override (multiTurnEnabled, historyTurns)
	// Events are emitted through eventBus (references, answer chunks, completion)
	KnowledgeQA(ctx context.Context,
		session *types.Session, query string, knowledgeBaseIDs []string, knowledgeIDs []string,
		assistantMessageID string, summaryModelID string, webSearchEnabled bool, eventBus *event.EventBus,
		customAgent *types.CustomAgent,
	) error
	// KnowledgeQAByEvent performs knowledge-based question answering by event
	KnowledgeQAByEvent(ctx context.Context, chatManage *types.ChatManage, eventList []types.EventType) error
	// SearchKnowledge performs knowledge-based search, without summarization
	// knowledgeBaseIDs: list of knowledge base IDs to search (supports multi-KB)
	// knowledgeIDs: list of specific knowledge (file) IDs to search
	SearchKnowledge(ctx context.Context, knowledgeBaseIDs []string, knowledgeIDs []string, query string) ([]*types.SearchResult, error)
	// AgentQA performs agent-based question answering with conversation history and streaming support
	// eventBus is optional - if nil, uses service's default EventBus
	// customAgent is optional - if provided, uses custom agent configuration instead of tenant defaults
	// summaryModelID is optional - if provided, overrides the model from customAgent config
	AgentQA(
		ctx context.Context,
		session *types.Session,
		query string,
		assistantMessageID string,
		summaryModelID string,
		eventBus *event.EventBus,
		customAgent *types.CustomAgent,
		knowledgeBaseIDs []string,
		knowledgeIDs []string,
	) error
	// ClearContext clears the LLM context for a session
	ClearContext(ctx context.Context, sessionID string) error
}

// SessionRepository defines the session repository interface
type SessionRepository interface {
	// Create creates a session
	Create(ctx context.Context, session *types.Session) (*types.Session, error)
	// Get gets a session
	Get(ctx context.Context, tenantID uint64, id string) (*types.Session, error)
	// GetByTenantID gets all sessions of a tenant
	GetByTenantID(ctx context.Context, tenantID uint64) ([]*types.Session, error)
	// GetPagedByTenantID gets paged sessions of a tenant
	GetPagedByTenantID(ctx context.Context, tenantID uint64, page *types.Pagination) ([]*types.Session, int64, error)
	// Update updates a session
	Update(ctx context.Context, session *types.Session) error
	// Delete deletes a session
	Delete(ctx context.Context, tenantID uint64, id string) error
}
