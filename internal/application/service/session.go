package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"

	chatpipeline "github.com/Tencent/WeKnora/internal/application/service/chat_pipeline"
	llmcontext "github.com/Tencent/WeKnora/internal/application/service/llmcontext"
)

// generateEventID generates a unique event ID with type suffix for better traceability
func generateEventID(suffix string) string {
	return fmt.Sprintf("%s-%s", uuid.New().String()[:8], suffix)
}

// sessionService implements the SessionService interface for managing conversation sessions
type sessionService struct {
	cfg                   *config.Config                         // Application configuration
	sessionRepo           interfaces.SessionRepository           // Repository for session data
	messageRepo           interfaces.MessageRepository           // Repository for message data
	knowledgeBaseService  interfaces.KnowledgeBaseService        // Service for knowledge base operations
	modelService          interfaces.ModelService                // Service for model operations
	tenantService         interfaces.TenantService               // Service for tenant operations
	eventManager          *chatpipeline.EventManager             // Event manager for chat pipeline
	agentService          interfaces.AgentService                // Service for agent operations
	sessionStorage        llmcontext.ContextStorage              // Session storage
	knowledgeService      interfaces.KnowledgeService            // Service for knowledge operations
	chunkService          interfaces.ChunkService                // Service for chunk operations
	webSearchStateRepo    interfaces.WebSearchStateService       // Service for web search state
	webSearchProviderRepo interfaces.WebSearchProviderRepository // Repository for web search provider entities
	kbShareService        interfaces.KBShareService              // Service for KB sharing operations
	memoryService         interfaces.MemoryService               // Service for memory operations
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
	sessionStorage llmcontext.ContextStorage,
	webSearchStateRepo interfaces.WebSearchStateService,
	webSearchProviderRepo interfaces.WebSearchProviderRepository,
	kbShareService interfaces.KBShareService,
	memoryService interfaces.MemoryService,
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
		sessionStorage:        sessionStorage,
		webSearchStateRepo:    webSearchStateRepo,
		webSearchProviderRepo: webSearchProviderRepo,
		kbShareService:        kbShareService,
		memoryService:         memoryService,
	}
}

// CreateSession creates a new conversation session
func (s *sessionService) CreateSession(ctx context.Context, session *types.Session) (*types.Session, error) {
	logger.Info(ctx, "Start creating session")

	// Validate tenant ID
	if session.TenantID == 0 {
		logger.Error(ctx, "Failed to create session: tenant ID cannot be empty")
		return nil, errors.New("tenant ID is required")
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
		return nil, errors.New("session id is required")
	}

	// Get tenant ID from context
	tenantID := types.MustTenantIDFromContext(ctx)
	logger.Infof(ctx, "Retrieving session, ID: %s, tenant ID: %d", id, tenantID)

	// Get session from repository
	session, err := s.sessionRepo.Get(ctx, tenantID, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": id,
			"tenant_id":  tenantID,
		})
		return nil, err
	}

	logger.Infof(ctx, "Session retrieved successfully, ID: %s, tenant ID: %d", session.ID, session.TenantID)
	return session, nil
}

// GetSessionsByTenant retrieves all sessions for the current tenant
func (s *sessionService) GetSessionsByTenant(ctx context.Context) ([]*types.Session, error) {
	// Get tenant ID from context
	tenantID := types.MustTenantIDFromContext(ctx)
	logger.Infof(ctx, "Retrieving all sessions for tenant, tenant ID: %d", tenantID)

	// Get sessions from repository
	sessions, err := s.sessionRepo.GetByTenantID(ctx, tenantID)
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
	// Get paged sessions from repository
	sessions, total, err := s.sessionRepo.GetPagedByTenantID(ctx, tenantID, pagination)
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

// UpdateSession updates an existing session's properties
func (s *sessionService) UpdateSession(ctx context.Context, session *types.Session) error {
	// Validate session ID
	if session.ID == "" {
		logger.Error(ctx, "Failed to update session: session ID cannot be empty")
		return errors.New("session id is required")
	}

	// Update session in repository
	err := s.sessionRepo.Update(ctx, session)
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

// DeleteSession removes a session by its ID
func (s *sessionService) DeleteSession(ctx context.Context, id string) error {
	// Validate session ID
	if id == "" {
		logger.Error(ctx, "Failed to delete session: session ID cannot be empty")
		return errors.New("session id is required")
	}

	// Get tenant ID from context
	tenantID := types.MustTenantIDFromContext(ctx)

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

	// Cleanup conversation context stored in Redis for this session
	if err := s.sessionStorage.Delete(ctx, id); err != nil {
		logger.Warnf(ctx, "Failed to cleanup conversation context for session %s: %v", id, err)
	}

	// Delete session from repository
	err := s.sessionRepo.Delete(ctx, tenantID, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": id,
			"tenant_id":  tenantID,
		})
		return err
	}

	return nil
}

// BatchDeleteSessions deletes multiple sessions by IDs
func (s *sessionService) BatchDeleteSessions(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		logger.Error(ctx, "Failed to batch delete sessions: IDs list is empty")
		return errors.New("session ids are required")
	}

	// Get tenant ID from context
	tenantID := types.MustTenantIDFromContext(ctx)

	// Cleanup associated resources for each session
	bgCtx := context.WithoutCancel(ctx)
	for _, id := range ids {
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
		if err := s.sessionStorage.Delete(ctx, id); err != nil {
			logger.Warnf(ctx, "Failed to cleanup conversation context for session %s: %v", id, err)
		}
	}

	// Batch delete sessions from repository
	if err := s.sessionRepo.BatchDelete(ctx, tenantID, ids); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_ids": ids,
			"tenant_id":   tenantID,
		})
		return err
	}

	return nil
}

// DeleteAllSessions deletes all sessions for the current tenant
func (s *sessionService) DeleteAllSessions(ctx context.Context) error {
	tenantID := types.MustTenantIDFromContext(ctx)
	logger.Infof(ctx, "Deleting all sessions for tenant %d", tenantID)

	sessions, err := s.sessionRepo.GetByTenantID(ctx, tenantID)
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
			if err := s.sessionStorage.Delete(ctx, session.ID); err != nil {
				logger.Warnf(ctx, "Failed to cleanup conversation context for session %s: %v", session.ID, err)
			}
		}
	}

	if err := s.sessionRepo.DeleteAllByTenantID(ctx, tenantID); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_id": tenantID,
		})
		return err
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
		return "", errors.New("session cannot be empty")
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
		return "", errors.New("no user message found")
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
			return "", errors.New("no KnowledgeQA model available for title generation")
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
	err = s.sessionRepo.Update(ctx, session)
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
	// sessionRepo.Update uses session.TenantID in WHERE, so the session row is updated correctly regardless of ctx.
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

// ClearContext clears the LLM context for a session
// This is useful when switching knowledge bases or agent modes to prevent context contamination
func (s *sessionService) ClearContext(ctx context.Context, sessionID string) error {
	logger.Infof(ctx, "Clearing context for session: %s", sessionID)
	return s.sessionStorage.Delete(ctx, sessionID)
}
