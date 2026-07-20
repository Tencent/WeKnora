package service

import (
	"context"
	stderrors "errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"

	chatpipeline "github.com/Tencent/WeKnora/internal/application/service/chat_pipeline"
)

func sessionUserIDFromContext(ctx context.Context) string {
	return types.SessionOwnerIDFromContext(ctx)
}

// loadSessionForRead loads a session honoring the caller's per-user scope, with
// an Admin+ fallback that additionally permits reading tenant channel sessions
// (API-key, IM, and embed) from the Web console. Non-admin callers must not
// open channel-managed rows even when legacy empty user_id scope would match.
// Write paths keep the strict scope and must not use this helper.
func loadSessionForRead(
	ctx context.Context,
	repo interfaces.SessionRepository,
	tenantID uint64,
	ownerID, sessionID string,
) (*types.Session, error) {
	isAdmin := types.TenantRoleFromContext(ctx).HasPermission(types.TenantRoleAdmin)

	session, err := repo.Get(ctx, tenantID, ownerID, sessionID)
	if err == nil {
		imPlatform, _ := repo.GetIMPlatform(ctx, tenantID, sessionID)
		if types.SessionRequiresAdminConsoleRead(session, imPlatform) && !isAdmin {
			return nil, apperrors.ErrSessionNotFound
		}
		if imPlatform != "" {
			session.IMPlatform = imPlatform
		}
		return session, nil
	}
	if !stderrors.Is(err, apperrors.ErrSessionNotFound) {
		return session, err
	}
	if !isAdmin {
		return nil, err
	}
	s, e := repo.GetByID(ctx, tenantID, sessionID)
	if e != nil {
		return nil, err
	}
	imPlatform, _ := repo.GetIMPlatform(ctx, tenantID, sessionID)
	if !types.SessionRequiresAdminConsoleRead(s, imPlatform) {
		return nil, err
	}
	if imPlatform != "" {
		s.IMPlatform = imPlatform
	}
	return s, nil
}

// generateEventID generates a unique event ID with type suffix for better traceability
func generateEventID(suffix string) string {
	return fmt.Sprintf("%s-%s", uuid.New().String()[:8], suffix)
}

// sessionService implements the SessionService interface for managing conversation sessions.
// History for multi-turn conversations is rebuilt from the messages table on demand
// (see service.LoadAgentHistory and chat_pipeline history loading) — there is no
// separate cross-turn cache layer.
type sessionService struct {
	cfg                   *config.Config                         // Application configuration
	sessionRepo           interfaces.SessionRepository           // Repository for session data
	messageRepo           interfaces.MessageRepository           // Repository for message data
	knowledgeBaseService  interfaces.KnowledgeBaseService        // Service for knowledge base operations
	modelService          interfaces.ModelService                // Service for model operations
	tenantService         interfaces.TenantService               // Service for tenant operations
	eventManager          *chatpipeline.EventManager             // Event manager for chat pipeline
	agentService          interfaces.AgentService                // Service for agent operations
	knowledgeService      interfaces.KnowledgeService            // Service for knowledge operations
	chunkService          interfaces.ChunkService                // Service for chunk operations
	webSearchStateRepo    interfaces.WebSearchStateService       // Service for web search state
	webSearchProviderRepo interfaces.WebSearchProviderRepository // Repository for web search provider entities
	kbShareService        interfaces.KBShareService              // Service for KB sharing operations
	suggestionRepo        interfaces.MessageSuggestionRepository
}

