package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/template"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	chatpipline "github.com/Tencent/WeKnora/internal/application/service/chat_pipline"
	llmcontext "github.com/Tencent/WeKnora/internal/application/service/llmcontext"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/tracing"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// generateEventID generates a unique event ID with type suffix for better traceability
func generateEventID(suffix string) string {
	return fmt.Sprintf("%s-%s", uuid.New().String()[:8], suffix)
}

// sessionService implements the SessionService interface for managing conversation sessions
type sessionService struct {
	cfg                  *config.Config                  // Application configuration
	sessionRepo          interfaces.SessionRepository    // Repository for session data
	messageRepo          interfaces.MessageRepository    // Repository for message data
	knowledgeBaseService interfaces.KnowledgeBaseService // Service for knowledge base operations
	modelService         interfaces.ModelService         // Service for model operations
	tenantService        interfaces.TenantService        // Service for tenant operations
	eventManager         *chatpipline.EventManager       // Event manager for chat pipeline
	agentService         interfaces.AgentService         // Service for agent operations
	sessionStorage       llmcontext.ContextStorage       // Session storage
	knowledgeService     interfaces.KnowledgeService     // Service for knowledge operations
	chunkService         interfaces.ChunkService         // Service for chunk operations
	redisClient          *redis.Client                   // Redis client for temp KB state
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
	eventManager *chatpipline.EventManager,
	agentService interfaces.AgentService,
	sessionStorage llmcontext.ContextStorage,
	redisClient *redis.Client,
) interfaces.SessionService {
	return &sessionService{
		cfg:                  cfg,
		sessionRepo:          sessionRepo,
		messageRepo:          messageRepo,
		knowledgeBaseService: knowledgeBaseService,
		knowledgeService:     knowledgeService,
		chunkService:         chunkService,
		modelService:         modelService,
		tenantService:        tenantService,
		eventManager:         eventManager,
		agentService:         agentService,
		sessionStorage:       sessionStorage,
		redisClient:          redisClient,
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

	logger.Infof(ctx, "Creating session, tenant ID: %d, model ID: %s, knowledge base ID: %s",
		session.TenantID, session.SummaryModelID, session.KnowledgeBaseID)

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
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
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
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
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
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
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
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	// Cleanup temporary KB stored in Redis for this session
	if err := s.DeleteWebSearchTempKBState(ctx, id); err != nil {
		logger.Warnf(ctx, "Failed to cleanup temporary KB for session %s: %v", id, err)
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

// GenerateTitle generates a title for the current conversation content
func (s *sessionService) GenerateTitle(ctx context.Context,
	session *types.Session, messages []types.Message,
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

	// Get chat model, use default if SummaryModelID is empty
	modelID := session.SummaryModelID
	if modelID == "" {
		// Try to get an available KnowledgeQA model
		models, err := s.modelService.ListModels(ctx)
		if err != nil {
			logger.ErrorWithFields(ctx, err, nil)
			return "", fmt.Errorf("failed to list models: %w", err)
		}
		// Find first available KnowledgeQA model
		for _, model := range models {
			if model == nil {
				continue
			}
			if model.Type == types.ModelTypeKnowledgeQA {
				modelID = model.ID
				logger.Infof(ctx, "Using first available KnowledgeQA model: %s", modelID)
				break
			}
		}
		if modelID == "" {
			logger.Error(ctx, "No KnowledgeQA model found")
			return "", errors.New("no KnowledgeQA model available for title generation")
		}
	}

	chatModel, err := s.modelService.GetChatModel(ctx, modelID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id": modelID,
		})
		return "", err
	}

	// Prepare messages for title generation
	var chatMessages []chat.Message
	chatMessages = append(chatMessages,
		chat.Message{Role: "system", Content: s.cfg.Conversation.GenerateSessionTitlePrompt},
	)
	chatMessages = append(chatMessages,
		chat.Message{Role: "user", Content: message.Content + " /no_think"},
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
func (s *sessionService) GenerateTitleAsync(
	ctx context.Context,
	session *types.Session,
	userQuery string,
	eventBus *event.EventBus,
) {
	// Extract values from context before cloning
	tenantID := ctx.Value(types.TenantIDContextKey)
	requestID := ctx.Value(types.RequestIDContextKey)
	go func() {
		// Create new background context and copy values
		bgCtx := context.Background()
		if tenantID != nil {
			bgCtx = context.WithValue(bgCtx, types.TenantIDContextKey, tenantID)
		}
		if requestID != nil {
			bgCtx = context.WithValue(bgCtx, types.RequestIDContextKey, requestID)
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

		title, err := s.GenerateTitle(bgCtx, session, messages)
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

// KnowledgeQA performs knowledge base question answering with LLM summarization
// Events are emitted through eventBus (references, answer chunks, completion)
func (s *sessionService) KnowledgeQA(
	ctx context.Context,
	session *types.Session,
	query string,
	knowledgeBaseIDs []string,
	knowledgeIDs []string,
	assistantMessageID string,
	summaryModelID string,
	webSearchEnabled bool,
	eventBus *event.EventBus,
) error {
	logger.Infof(
		ctx,
		"Knowledge base question answering parameters, session ID: %s, query: %s, webSearchEnabled: %v",
		session.ID,
		query,
		webSearchEnabled,
	)

	// If no knowledge base IDs provided, fall back to session's default
	if len(knowledgeBaseIDs) == 0 {
		if session.KnowledgeBaseID != "" {
			knowledgeBaseIDs = []string{session.KnowledgeBaseID}
			logger.Infof(ctx, "No knowledge base IDs provided, using session default: %s", session.KnowledgeBaseID)
		} else {
			logger.Warnf(ctx, "Session has no associated knowledge base, session ID: %s", session.ID)
		}
	}

	logger.Infof(ctx, "Using knowledge bases: %v", knowledgeBaseIDs)

	// Determine chat model ID: prioritize request's summaryModelID, then Remote models
	chatModelID, err := s.selectChatModelIDWithOverride(ctx, session, knowledgeBaseIDs, knowledgeIDs, summaryModelID)
	if err != nil {
		return err
	}

	rewritePromptSystem := s.cfg.Conversation.RewritePromptSystem
	rewritePromptUser := s.cfg.Conversation.RewritePromptUser
	var tenantConv *types.ConversationConfig
	if tc, err := getTenantConversationConfig(ctx); err == nil {
		tenantConv = tc
	} else {
		logger.Warnf(ctx, "Failed to load tenant conversation config, tenant ID: %d, error: %v", session.TenantID, err)
	}

	vectorThreshold := session.VectorThreshold
	keywordThreshold := session.KeywordThreshold
	embeddingTopK := session.EmbeddingTopK
	rerankModelID := session.RerankModelID
	rerankTopK := session.RerankTopK
	rerankThreshold := session.RerankThreshold
	maxRounds := session.MaxRounds
	fallbackStrategy := session.FallbackStrategy
	fallbackResponse := session.FallbackResponse
	fallbackPrompt := ""
	enableRewrite := session.EnableRewrite
	enableQueryExpansion := true

	summaryParams := session.SummaryParameters
	if summaryParams == nil {
		summaryParams = &types.SummaryConfig{}
	}
	summaryConfig := types.SummaryConfig{
		MaxTokens:           summaryParams.MaxTokens,
		RepeatPenalty:       summaryParams.RepeatPenalty,
		TopK:                summaryParams.TopK,
		TopP:                summaryParams.TopP,
		FrequencyPenalty:    summaryParams.FrequencyPenalty,
		PresencePenalty:     summaryParams.PresencePenalty,
		Prompt:              summaryParams.Prompt,
		ContextTemplate:     summaryParams.ContextTemplate,
		Temperature:         summaryParams.Temperature,
		Seed:                summaryParams.Seed,
		NoMatchPrefix:       summaryParams.NoMatchPrefix,
		MaxCompletionTokens: summaryParams.MaxCompletionTokens,
	}

	if tenantConv != nil {
		vectorThreshold = tenantConv.VectorThreshold
		keywordThreshold = tenantConv.KeywordThreshold
		embeddingTopK = tenantConv.EmbeddingTopK
		rerankModelID = tenantConv.RerankModelID
		rerankTopK = tenantConv.RerankTopK
		rerankThreshold = tenantConv.RerankThreshold
		maxRounds = tenantConv.MaxRounds
		if tenantConv.FallbackStrategy != "" {
			fallbackStrategy = types.FallbackStrategy(tenantConv.FallbackStrategy)
		}
		fallbackResponse = tenantConv.FallbackResponse
		if tenantConv.FallbackPrompt != "" {
			fallbackPrompt = tenantConv.FallbackPrompt
		}
		enableRewrite = tenantConv.EnableRewrite
		enableQueryExpansion = tenantConv.EnableQueryExpansion

		if tenantConv.MaxCompletionTokens != 0 {
			summaryConfig.MaxCompletionTokens = tenantConv.MaxCompletionTokens
		}
		if tenantConv.Prompt != "" {
			summaryConfig.Prompt = tenantConv.Prompt
		}
		if tenantConv.ContextTemplate != "" {
			summaryConfig.ContextTemplate = tenantConv.ContextTemplate
		}
		if tenantConv.Temperature != 0 {
			summaryConfig.Temperature = tenantConv.Temperature
		}
		if tenantConv.RewritePromptSystem != "" {
			rewritePromptSystem = tenantConv.RewritePromptSystem
		}
		if tenantConv.RewritePromptUser != "" {
			rewritePromptUser = tenantConv.RewritePromptUser
		}
	}

	// Set default fallback strategy if not set
	if fallbackStrategy == "" {
		fallbackStrategy = types.FallbackStrategyFixed
		logger.Infof(ctx, "Fallback strategy not set, using default: %v", fallbackStrategy)
	}

	// Build unified search targets (computed once, used throughout pipeline)
	searchTargets, err := s.buildSearchTargets(ctx, session.TenantID, knowledgeBaseIDs, knowledgeIDs)
	if err != nil {
		logger.Warnf(ctx, "Failed to build search targets: %v", err)
	}

	// Create chat management object with session settings
	logger.Infof(
		ctx,
		"Creating chat manage object, knowledge base IDs: %v, knowledge IDs: %v, chat model ID: %s, search targets: %d",
		knowledgeBaseIDs,
		knowledgeIDs,
		chatModelID,
		len(searchTargets),
	)
	chatManage := &types.ChatManage{
		Query:                query,
		RewriteQuery:         query,
		SessionID:            session.ID,
		MessageID:            assistantMessageID, // NEW: For event emission in pipeline
		KnowledgeBaseIDs:     knowledgeBaseIDs,   // Multi-KB support
		KnowledgeIDs:         knowledgeIDs,       // Specific knowledge (file) IDs
		SearchTargets:        searchTargets,      // Pre-computed search targets
		VectorThreshold:      vectorThreshold,
		KeywordThreshold:     keywordThreshold,
		EmbeddingTopK:        embeddingTopK,
		RerankModelID:        rerankModelID,
		RerankTopK:           rerankTopK,
		RerankThreshold:      rerankThreshold,
		MaxRounds:            maxRounds,
		ChatModelID:          chatModelID,
		SummaryConfig:        summaryConfig,
		FallbackStrategy:     fallbackStrategy,
		FallbackResponse:     fallbackResponse,
		FallbackPrompt:       fallbackPrompt,
		EventBus:             eventBus.AsEventBusInterface(), // NEW: For pipeline to emit events directly
		WebSearchEnabled:     webSearchEnabled,
		TenantID:             session.TenantID,
		RewritePromptSystem:  rewritePromptSystem,
		RewritePromptUser:    rewritePromptUser,
		EnableRewrite:        enableRewrite,
		EnableQueryExpansion: enableQueryExpansion,
	}

	// Determine pipeline based on knowledge bases availability
	// If no knowledge bases are selected, use pure chat pipeline
	var pipeline []types.EventType
	if len(knowledgeBaseIDs) == 0 && len(knowledgeIDs) == 0 {
		logger.Info(ctx, "No knowledge bases selected, using chat_stream pipeline")
		pipeline = types.Pipline["chat_stream"]
		// For pure chat, UserContent is the Query (since INTO_CHAT_MESSAGE is skipped)
		chatManage.UserContent = query
	} else {
		logger.Info(ctx, "Knowledge bases selected, using rag_stream pipeline")
		pipeline = types.Pipline["rag_stream"]
	}

	// Start knowledge QA event processing
	logger.Info(ctx, "Triggering question answering event")
	err = s.KnowledgeQAByEvent(ctx, chatManage, pipeline)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id":        session.ID,
			"knowledge_base_id": session.KnowledgeBaseID,
		})
		return err
	}

	// Emit references event if we have search results
	if len(chatManage.MergeResult) > 0 {
		logger.Infof(ctx, "Emitting references event with %d results", len(chatManage.MergeResult))
		if err := eventBus.Emit(ctx, event.Event{
			ID:        generateEventID("references"),
			Type:      event.EventAgentReferences,
			SessionID: session.ID,
			Data: event.AgentReferencesData{
				References: chatManage.MergeResult,
			},
		}); err != nil {
			logger.Errorf(ctx, "Failed to emit references event: %v", err)
		}
	}

	// Note: Answer events are now emitted directly by chat_completion_stream plugin
	// Completion event will be emitted when the last answer event has Done=true
	// We can optionally add a completion watcher here if needed, but for now
	// the frontend can detect completion from the Done flag

	logger.Info(ctx, "Knowledge base question answering initiated")
	return nil
}

// selectChatModelIDWithOverride selects the appropriate chat model ID with priority for request override
// Priority order:
// 1. Request's summaryModelID (if provided and valid)
// 2. Session's SummaryModelID if it's a Remote model
// 3. First knowledge base with a Remote model
// 4. Session's SummaryModelID (if not Remote)
// 5. First knowledge base's SummaryModelID
func (s *sessionService) selectChatModelIDWithOverride(
	ctx context.Context,
	session *types.Session,
	knowledgeBaseIDs []string,
	knowledgeIDs []string,
	summaryModelID string,
) (string, error) {
	// First, check if request has summaryModelID override
	if summaryModelID != "" {
		// Validate that the model exists
		model, err := s.modelService.GetModelByID(ctx, summaryModelID)
		if err != nil {
			logger.Warnf(
				ctx,
				"Request provided invalid summary model ID %s: %v, falling back to default selection",
				summaryModelID,
				err,
			)
		} else if model != nil {
			logger.Infof(ctx, "Using request's summary model override: %s", summaryModelID)
			return summaryModelID, nil
		}
	}

	// If no valid override, use default selection logic
	return s.selectChatModelID(ctx, session, knowledgeBaseIDs, knowledgeIDs)
}

// selectChatModelID selects the appropriate chat model ID with priority for Remote models
// Priority order:
// 1. Session's SummaryModelID if it's a Remote model
// 2. First knowledge base with a Remote model (from knowledgeBaseIDs or derived from knowledgeIDs)
// 3. Session's SummaryModelID (if not Remote)
// 4. First knowledge base's SummaryModelID
func (s *sessionService) selectChatModelID(
	ctx context.Context,
	session *types.Session,
	knowledgeBaseIDs []string,
	knowledgeIDs []string,
) (string, error) {

	// First, check if session has a SummaryModelID and if it's a Remote model
	if session.SummaryModelID != "" {
		return session.SummaryModelID, nil
	}
	// If no knowledge base IDs but have knowledge IDs, derive KB IDs from knowledge IDs
	if len(knowledgeBaseIDs) == 0 && len(knowledgeIDs) > 0 {
		tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
		knowledgeList, err := s.knowledgeService.GetKnowledgeBatch(ctx, tenantID, knowledgeIDs)
		if err != nil {
			logger.Warnf(ctx, "Failed to get knowledge batch for model selection: %v", err)
		} else {
			// Collect unique KB IDs from knowledge items
			kbIDSet := make(map[string]bool)
			for _, k := range knowledgeList {
				if k != nil && k.KnowledgeBaseID != "" {
					kbIDSet[k.KnowledgeBaseID] = true
				}
			}
			for kbID := range kbIDSet {
				knowledgeBaseIDs = append(knowledgeBaseIDs, kbID)
			}
			logger.Infof(ctx, "Derived %d knowledge base IDs from %d knowledge IDs for model selection",
				len(knowledgeBaseIDs), len(knowledgeIDs))
		}
	}
	// If no Remote model found from session, check knowledge bases for Remote models
	if len(knowledgeBaseIDs) > 0 {
		// Try to find a knowledge base with Remote model
		for _, kbID := range knowledgeBaseIDs {
			kb, err := s.knowledgeBaseService.GetKnowledgeBaseByID(ctx, kbID)
			if err != nil {
				logger.Warnf(ctx, "Failed to get knowledge base: %v", err)
				continue
			}
			if kb != nil && kb.SummaryModelID != "" {
				model, err := s.modelService.GetModelByID(ctx, kb.SummaryModelID)
				if err == nil && model != nil && model.Source == types.ModelSourceRemote {
					logger.Info(ctx, "Using Remote summary model from knowledge base")
					return kb.SummaryModelID, nil
				}
			}
		}

		// If still no Remote model found, use session's SummaryModelID if available
		if session.SummaryModelID != "" {
			logger.Infof(ctx, "No Remote model found, using session's summary model: %s", session.SummaryModelID)
			return session.SummaryModelID, nil
		}

		// If still empty, use first knowledge base's model
		kb, err := s.knowledgeBaseService.GetKnowledgeBaseByID(ctx, knowledgeBaseIDs[0])
		if err != nil {
			logger.Errorf(ctx, "Failed to get knowledge base for model ID: %v", err)
			return "", fmt.Errorf("failed to get knowledge base %s: %w", knowledgeBaseIDs[0], err)
		}
		if kb != nil && kb.SummaryModelID != "" {
			logger.Infof(
				ctx,
				"Using summary model from first knowledge base %s: %s",
				knowledgeBaseIDs[0],
				kb.SummaryModelID,
			)
			return kb.SummaryModelID, nil
		} else {
			logger.Errorf(ctx, "Knowledge base %s has no summary model ID", knowledgeBaseIDs[0])
			return "", fmt.Errorf("knowledge base %s has no summary model configured", knowledgeBaseIDs[0])
		}
	}

	// No knowledge bases - use session's SummaryModelID if available
	if session.SummaryModelID != "" {
		logger.Infof(ctx, "No knowledge bases, using session's summary model: %s", session.SummaryModelID)
		return session.SummaryModelID, nil
	}

	logger.Error(ctx, "No chat model ID available")
	return "", errors.New(
		"no chat model ID available: session has no SummaryModelID and no knowledge bases configured",
	)
}

// buildSearchTargets computes the unified search targets from knowledgeBaseIDs and knowledgeIDs
// This is called once at the request entry point to avoid repeated queries later in the pipeline
// Logic:
//   - For each knowledgeBaseID: create a SearchTargetTypeKnowledgeBase target
//   - For each knowledgeID: find its knowledgeBaseID, if the KB is already in the list, skip (covered by full KB search)
//     otherwise create a SearchTargetTypeKnowledge target grouped by KB
func (s *sessionService) buildSearchTargets(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseIDs []string,
	knowledgeIDs []string,
) (types.SearchTargets, error) {
	var targets types.SearchTargets

	// Track which KBs are fully searched
	fullKBSet := make(map[string]bool)
	for _, kbID := range knowledgeBaseIDs {
		fullKBSet[kbID] = true
		targets = append(targets, &types.SearchTarget{
			Type:            types.SearchTargetTypeKnowledgeBase,
			KnowledgeBaseID: kbID,
		})
	}

	// Process individual knowledge IDs
	if len(knowledgeIDs) > 0 {
		knowledgeList, err := s.knowledgeService.GetKnowledgeBatch(ctx, tenantID, knowledgeIDs)
		if err != nil {
			logger.Warnf(ctx, "Failed to get knowledge batch for search targets: %v", err)
			return targets, nil // Return what we have, don't fail
		}

		// Group knowledge IDs by their KB, excluding those already covered by full KB search
		kbToKnowledgeIDs := make(map[string][]string)
		for _, k := range knowledgeList {
			if k == nil || k.KnowledgeBaseID == "" {
				continue
			}
			// Skip if this KB is already fully searched
			if fullKBSet[k.KnowledgeBaseID] {
				continue
			}
			kbToKnowledgeIDs[k.KnowledgeBaseID] = append(kbToKnowledgeIDs[k.KnowledgeBaseID], k.ID)
		}

		// Create SearchTargetTypeKnowledge targets for each KB with specific files
		for kbID, kidList := range kbToKnowledgeIDs {
			targets = append(targets, &types.SearchTarget{
				Type:            types.SearchTargetTypeKnowledge,
				KnowledgeBaseID: kbID,
				KnowledgeIDs:    kidList,
			})
		}
	}

	logger.Infof(ctx, "Built %d search targets: %d full KB, %d partial KB",
		len(targets), len(knowledgeBaseIDs), len(targets)-len(knowledgeBaseIDs))

	return targets, nil
}

// KnowledgeQAByEvent processes knowledge QA through a series of events in the pipeline
func (s *sessionService) KnowledgeQAByEvent(ctx context.Context,
	chatManage *types.ChatManage, eventList []types.EventType,
) error {
	ctx, span := tracing.ContextWithSpan(ctx, "SessionService.KnowledgeQAByEvent")
	defer span.End()

	logger.Info(ctx, "Start processing knowledge base question answering through events")
	logger.Infof(ctx, "Knowledge base question answering parameters, session ID: %s,  query: %s",
		chatManage.SessionID, chatManage.Query)

	// Prepare method list for logging and tracing
	methods := []string{}
	for _, event := range eventList {
		methods = append(methods, string(event))
	}

	// Set up tracing attributes
	logger.Infof(ctx, "Trigger event list: %v", methods)
	span.SetAttributes(
		attribute.String("request_id", ctx.Value(types.RequestIDContextKey).(string)),
		attribute.String("query", chatManage.Query),
		attribute.String("method", strings.Join(methods, ",")),
	)

	// Process each event in sequence
	for _, eventType := range eventList {
		logger.Infof(ctx, "Starting to trigger event: %v", eventType)
		err := s.eventManager.Trigger(ctx, eventType, chatManage)

		// Handle case where search returns no results
		if err == chatpipline.ErrSearchNothing {
			logger.Warnf(
				ctx,
				"Event %v triggered, search result is empty, using fallback response, strategy: %v",
				eventType,
				chatManage.FallbackStrategy,
			)
			s.handleFallbackResponse(ctx, chatManage)
			return nil
		}

		// Handle other errors
		if err != nil {
			logger.Errorf(ctx, "Event triggering failed, event: %v, error type: %s, description: %s, error: %v",
				eventType, err.ErrorType, err.Description, err.Err)
			span.RecordError(err.Err)
			span.SetStatus(codes.Error, err.Description)
			span.SetAttributes(attribute.String("error_type", err.ErrorType))
			return err.Err
		}
		logger.Infof(ctx, "Event %v triggered successfully", eventType)
	}

	logger.Info(ctx, "All events triggered successfully")
	return nil
}

func getTenantConversationConfig(ctx context.Context) (*types.ConversationConfig, error) {
	tenant := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	if tenant == nil {
		return nil, errors.New("tenant is empty")
	}
	if tenant.ConversationConfig == nil {
		return nil, errors.New("tenant has no conversation config")
	}
	return tenant.ConversationConfig, nil
}

// SearchKnowledge performs knowledge base search without LLM summarization
func (s *sessionService) SearchKnowledge(ctx context.Context,
	knowledgeBaseID, query string,
) ([]*types.SearchResult, error) {
	logger.Info(ctx, "Start knowledge base search without LLM summary")
	logger.Infof(ctx, "Knowledge base search parameters, knowledge base ID: %s, query: %s", knowledgeBaseID, query)

	// Create default retrieval parameters
	chatManage := &types.ChatManage{
		Query:               query,
		RewriteQuery:        query,
		VectorThreshold:     s.cfg.Conversation.VectorThreshold,  // Use default configuration
		KeywordThreshold:    s.cfg.Conversation.KeywordThreshold, // Use default configuration
		EmbeddingTopK:       s.cfg.Conversation.EmbeddingTopK,    // Use default configuration
		RerankTopK:          s.cfg.Conversation.RerankTopK,       // Use default configuration
		RerankThreshold:     s.cfg.Conversation.RerankThreshold,  // Use default configuration
		MaxRounds:           s.cfg.Conversation.MaxRounds,
		RewritePromptSystem: s.cfg.Conversation.RewritePromptSystem,
		RewritePromptUser:   s.cfg.Conversation.RewritePromptUser,
	}

	// Get default models
	models, err := s.modelService.ListModels(ctx)
	if err != nil {
		logger.Errorf(ctx, "Failed to get models: %v", err)
		return nil, err
	}

	// Find the first available rerank model
	for _, model := range models {
		if model == nil {
			continue
		}
		if model.Type == types.ModelTypeRerank {
			chatManage.RerankModelID = model.ID
			break
		}
	}

	// Use specific event list, only including retrieval-related events, not LLM summarization
	searchEvents := []types.EventType{
		types.CHUNK_SEARCH, // Vector search
		types.CHUNK_RERANK, // Rerank search results
		types.CHUNK_MERGE,  // Merge search results
		types.FILTER_TOP_K, // Filter top K results
	}

	ctx, span := tracing.ContextWithSpan(ctx, "SessionService.SearchKnowledge")
	defer span.End()

	// Prepare method list for logging and tracing
	methods := []string{}
	for _, event := range searchEvents {
		methods = append(methods, string(event))
	}

	// Set up tracing attributes
	logger.Infof(ctx, "Trigger search event list: %v", methods)
	span.SetAttributes(
		attribute.String("query", query),
		attribute.String("knowledge_base_id", knowledgeBaseID),
		attribute.String("method", strings.Join(methods, ",")),
	)

	// Process each search event in sequence
	for _, event := range searchEvents {
		logger.Infof(ctx, "Starting to trigger search event: %v", event)
		err := s.eventManager.Trigger(ctx, event, chatManage)

		// Handle case where search returns no results
		if err == chatpipline.ErrSearchNothing {
			logger.Warnf(ctx, "Event %v triggered, search result is empty", event)
			return []*types.SearchResult{}, nil
		}

		// Handle other errors
		if err != nil {
			logger.Errorf(ctx, "Event triggering failed, event: %v, error type: %s, description: %s, error: %v",
				event, err.ErrorType, err.Description, err.Err)
			span.RecordError(err.Err)
			span.SetStatus(codes.Error, err.Description)
			span.SetAttributes(attribute.String("error_type", err.ErrorType))
			return nil, err.Err
		}
		logger.Infof(ctx, "Event %v triggered successfully", event)
	}

	logger.Infof(ctx, "Knowledge base search completed, found %d results", len(chatManage.MergeResult))
	return chatManage.MergeResult, nil
}

// AgentQA performs agent-based question answering with conversation history and streaming support
func (s *sessionService) AgentQA(
	ctx context.Context,
	session *types.Session,
	query string,
	assistantMessageID string,
	eventBus *event.EventBus,
) error {
	sessionID := session.ID
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	sessionJSON, err := json.Marshal(session)
	if err != nil {
		logger.Errorf(ctx, "Failed to marshal session, session ID: %s, error: %v", sessionID, err)
		return fmt.Errorf("failed to marshal session: %w", err)
	}
	logger.Infof(ctx, "Start agent-based question answering, session ID: %s, tenant ID: %d, query: %s, session: %s",
		sessionID, tenantID, query, string(sessionJSON))

	// Build effective agent configuration by merging session and tenant configs
	// Session-level config: Enabled, KnowledgeBases (stored in session.AgentConfig)
	// Tenant-level config: MaxIterations, Temperature, Models, Tools, etc. (from tenant.AgentConfig)

	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)

	// Check if agent is enabled at session level
	if session.AgentConfig == nil {
		logger.Warnf(ctx, "Agent config not found for session: %s", sessionID)
		return errors.New("agent config not found for session")
	}

	// Check if tenant has agent configuration
	if tenantInfo.AgentConfig == nil {
		tenantInfo.AgentConfig = &types.AgentConfig{
			MaxIterations:           agent.DefaultAgentMaxIterations,
			ReflectionEnabled:       agent.DefaultAgentReflectionEnabled,
			AllowedTools:            tools.DefaultAllowedTools(),
			Temperature:             agent.DefaultAgentTemperature,
			SystemPromptWebEnabled:  agent.ProgressiveRAGSystemPromptWithWeb,
			SystemPromptWebDisabled: agent.ProgressiveRAGSystemPromptWithoutWeb,
			UseCustomSystemPrompt:   agent.DefaultUseCustomSystemPrompt,
		}
	}

	// Create runtime AgentConfig by merging session and tenant configs
	// Tenant config provides the runtime parameters (MaxIterations, Temperature, Tools, Models)
	// Session config provides KnowledgeBases and KnowledgeIDs
	agentConfig := &types.AgentConfig{
		MaxIterations:     tenantInfo.AgentConfig.MaxIterations,
		ReflectionEnabled: tenantInfo.AgentConfig.ReflectionEnabled,
		AllowedTools:      tools.DefaultAllowedTools(),
		Temperature:       tenantInfo.AgentConfig.Temperature,
		KnowledgeBases:    session.AgentConfig.KnowledgeBases,   // Use session's knowledge bases
		KnowledgeIDs:      session.AgentConfig.KnowledgeIDs,     // Use session's knowledge IDs (individual documents)
		WebSearchEnabled:  session.AgentConfig.WebSearchEnabled, // Web search enabled from session config
	}

	agentConfig.UseCustomSystemPrompt = tenantInfo.AgentConfig.UseCustomSystemPrompt
	if agentConfig.UseCustomSystemPrompt {
		agentConfig.SystemPromptWebEnabled = tenantInfo.AgentConfig.ResolveSystemPrompt(true)
		agentConfig.SystemPromptWebDisabled = tenantInfo.AgentConfig.ResolveSystemPrompt(false)
	}

	// Set web search max results from tenant config (default: 5)
	agentConfig.WebSearchMaxResults = 5
	if tenantInfo.WebSearchConfig != nil && tenantInfo.WebSearchConfig.MaxResults > 0 {
		agentConfig.WebSearchMaxResults = tenantInfo.WebSearchConfig.MaxResults
	}

	logger.Infof(ctx, "Merged agent config from tenant %d and session %s", tenantInfo.ID, sessionID)

	// Log knowledge IDs if present
	if len(agentConfig.KnowledgeIDs) > 0 {
		logger.Infof(ctx, "Agent configured with %d individual knowledge ID(s): %v",
			len(agentConfig.KnowledgeIDs), agentConfig.KnowledgeIDs)
	}

	// Determine knowledge bases for agent
	// Priority: Session.AgentConfig.KnowledgeBases > Session.KnowledgeBaseID > All tenant knowledge bases
	// Exception: If KnowledgeIDs are specified, don't auto-add all KBs (let buildSearchTargets handle it)
	if len(agentConfig.KnowledgeBases) == 0 && len(agentConfig.KnowledgeIDs) == 0 {
		if session.KnowledgeBaseID != "" {
			// Use session's knowledge base as fallback
			agentConfig.KnowledgeBases = []string{session.KnowledgeBaseID}
			logger.Infof(ctx, "Using session's knowledge base for agent: %s", session.KnowledgeBaseID)
		} else {
			// Allow running without knowledge bases (Pure Agent mode)
			logger.Infof(ctx, "No knowledge bases specified for agent, running in pure agent mode")
		}
	} else if len(agentConfig.KnowledgeIDs) > 0 && len(agentConfig.KnowledgeBases) == 0 {
		// User specified individual files but no KBs - don't auto-add all KBs
		logger.Infof(ctx, "Agent configured with %d individual knowledge ID(s), no KB auto-expansion",
			len(agentConfig.KnowledgeIDs))
	} else {
		logger.Infof(ctx, "Agent configured with %d knowledge base(s): %v",
			len(agentConfig.KnowledgeBases), agentConfig.KnowledgeBases)
	}

	// Build search targets for agent (pre-compute once to avoid repeated queries)
	searchTargets, err := s.buildSearchTargets(ctx, tenantInfo.ID, agentConfig.KnowledgeBases, agentConfig.KnowledgeIDs)
	if err != nil {
		logger.Warnf(ctx, "Failed to build search targets for agent: %v", err)
		// Continue without search targets, the tool will handle empty targets
	}
	agentConfig.SearchTargets = searchTargets
	logger.Infof(ctx, "Agent search targets built: %d targets", len(searchTargets))

	summaryModelID := session.SummaryModelID
	if summaryModelID == "" && tenantInfo.ConversationConfig != nil {
		summaryModelID = tenantInfo.ConversationConfig.SummaryModelID
	}
	if summaryModelID == "" {
		logger.Warnf(ctx, "No summary model configured for tenant %d or session %s", tenantInfo.ID, session.ID)
		return errors.New("summary model is not configured in conversation settings")
	}

	summaryModel, err := s.modelService.GetChatModel(ctx, summaryModelID)
	if err != nil {
		logger.Warnf(ctx, "Failed to get chat model: %v", err)
		return fmt.Errorf("failed to get chat model: %w", err)
	}

	rerankModelID := session.RerankModelID
	if rerankModelID == "" && tenantInfo.ConversationConfig != nil {
		rerankModelID = tenantInfo.ConversationConfig.RerankModelID
	}
	if rerankModelID == "" {
		logger.Warnf(ctx, "No rerank model configured for tenant %d or session %s", tenantInfo.ID, session.ID)
		return errors.New("rerank model is not configured in conversation settings")
	}

	rerankModel, err := s.modelService.GetRerankModel(ctx, rerankModelID)
	if err != nil {
		logger.Warnf(ctx, "Failed to get rerank model: %v", err)
		return fmt.Errorf("failed to get rerank model: %w", err)
	}

	// Get or create contextManager for this session
	contextManager := s.getContextManagerForSession(ctx, session, summaryModel)
	// Get LLM context from context manager
	llmContext, err := s.getContextForSession(ctx, contextManager, sessionID)
	if err != nil {
		logger.Warnf(ctx, "Failed to get LLM context: %v, continuing without history", err)
		llmContext = []chat.Message{}
	}
	logger.Infof(ctx, "Loaded %d messages from LLM context manager", len(llmContext))

	// Create agent engine with EventBus and ContextManager
	logger.Info(ctx, "Creating agent engine")
	engine, err := s.agentService.CreateAgentEngine(
		ctx,
		agentConfig,
		summaryModel,
		rerankModel,
		eventBus,
		contextManager,
		session.ID,
		s,
	)
	if err != nil {
		logger.Errorf(ctx, "Failed to create agent engine: %v", err)
		return err
	}

	// Execute agent with streaming (asynchronously)
	// Events will be emitted to EventBus and handled by the Handler layer
	logger.Info(ctx, "Executing agent with streaming")
	if _, err := engine.Execute(ctx, sessionID, assistantMessageID, query, llmContext); err != nil {
		logger.Errorf(ctx, "Agent execution failed: %v", err)
		// Emit error event to the EventBus used by this agent
		eventBus.Emit(ctx, event.Event{
			Type:      event.EventError,
			SessionID: sessionID,
			Data: event.ErrorData{
				Error:     err.Error(),
				Stage:     "agent_execution",
				SessionID: sessionID,
			},
		})
	}
	// Return empty - events will be handled by Handler via EventBus subscription
	return nil
}

// getContextManagerForSession creates a context manager for the session based on configuration
// Returns the configured context manager (tenant-level or session-level) or default
func (s *sessionService) getContextManagerForSession(
	ctx context.Context,
	session *types.Session,
	chatModel chat.Chat,
) interfaces.ContextManager {
	// Get tenant to access global context configuration
	tenant, _ := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	// Determine which context config to use: session-specific or tenant-level
	var contextConfig *types.ContextConfig
	if session.ContextConfig != nil {
		// Use session-specific configuration
		contextConfig = session.ContextConfig
		logger.Infof(ctx, "Using session-specific context config for session %s", session.ID)
	} else if tenant.ContextConfig != nil {
		// Use tenant-level configuration
		contextConfig = tenant.ContextConfig
		logger.Infof(ctx, "Using tenant-level context config for session %s", session.ID)
	} else {
		// Use service's default context manager
		logger.Debugf(ctx, "Using default context manager for session %s", session.ID)
		contextConfig = &types.ContextConfig{
			MaxTokens:           llmcontext.DefaultMaxTokens,
			CompressionStrategy: llmcontext.DefaultCompressionStrategy,
			RecentMessageCount:  llmcontext.DefaultRecentMessageCount,
			SummarizeThreshold:  llmcontext.DefaultSummarizeThreshold,
		}
	}
	return llmcontext.NewContextManagerFromConfig(contextConfig, s.sessionStorage, chatModel)
}

// getContextForSession retrieves LLM context for a session
func (s *sessionService) getContextForSession(
	ctx context.Context,
	contextManager interfaces.ContextManager,
	sessionID string,
) ([]chat.Message, error) {
	history, err := contextManager.GetContext(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get context: %w", err)
	}

	// Log context statistics
	stats, _ := contextManager.GetContextStats(ctx, sessionID)
	if stats != nil {
		logger.Infof(ctx, "LLM context stats for session %s: messages=%d, tokens=~%d, compressed=%v",
			sessionID, stats.MessageCount, stats.TokenCount, stats.IsCompressed)
	}

	return history, nil
}

// ClearContext clears the LLM context for a session
// This is useful when switching knowledge bases or agent modes to prevent context contamination
func (s *sessionService) ClearContext(ctx context.Context, sessionID string) error {
	logger.Infof(ctx, "Clearing context for session: %s", sessionID)
	return s.sessionStorage.Delete(ctx, sessionID)
}

// GetWebSearchTempKBState retrieves the temporary KB state for web search from Redis
func (s *sessionService) GetWebSearchTempKBState(
	ctx context.Context,
	sessionID string,
) (tempKBID string, seenURLs map[string]bool, knowledgeIDs []string) {
	stateKey := fmt.Sprintf("tempkb:%s", sessionID)
	if raw, getErr := s.redisClient.Get(ctx, stateKey).Bytes(); getErr == nil && len(raw) > 0 {
		var state struct {
			KBID         string          `json:"kbID"`
			KnowledgeIDs []string        `json:"knowledgeIDs"`
			SeenURLs     map[string]bool `json:"seenURLs"`
		}
		if err := json.Unmarshal(raw, &state); err == nil {
			tempKBID = state.KBID
			ids := state.KnowledgeIDs
			if state.SeenURLs != nil {
				seenURLs = state.SeenURLs
			} else {
				seenURLs = make(map[string]bool)
			}
			return tempKBID, seenURLs, ids
		}
	}
	return "", make(map[string]bool), []string{}
}

// SaveWebSearchTempKBState saves the temporary KB state for web search to Redis
func (s *sessionService) SaveWebSearchTempKBState(
	ctx context.Context,
	sessionID string,
	tempKBID string,
	seenURLs map[string]bool,
	knowledgeIDs []string,
) {
	stateKey := fmt.Sprintf("tempkb:%s", sessionID)
	state := struct {
		KBID         string          `json:"kbID"`
		KnowledgeIDs []string        `json:"knowledgeIDs"`
		SeenURLs     map[string]bool `json:"seenURLs"`
	}{
		KBID:         tempKBID,
		KnowledgeIDs: knowledgeIDs,
		SeenURLs:     seenURLs,
	}
	if b, err := json.Marshal(state); err == nil {
		_ = s.redisClient.Set(ctx, stateKey, b, 0).Err()
	}
}

// DeleteWebSearchTempKBState deletes the temporary KB state for web search from Redis
// and cleans up associated knowledge base and knowledge items.
func (s *sessionService) DeleteWebSearchTempKBState(ctx context.Context, sessionID string) error {
	if s.redisClient == nil {
		return nil
	}

	stateKey := fmt.Sprintf("tempkb:%s", sessionID)
	raw, getErr := s.redisClient.Get(ctx, stateKey).Bytes()
	if getErr != nil || len(raw) == 0 {
		// No state found, nothing to clean up
		return nil
	}

	var state struct {
		KBID         string          `json:"kbID"`
		KnowledgeIDs []string        `json:"knowledgeIDs"`
		SeenURLs     map[string]bool `json:"seenURLs"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		// Invalid state, just delete the key
		_ = s.redisClient.Del(ctx, stateKey).Err()
		return nil
	}

	// If KBID is empty, just delete the Redis key
	if strings.TrimSpace(state.KBID) == "" {
		_ = s.redisClient.Del(ctx, stateKey).Err()
		return nil
	}

	logger.Infof(ctx, "Cleaning temporary KB for session %s: %s", sessionID, state.KBID)

	// Delete all knowledge items
	for _, kid := range state.KnowledgeIDs {
		if delErr := s.knowledgeService.DeleteKnowledge(ctx, kid); delErr != nil {
			logger.Warnf(ctx, "Failed to delete temp knowledge %s: %v", kid, delErr)
		}
	}

	// Delete the knowledge base
	if delErr := s.knowledgeBaseService.DeleteKnowledgeBase(ctx, state.KBID); delErr != nil {
		logger.Warnf(ctx, "Failed to delete temp knowledge base %s: %v", state.KBID, delErr)
	}

	// Delete the Redis key
	if delErr := s.redisClient.Del(ctx, stateKey).Err(); delErr != nil {
		logger.Warnf(ctx, "Failed to delete Redis key %s: %v", stateKey, delErr)
		return fmt.Errorf("failed to delete Redis key: %w", delErr)
	}

	logger.Infof(ctx, "Successfully cleaned up temporary KB for session %s", sessionID)
	return nil
}

// handleFallbackResponse handles fallback response based on strategy
func (s *sessionService) handleFallbackResponse(ctx context.Context, chatManage *types.ChatManage) {
	if chatManage.FallbackStrategy == types.FallbackStrategyModel {
		s.handleModelFallback(ctx, chatManage)
	} else {
		s.handleFixedFallback(ctx, chatManage)
	}
}

// handleFixedFallback handles fixed fallback response
func (s *sessionService) handleFixedFallback(ctx context.Context, chatManage *types.ChatManage) {
	fallbackContent := chatManage.FallbackResponse
	chatManage.ChatResponse = &types.ChatResponse{Content: fallbackContent}
	s.emitFallbackAnswer(ctx, chatManage, fallbackContent)
}

// handleModelFallback handles model-based fallback response using streaming
func (s *sessionService) handleModelFallback(ctx context.Context, chatManage *types.ChatManage) {
	// Check if FallbackPrompt is available
	if chatManage.FallbackPrompt == "" {
		logger.Warnf(ctx, "Fallback strategy is 'model' but FallbackPrompt is empty, falling back to fixed response")
		s.handleFixedFallback(ctx, chatManage)
		return
	}

	// Render template with Query variable
	promptContent, err := s.renderFallbackPrompt(ctx, chatManage)
	if err != nil {
		logger.Errorf(ctx, "Failed to render fallback prompt: %v, falling back to fixed response", err)
		s.handleFixedFallback(ctx, chatManage)
		return
	}

	// Check if EventBus is available for streaming
	if chatManage.EventBus == nil {
		logger.Warnf(ctx, "EventBus not available for streaming fallback, falling back to fixed response")
		s.handleFixedFallback(ctx, chatManage)
		return
	}

	// Get chat model
	chatModel, err := s.modelService.GetChatModel(ctx, chatManage.ChatModelID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get chat model for fallback: %v, falling back to fixed response", err)
		s.handleFixedFallback(ctx, chatManage)
		return
	}

	// Prepare chat options
	thinking := false
	opt := &chat.ChatOptions{
		Temperature:         chatManage.SummaryConfig.Temperature,
		MaxCompletionTokens: chatManage.SummaryConfig.MaxCompletionTokens,
		Thinking:            &thinking,
	}

	// Start streaming response
	responseChan, err := chatModel.ChatStream(ctx, []chat.Message{
		{Role: "user", Content: promptContent},
	}, opt)
	if err != nil {
		logger.Errorf(ctx, "Failed to start streaming fallback response: %v, falling back to fixed response", err)
		s.handleFixedFallback(ctx, chatManage)
		return
	}

	if responseChan == nil {
		logger.Errorf(ctx, "Chat stream returned nil channel, falling back to fixed response")
		s.handleFixedFallback(ctx, chatManage)
		return
	}

	// Start goroutine to consume stream and emit events
	go s.consumeFallbackStream(ctx, chatManage, responseChan)
}

// renderFallbackPrompt renders the fallback prompt template with Query variable
func (s *sessionService) renderFallbackPrompt(ctx context.Context, chatManage *types.ChatManage) (string, error) {
	tmpl, err := template.New("fallbackPrompt").Parse(chatManage.FallbackPrompt)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var promptContent bytes.Buffer
	err = tmpl.Execute(&promptContent, map[string]interface{}{
		"Query": chatManage.Query,
	})
	if err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return promptContent.String(), nil
}

// consumeFallbackStream consumes the streaming response and emits events
func (s *sessionService) consumeFallbackStream(
	ctx context.Context,
	chatManage *types.ChatManage,
	responseChan <-chan types.StreamResponse,
) {
	fallbackID := generateEventID("fallback")
	eventBus := chatManage.EventBus
	var finalContent string
	streamCompleted := false

	for response := range responseChan {
		// Emit event for each answer chunk
		if response.ResponseType == types.ResponseTypeAnswer {
			finalContent += response.Content
			if err := eventBus.Emit(ctx, types.Event{
				ID:        fallbackID,
				Type:      types.EventType(event.EventAgentFinalAnswer),
				SessionID: chatManage.SessionID,
				Data: event.AgentFinalAnswerData{
					Content: response.Content,
					Done:    response.Done,
				},
			}); err != nil {
				logger.Errorf(ctx, "Failed to emit fallback answer chunk event: %v", err)
			}

			// Update ChatResponse with final content when done
			if response.Done {
				chatManage.ChatResponse = &types.ChatResponse{Content: finalContent}
				streamCompleted = true
				logger.Infof(ctx, "Fallback streaming response completed")
				break
			}
		}
	}

	// If channel closed without Done=true, emit final event with fixed response
	if !streamCompleted {
		logger.Warnf(ctx, "Fallback stream closed without completion, emitting final event with fixed response")
		s.emitFallbackAnswer(ctx, chatManage, chatManage.FallbackResponse)
	}
}

// emitFallbackAnswer emits fallback answer event
func (s *sessionService) emitFallbackAnswer(ctx context.Context, chatManage *types.ChatManage, content string) {
	if chatManage.EventBus == nil {
		return
	}

	fallbackID := generateEventID("fallback")
	if err := chatManage.EventBus.Emit(ctx, types.Event{
		ID:        fallbackID,
		Type:      types.EventType(event.EventAgentFinalAnswer),
		SessionID: chatManage.SessionID,
		Data: event.AgentFinalAnswerData{
			Content: content,
			Done:    true,
		},
	}); err != nil {
		logger.Errorf(ctx, "Failed to emit fallback answer event: %v", err)
	} else {
		logger.Infof(ctx, "Fallback answer event emitted successfully")
	}
}
