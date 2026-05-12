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
	// BatchDeleteSessions deletes multiple sessions by IDs
	BatchDeleteSessions(ctx context.Context, ids []string) error
	// DeleteAllSessions deletes all sessions for the current tenant
	DeleteAllSessions(ctx context.Context) error
	// ListSessions returns a page of sessions for the current tenant/user with
	// search/source filters and pin-aware ordering. User scope is pulled from ctx.
	ListSessions(ctx context.Context, query *types.SessionListQuery) (*types.PageResult, error)
	// SetSessionPinned pins or unpins the session for the current user scope.
	// Returns the number of rows affected; 0 signals "not found" to the handler.
	SetSessionPinned(ctx context.Context, sessionID string, pinned bool) (int64, error)
	// GenerateTitle generates a title for the current conversation
	// modelID: optional model ID to use for title generation (if empty, uses first available KnowledgeQA model)
	GenerateTitle(ctx context.Context, session *types.Session, messages []types.Message, modelID string) (string, error)
	// GenerateTitleAsync generates a title for the session asynchronously
	// It emits an event when the title is generated
	// modelID: optional model ID to use for title generation (if empty, uses first available KnowledgeQA model)
	GenerateTitleAsync(ctx context.Context, session *types.Session, userQuery string, modelID string, eventBus *event.EventBus)
	// KnowledgeQA performs knowledge-based question answering.
	// Events are emitted through eventBus (references, answer chunks, completion).
	KnowledgeQA(ctx context.Context, req *types.QARequest, eventBus *event.EventBus) error
	// KnowledgeQAByEvent performs knowledge-based question answering by event
	KnowledgeQAByEvent(ctx context.Context, chatManage *types.ChatManage, eventList []types.EventType) error
	// SearchKnowledge performs knowledge-based search, without summarization
	// knowledgeBaseIDs: list of knowledge base IDs to search (supports multi-KB)
	// knowledgeIDs: list of specific knowledge (file) IDs to search
	SearchKnowledge(ctx context.Context, knowledgeBaseIDs []string, knowledgeIDs []string, query string) ([]*types.SearchResult, error)
	// AgentQA performs agent-based question answering with conversation history and streaming support.
	AgentQA(ctx context.Context, req *types.QARequest, eventBus *event.EventBus) error
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
	// QueryPaged lists sessions with filters, user-scoped ownership and pin-aware ordering.
	QueryPaged(ctx context.Context, q *types.SessionListQuery) ([]*types.SessionListItem, int64, error)
	// Update updates a session
	Update(ctx context.Context, session *types.Session) error
	// SetPinned pins or unpins a session row scoped by tenant.
	// userID, when non-empty, is enforced so users cannot pin sessions they don't own.
	// Returns the number of rows affected; 0 means the session doesn't exist or is
	// not visible to this caller.
	SetPinned(ctx context.Context, tenantID uint64, userID string, id string, pinned bool) (int64, error)
	// Delete deletes a session
	Delete(ctx context.Context, tenantID uint64, id string) error
	// BatchDelete deletes multiple sessions by IDs
	BatchDelete(ctx context.Context, tenantID uint64, ids []string) error
	// DeleteAllByTenantID deletes all sessions for a tenant
	DeleteAllByTenantID(ctx context.Context, tenantID uint64) error
}