// NewSessionService creates a new session service instance with all required dependencies
func NewSessionService(cfg *config.Config,
	sessionRepo interfaces.SessionRepository,
	messageRepo interfaces.MessageRepository,
	knowledgeBaseService interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
	chunkService interfaces.ChunkService,
	modelService interfaces.ModelService,
	tenantService interfaces.TenantService,
	eventManager *chatpipeline.EventManager,
	agentService interfaces.AgentService,
	webSearchStateRepo interfaces.WebSearchStateService,
	webSearchProviderRepo interfaces.WebSearchProviderRepository,
	kbShareService interfaces.KBShareService,
	suggestionRepo interfaces.MessageSuggestionRepository,
) interfaces.SessionService {
	return &sessionService{
		cfg:                   cfg,
		sessionRepo:           sessionRepo,
		messageRepo:           messageRepo,
		knowledgeBaseService:  knowledgeBaseService,
		knowledgeService:      knowledgeService,
		chunkService:          chunkService,
		modelService:          modelService,
		tenantService:         tenantService,
		eventManager:          eventManager,
		agentService:          agentService,
		webSearchStateRepo:    webSearchStateRepo,
		webSearchProviderRepo: webSearchProviderRepo,
		kbShareService:        kbShareService,
		suggestionRepo:        suggestionRepo,
	}
}

// CreateSession creates a new conversation session
func (s *sessionService) CreateSession(ctx context.Context, session *types.Session) (*types.Session, error) {
	logger.Info(ctx, "Start creating session")

	// Validate tenant ID
	if session.TenantID == 0 {
		logger.Error(ctx, "Failed to create session: tenant ID cannot be empty")
		return nil, stderrors.New("tenant ID is required")
	}

	logger.Infof(ctx, "Creating session, tenant ID: %d", session.TenantID)

	// Create session in repository
	createdSession, err := s.sessionRepo.Create(ctx, session)
	if err != nil {
		return nil, err
	}

	logger.Infof(ctx, "Session created successfully, ID: %s, tenant ID: %d", createdSession.ID, createdSession.TenantID)
	return createdSession, nil
}

// GetSession retrieves a session by its ID
func (s *sessionService) GetSession(ctx context.Context, id string) (*types.Session, error) {
	logger.Info(ctx, "Start retrieving session")

	// Validate session ID
	if id == "" {
		logger.Error(ctx, "Failed to get session: session ID cannot be empty")
		return nil, stderrors.New("session id is required")
	}

	// Get tenant ID from context
	tenantID := types.MustTenantIDFromContext(ctx)
	userID := sessionUserIDFromContext(ctx)
	logger.Infof(ctx, "Retrieving session, ID: %s, tenant ID: %d", id, tenantID)

	// Get session from repository
	session, err := loadSessionForRead(ctx, s.sessionRepo, tenantID, userID, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": id,
			"tenant_id":  tenantID,
		})
		return nil, err
	}

	// Best-effort IM origin so the Web console can classify the session's
	// folder on read; a lookup failure must not fail the detail request.
	if session.IMPlatform == "" {
		if platform, pErr := s.sessionRepo.GetIMPlatform(ctx, tenantID, session.ID); pErr == nil {
			session.IMPlatform = platform
		} else {
			logger.Warnf(ctx, "Failed to resolve IM platform for session %s: %v", session.ID, pErr)
		}
	}

	logger.Infof(ctx, "Session retrieved successfully, ID: %s, tenant ID: %d", session.ID, session.TenantID)
	return session, nil
}

// GetOwnedSession loads a session strictly within the caller's owner scope.
// Unlike GetSession it does NOT apply the Admin+ API-key read fallback
// (loadSessionForRead), so it is the correct check for write/mutation
// endpoints: a tenant admin may open and read an API-key session, but must not
// be able to modify it (title, attachments, streaming state, messages).
func (s *sessionService) GetOwnedSession(ctx context.Context, id string) (*types.Session, error) {
	if id == "" {
		return nil, stderrors.New("session id is required")
	}
	tenantID := types.MustTenantIDFromContext(ctx)
	userID := sessionUserIDFromContext(ctx)
	return s.sessionRepo.Get(ctx, tenantID, userID, id)
}

// GetSessionByID loads a session by tenant and id without user scoping.
func (s *sessionService) GetSessionByID(ctx context.Context, tenantID uint64, id string) (*types.Session, error) {
	if id == "" {
		return nil, stderrors.New("session id is required")
	}
	if tenantID == 0 {
		return nil, stderrors.New("workspace id is required")
	}
	return s.sessionRepo.GetByID(ctx, tenantID, id)
}

// SetSessionOwnerID assigns sessions.user_id for the given session row.
func (s *sessionService) SetSessionOwnerID(ctx context.Context, tenantID uint64, sessionID, ownerID string) error {
	if sessionID == "" || ownerID == "" || tenantID == 0 {
		return stderrors.New("tenant id, session id and owner id are required")
	}
	affected, err := s.sessionRepo.SetOwnerID(ctx, tenantID, sessionID, ownerID)
	if err != nil {
		return err
	}
	if affected == 0 {
		return apperrors.ErrSessionNotFound
	}
	return nil
}

// GetSessionsByTenant retrieves all sessions for the current tenant
func (s *sessionService) GetSessionsByTenant(ctx context.Context) ([]*types.Session, error) {
	// Get tenant ID from context
	tenantID := types.MustTenantIDFromContext(ctx)
	userID := sessionUserIDFromContext(ctx)
	logger.Infof(ctx, "Retrieving all sessions for tenant, tenant ID: %d", tenantID)

	// Get sessions from repository
	sessions, err := s.sessionRepo.GetByTenantID(ctx, tenantID, userID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_id": tenantID,
		})
		return nil, err
	}

	logger.Infof(
		ctx, "Tenant sessions retrieved successfully, tenant ID: %d, session count: %d", tenantID, len(sessions),
	)
	return sessions, nil
}

// GetPagedSessionsByTenant retrieves sessions for the current tenant with pagination
func (s *sessionService) GetPagedSessionsByTenant(ctx context.Context,
	pagination *types.Pagination,
) (*types.PageResult, error) {
	// Get tenant ID from context
	tenantID := types.MustTenantIDFromContext(ctx)
	userID := sessionUserIDFromContext(ctx)
	// Get paged sessions from repository
	sessions, total, err := s.sessionRepo.GetPagedByTenantID(ctx, tenantID, userID, pagination)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_id": tenantID,
			"page":      pagination.Page,
			"page_size": pagination.PageSize,
		})
		return nil, err
	}

	return types.NewPageResult(total, pagination, sessions), nil
}

// ListSessions returns a page of sessions with search/source filters, scoped to
// the current tenant (and user when the caller is an authenticated user).
func (s *sessionService) ListSessions(
	ctx context.Context, query *types.SessionListQuery,
) (*types.PageResult, error) {
	if query == nil {
		query = &types.SessionListQuery{}
	}
	query.TenantID = types.MustTenantIDFromContext(ctx)
	// API / IM / embed source filters are tenant-wide admin views over channel
	// traffic. Gate them behind Admin+ and drop the per-user owner scope so an
	// Owner/admin can observe sessions that are otherwise isolated per key,
	// visitor, or IM identity; everyone else stays scoped to their own principal.
	if types.SessionListSourceRequiresAdmin(query.Source) {
		if !types.TenantRoleFromContext(ctx).HasPermission(types.TenantRoleAdmin) {
			return nil, apperrors.NewForbiddenError(
				"listing channel sessions requires tenant admin or owner role",
			)
		}
		query.UserID = ""
	} else if uid := types.SessionOwnerIDFromContext(ctx); uid != "" {
		query.UserID = uid
	}

	items, total, err := s.sessionRepo.QueryPaged(ctx, query)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_id": query.TenantID,
			"user_id":   query.UserID,
			"keyword":   query.Keyword,
			"source":    query.Source,
			"agent_id":  query.AgentID,
		})
		return nil, err
	}

	pagination := &types.Pagination{Page: query.Page, PageSize: query.PageSize}
	return types.NewPageResult(total, pagination, items), nil
}

// CountSessionsBySource returns the total session count for a source filter
// without the Admin+ gate used by ListSessions. Aggregate stats endpoints may
// expose counts to Viewer+ while keeping session rows admin-only.
func (s *sessionService) CountSessionsBySource(
	ctx context.Context, query *types.SessionListQuery,
) (int64, error) {
	if query == nil {
		query = &types.SessionListQuery{}
	}
	query.TenantID = types.MustTenantIDFromContext(ctx)
	if types.SessionListSourceRequiresAdmin(query.Source) {
		query.UserID = ""
	} else if uid := types.SessionOwnerIDFromContext(ctx); uid != "" {
		query.UserID = uid
	}
	_, total, err := s.sessionRepo.QueryPaged(ctx, query)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_id": query.TenantID,
			"user_id":   query.UserID,
			"source":    query.Source,
		})
		return 0, err
	}
	return total, nil
}

// SetSessionPinned pins or unpins a session for the current user scope.
// Returns the number of rows affected; 0 means the session doesn't exist
// or is not owned by the caller so the handler can respond 404.
func (s *sessionService) SetSessionPinned(
	ctx context.Context, sessionID string, pinned bool,
) (int64, error) {
	if sessionID == "" {
		return 0, stderrors.New("session id is required")
	}
	tenantID := types.MustTenantIDFromContext(ctx)
	userID := sessionUserIDFromContext(ctx)
	return s.sessionRepo.SetPinned(ctx, tenantID, userID, sessionID, pinned)
}

// UpdateSession updates an existing session's properties
func (s *sessionService) UpdateSession(ctx context.Context, session *types.Session) error {
	// Validate session ID
	if session.ID == "" {
		logger.Error(ctx, "Failed to update session: session ID cannot be empty")
		return stderrors.New("session id is required")
	}

	// Update session in repository
	userID := sessionUserIDFromContext(ctx)
	if _, err := s.sessionRepo.Get(ctx, session.TenantID, userID, session.ID); err != nil {
		return err
	}

	_, err := s.sessionRepo.Update(ctx, session, userID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": session.ID,
			"tenant_id":  session.TenantID,
		})
		return err
	}

	logger.Infof(ctx, "Session updated successfully, ID: %s", session.ID)
	return nil
}

// UpdateSessionLastRequestState persists the input-bar state used by the most
// recent QA request on this session. Called from the QA handler after a
// request is accepted so the UI can rehydrate the same settings on reopen.
// Best-effort: scope mismatches are logged and swallowed — failing to record
// the UI memo should never fail the user's chat request.
func (s *sessionService) UpdateSessionLastRequestState(
	ctx context.Context, sessionID string, state *types.SessionLastRequestState,
) error {
	if sessionID == "" {
		return stderrors.New("session id is required")
	}
	tenantID := types.MustTenantIDFromContext(ctx)
	userID := sessionUserIDFromContext(ctx)
	affected, err := s.sessionRepo.UpdateLastRequestState(ctx, tenantID, userID, sessionID, state)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": sessionID,
			"tenant_id":  tenantID,
		})
		return err
	}
	if affected == 0 {
		logger.Warnf(ctx, "UpdateSessionLastRequestState: no rows affected for session %s", sessionID)
	}
	return nil
}

// DeleteSession removes a session by its ID
func (s *sessionService) DeleteSession(ctx context.Context, id string) error {
	// Validate session ID
	if id == "" {
		logger.Error(ctx, "Failed to delete session: session ID cannot be empty")
		return stderrors.New("session id is required")
	}

	// Get tenant ID from context
	tenantID := types.MustTenantIDFromContext(ctx)
	userID := sessionUserIDFromContext(ctx)

	if _, err := s.sessionRepo.Get(ctx, tenantID, userID, id); err != nil {
		return err
	}

	// Cleanup chat history knowledge entries for this session (async, best-effort).
	// Use WithoutCancel so the goroutine survives after the HTTP request context is done.
	bgCtx := context.WithoutCancel(ctx)
	go func() {
		knowledgeIDs, err := s.messageRepo.GetKnowledgeIDsBySessionID(bgCtx, id)
		if err != nil {
			logger.Warnf(bgCtx, "Failed to get knowledge IDs for session %s: %v", id, err)
			return
		}
		if len(knowledgeIDs) > 0 {
			if err := s.knowledgeService.DeleteKnowledgeList(bgCtx, knowledgeIDs); err != nil {
				logger.Warnf(bgCtx, "Failed to delete chat history knowledge for session %s: %v", id, err)
			}
		}
	}()

	// Cleanup temporary KB stored in Redis for this session
	if err := s.webSearchStateRepo.DeleteWebSearchTempKBState(ctx, id); err != nil {
		logger.Warnf(ctx, "Failed to cleanup temporary KB for session %s: %v", id, err)
	}

	// Delete session from repository
	rows, err := s.sessionRepo.Delete(ctx, tenantID, userID, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": id,
			"tenant_id":  tenantID,
		})
		return err
	}
	if rows == 0 {
		return apperrors.ErrSessionNotFound
	}
	if s.suggestionRepo != nil {
		if err := s.suggestionRepo.DeleteBySessionID(ctx, tenantID, id); err != nil {
			logger.Warnf(ctx, "Failed to delete suggestions for session %s: %v", id, err)
		}
	}

	return nil
}

// BatchDeleteSessions deletes multiple sessions by IDs
func (s *sessionService) BatchDeleteSessions(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		logger.Error(ctx, "Failed to batch delete sessions: IDs list is empty")
		return stderrors.New("session ids are required")
	}

	// Get tenant ID from context
	tenantID := types.MustTenantIDFromContext(ctx)
	userID := sessionUserIDFromContext(ctx)

	visibleIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, err := s.sessionRepo.Get(ctx, tenantID, userID, id); err == nil {
			visibleIDs = append(visibleIDs, id)
		} else if !stderrors.Is(err, apperrors.ErrSessionNotFound) {
			return err
		}
	}
	if len(visibleIDs) == 0 {
		return apperrors.ErrSessionNotFound
	}

	// Cleanup associated resources for each session
	bgCtx := context.WithoutCancel(ctx)
	for _, id := range visibleIDs {
		// Cleanup chat history knowledge entries (async, best-effort)
		go func(sessionID string) {
			knowledgeIDs, err := s.messageRepo.GetKnowledgeIDsBySessionID(bgCtx, sessionID)
			if err != nil {
				logger.Warnf(bgCtx, "Failed to get knowledge IDs for session %s: %v", sessionID, err)
				return
			}
			if len(knowledgeIDs) > 0 {
				if err := s.knowledgeService.DeleteKnowledgeList(bgCtx, knowledgeIDs); err != nil {
					logger.Warnf(bgCtx, "Failed to delete chat history knowledge for session %s: %v", sessionID, err)
				}
			}
		}(id)

		if err := s.webSearchStateRepo.DeleteWebSearchTempKBState(ctx, id); err != nil {
			logger.Warnf(ctx, "Failed to cleanup temporary KB for session %s: %v", id, err)
		}
	}

	// Batch delete sessions from repository
	if _, err := s.sessionRepo.BatchDelete(ctx, tenantID, userID, visibleIDs); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_ids": visibleIDs,
			"tenant_id":   tenantID,
		})
		return err
	}
	if s.suggestionRepo != nil {
		for _, id := range visibleIDs {
			if err := s.suggestionRepo.DeleteBySessionID(ctx, tenantID, id); err != nil {
				logger.Warnf(ctx, "Failed to delete suggestions for session %s: %v", id, err)
			}
		}
	}

	return nil
}

// DeleteAllSessions deletes all sessions for the current tenant
func (s *sessionService) DeleteAllSessions(ctx context.Context) error {
	tenantID := types.MustTenantIDFromContext(ctx)
	userID := sessionUserIDFromContext(ctx)
	logger.Infof(ctx, "Deleting all sessions for tenant %d", tenantID)

	sessions, err := s.sessionRepo.GetByTenantID(ctx, tenantID, userID)
	if err != nil {
		logger.Warnf(ctx, "Failed to list sessions for cleanup: %v", err)
	} else {
		bgCtx := context.WithoutCancel(ctx)
		for _, session := range sessions {
			// Cleanup chat history knowledge entries (async, best-effort)
			go func(sessionID string) {
				knowledgeIDs, err := s.messageRepo.GetKnowledgeIDsBySessionID(bgCtx, sessionID)
				if err != nil {
					logger.Warnf(bgCtx, "Failed to get knowledge IDs for session %s: %v", sessionID, err)
					return
				}
				if len(knowledgeIDs) > 0 {
					if err := s.knowledgeService.DeleteKnowledgeList(bgCtx, knowledgeIDs); err != nil {
						logger.Warnf(bgCtx, "Failed to delete chat history knowledge for session %s: %v", sessionID, err)
					}
				}
			}(session.ID)

			if err := s.webSearchStateRepo.DeleteWebSearchTempKBState(ctx, session.ID); err != nil {
				logger.Warnf(ctx, "Failed to cleanup temporary KB for session %s: %v", session.ID, err)
			}
		}
	}

	if _, err := s.sessionRepo.DeleteAllByTenantID(ctx, tenantID, userID); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_id": tenantID,
		})
		return err
	}
	if s.suggestionRepo != nil && sessions != nil {
		for _, session := range sessions {
			if err := s.suggestionRepo.DeleteBySessionID(ctx, tenantID, session.ID); err != nil {
				logger.Warnf(ctx, "Failed to delete suggestions for session %s: %v", session.ID, err)
			}
		}
	}

	logger.Infof(ctx, "All sessions deleted for tenant %d", tenantID)
	return nil
}

// GenerateTitle generates a title for the current conversation content
// modelID: optional model ID to use for title generation (if empty, uses first available KnowledgeQA model)
func (s *sessionService) GenerateTitle(ctx context.Context,
	session *types.Session, messages []types.Message, modelID string,
) (string, error) {
	if session == nil {
		logger.Error(ctx, "Failed to generate title: session cannot be empty")
		return "", stderrors.New("session cannot be empty")
	}

	// Skip if title already exists
	if session.Title != "" {
		return session.Title, nil
	}
	var err error
	// Get the first user message, either from provided messages or repository
	var message *types.Message
	if len(messages) == 0 {
		message, err = s.messageRepo.GetFirstMessageOfUser(ctx, session.ID)
		if err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"session_id": session.ID,
			})
			return "", err
		}
	} else {
		for _, m := range messages {
			if m.Role == "user" {
				message = &m
				break
			}
		}
	}

	// Ensure a user message was found
	if message == nil {
		logger.Error(ctx, "No user message found, cannot generate title")
		return "", stderrors.New("no user message found")
	}

	// Use provided modelID, or fallback to first available KnowledgeQA model
	if modelID == "" {
		models, err := s.modelService.ListModels(ctx)
		if err != nil {
			logger.ErrorWithFields(ctx, err, nil)
			return "", fmt.Errorf("failed to list models: %w", err)
		}
		for _, model := range models {
			if model == nil {
				continue
			}
			if model.Type == types.ModelTypeKnowledgeQA {
				modelID = model.ID
				logger.Infof(ctx, "Using first available KnowledgeQA model for title: %s", modelID)
				break
			}
		}
		if modelID == "" {
			logger.Error(ctx, "No KnowledgeQA model found")
			return "", stderrors.New("no KnowledgeQA model available for title generation")
		}
	} else {
		logger.Infof(ctx, "Using specified model for title generation: %s", modelID)
	}

	chatModel, err := s.modelService.GetChatModel(ctx, modelID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id": modelID,
		})
		return "", err
	}

	// Prepare messages for title generation
	titlePrompt := types.RenderPromptPlaceholders(s.cfg.Conversation.GenerateSessionTitlePrompt, types.PlaceholderValues{
		"language": types.LanguageNameFromContext(ctx),
	})
	var chatMessages []chat.Message
	chatMessages = append(chatMessages,
		chat.Message{Role: "system", Content: titlePrompt},
	)
	chatMessages = append(chatMessages,
		chat.Message{Role: "user", Content: message.Content},
	)

	// Call model to generate title
	thinking := false
	response, err := chatModel.Chat(ctx, chatMessages, &chat.ChatOptions{
		Temperature: 0.3,
		Thinking:    &thinking,
	})
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		return "", err
	}

	// Process and store the generated title
	session.Title = strings.TrimPrefix(response.Content, "<think>\n\n</think>")

	// Update session with new title
	_, err = s.sessionRepo.Update(ctx, session, session.UserID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		return "", err
	}

	return session.Title, nil
}

// GenerateTitleAsync generates a title for the session asynchronously
// This method clones the session and generates the title in a goroutine
// It emits an event when the title is generated
// modelID: optional model ID to use for title generation (if empty, uses first available KnowledgeQA model)
func (s *sessionService) GenerateTitleAsync(
	ctx context.Context,
	session *types.Session,
	userQuery string,
	modelID string,
	eventBus *event.EventBus,
) {
	// Use context tenant (effective tenant when using shared agent) so ListModels/GetChatModel find the agent's model.
	// The session row itself is still updated by its persisted tenant/user owner scope.
	tenantID := ctx.Value(types.TenantIDContextKey)
	requestID := ctx.Value(types.RequestIDContextKey)
	language := ctx.Value(types.LanguageContextKey)
	// Keep the Langfuse trace handle so the async title generation shows up
	// as a child of the same trace as the originating chat request.
	langfuseTrace := ctx.Value(types.LangfuseTraceContextKey)
	go func() {
		bgCtx := context.Background()
		if tenantID != nil {
			bgCtx = context.WithValue(bgCtx, types.TenantIDContextKey, tenantID)
		}
		if requestID != nil {
			bgCtx = context.WithValue(bgCtx, types.RequestIDContextKey, requestID)
		}
		if language != nil {
			bgCtx = context.WithValue(bgCtx, types.LanguageContextKey, language)
		}
		if langfuseTrace != nil {
			bgCtx = context.WithValue(bgCtx, types.LangfuseTraceContextKey, langfuseTrace)
		}

		// Skip if title already exists
		if session.Title != "" {
			return
		}

		// Generate title using the first user message
		messages := []types.Message{
			{
				Role:    "user",
				Content: userQuery,
			},
		}

		title, err := s.GenerateTitle(bgCtx, session, messages, modelID)
		if err != nil {
			logger.ErrorWithFields(bgCtx, err, map[string]interface{}{
				"session_id": session.ID,
			})
			return
		}

		// Emit title update event - BUG FIX: use bgCtx instead of ctx
		// The original ctx is from the HTTP request and may be cancelled by the time we get here
		if eventBus != nil {
			if err := eventBus.Emit(bgCtx, event.Event{
				Type:      event.EventSessionTitle,
				SessionID: session.ID,
				Data: event.SessionTitleData{
					SessionID: session.ID,
					Title:     title,
				},
			}); err != nil {
				logger.ErrorWithFields(bgCtx, err, map[string]interface{}{
					"session_id": session.ID,
				})
			} else {
				logger.Infof(bgCtx, "Title update event emitted successfully, session ID: %s, title: %s", session.ID, title)
			}
		}
	}()
}
