package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// SessionHandler handles all HTTP requests related to conversation sessions
type SessionHandler struct {
	messageService       interfaces.MessageService // Service for managing messages
	sessionService       interfaces.SessionService // Service for managing sessions
	streamManager        interfaces.StreamManager  // Manager for handling streaming responses
	config               *config.Config            // Application configuration
	knowledgebaseService interfaces.KnowledgeBaseService
}

// NewSessionHandler creates a new instance of SessionHandler with all necessary dependencies
func NewSessionHandler(
	sessionService interfaces.SessionService,
	messageService interfaces.MessageService,
	streamManager interfaces.StreamManager,
	config *config.Config,
	knowledgebaseService interfaces.KnowledgeBaseService,
) *SessionHandler {
	return &SessionHandler{
		sessionService:       sessionService,
		messageService:       messageService,
		streamManager:        streamManager,
		config:               config,
		knowledgebaseService: knowledgebaseService,
	}
}

// SessionStrategy defines the configuration for a conversation session strategy
type SessionStrategy struct {
	// Maximum number of conversation rounds to maintain
	MaxRounds int `json:"max_rounds"`
	// Whether to enable query rewrite for multi-round conversations
	EnableRewrite bool `json:"enable_rewrite"`
	// Strategy to use when no relevant knowledge is found
	FallbackStrategy types.FallbackStrategy `json:"fallback_strategy"`
	// Fixed response content for fallback
	FallbackResponse string `json:"fallback_response"`
	// Number of top results to retrieve from vector search
	EmbeddingTopK int `json:"embedding_top_k"`
	// Threshold for keyword-based retrieval
	KeywordThreshold float64 `json:"keyword_threshold"`
	// Threshold for vector-based retrieval
	VectorThreshold float64 `json:"vector_threshold"`
	// ID of the model used for reranking results
	RerankModelID string `json:"rerank_model_id"`
	// Number of top results after reranking
	RerankTopK int `json:"rerank_top_k"`
	// Threshold for reranking results
	RerankThreshold float64 `json:"rerank_threshold"`
	// ID of the model used for summarization
	SummaryModelID string `json:"summary_model_id"`
	// Parameters for the summary model
	SummaryParameters *types.SummaryConfig `json:"summary_parameters" gorm:"type:json"`
	// Prefix for responses when no match is found
	NoMatchPrefix string `json:"no_match_prefix"`
}

// CreateSessionRequest represents a request to create a new session
type CreateSessionRequest struct {
	// ID of the associated knowledge base
	KnowledgeBaseID string `json:"knowledge_base_id" binding:"required"`
	// Session strategy configuration
	SessionStrategy *SessionStrategy `json:"session_strategy"`
}

// CreateSession handles the creation of a new conversation session
func (h *SessionHandler) CreateSession(c *gin.Context) {
	ctx := c.Request.Context()

	// logger.Infof(ctx, "Start creating session, config: %+v", h.config.Conversation)

	// Parse and validate the request body
	var request CreateSessionRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.Error(ctx, "Failed to validate session creation parameters", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// Get tenant ID from context
	tenantID, exists := c.Get(types.TenantIDContextKey.String())
	if !exists {
		logger.Error(ctx, "Failed to get tenant ID")
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	// Validate knowledge base ID
	if request.KnowledgeBaseID == "" {
		logger.Error(ctx, "Knowledge base ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge base cannot be empty"))
		return
	}

	logger.Infof(
		ctx,
		"Processing session creation request, tenant ID: %d, knowledge base ID: %s",
		tenantID.(uint),
		request.KnowledgeBaseID,
	)

	// Create session object with base properties
	createdSession := &types.Session{
		TenantID:        tenantID.(uint),
		KnowledgeBaseID: request.KnowledgeBaseID,
	}

	// If summary model parameters are empty, set defaults
	if request.SessionStrategy != nil {
		createdSession.RerankModelID = request.SessionStrategy.RerankModelID
		createdSession.SummaryModelID = request.SessionStrategy.SummaryModelID
		createdSession.MaxRounds = request.SessionStrategy.MaxRounds
		createdSession.EnableRewrite = request.SessionStrategy.EnableRewrite
		createdSession.FallbackStrategy = request.SessionStrategy.FallbackStrategy
		createdSession.FallbackResponse = request.SessionStrategy.FallbackResponse
		createdSession.EmbeddingTopK = request.SessionStrategy.EmbeddingTopK
		createdSession.KeywordThreshold = request.SessionStrategy.KeywordThreshold
		createdSession.VectorThreshold = request.SessionStrategy.VectorThreshold
		createdSession.RerankTopK = request.SessionStrategy.RerankTopK
		createdSession.RerankThreshold = request.SessionStrategy.RerankThreshold
		if request.SessionStrategy.SummaryParameters != nil {
			createdSession.SummaryParameters = request.SessionStrategy.SummaryParameters
		} else {
			createdSession.SummaryParameters = &types.SummaryConfig{
				MaxTokens:           h.config.Conversation.Summary.MaxTokens,
				TopP:                h.config.Conversation.Summary.TopP,
				TopK:                h.config.Conversation.Summary.TopK,
				FrequencyPenalty:    h.config.Conversation.Summary.FrequencyPenalty,
				PresencePenalty:     h.config.Conversation.Summary.PresencePenalty,
				RepeatPenalty:       h.config.Conversation.Summary.RepeatPenalty,
				NoMatchPrefix:       h.config.Conversation.Summary.NoMatchPrefix,
				Temperature:         h.config.Conversation.Summary.Temperature,
				Seed:                h.config.Conversation.Summary.Seed,
				MaxCompletionTokens: h.config.Conversation.Summary.MaxCompletionTokens,
			}
		}
		if createdSession.SummaryParameters.Prompt == "" {
			createdSession.SummaryParameters.Prompt = h.config.Conversation.Summary.Prompt
		}
		if createdSession.SummaryParameters.ContextTemplate == "" {
			createdSession.SummaryParameters.ContextTemplate = h.config.Conversation.Summary.ContextTemplate
		}
		if createdSession.SummaryParameters.NoMatchPrefix == "" {
			createdSession.SummaryParameters.NoMatchPrefix = h.config.Conversation.Summary.NoMatchPrefix
		}

		logger.Debug(ctx, "Custom session strategy set")
	} else {
		// Use default configuration from global config
		createdSession.MaxRounds = h.config.Conversation.MaxRounds
		createdSession.EnableRewrite = h.config.Conversation.EnableRewrite
		createdSession.FallbackStrategy = types.FallbackStrategy(h.config.Conversation.FallbackStrategy)
		createdSession.FallbackResponse = h.config.Conversation.FallbackResponse
		createdSession.EmbeddingTopK = h.config.Conversation.EmbeddingTopK
		createdSession.KeywordThreshold = h.config.Conversation.KeywordThreshold
		createdSession.VectorThreshold = h.config.Conversation.VectorThreshold
		createdSession.RerankThreshold = h.config.Conversation.RerankThreshold
		createdSession.RerankTopK = h.config.Conversation.RerankTopK
		createdSession.SummaryParameters = &types.SummaryConfig{
			MaxTokens:           h.config.Conversation.Summary.MaxTokens,
			TopP:                h.config.Conversation.Summary.TopP,
			TopK:                h.config.Conversation.Summary.TopK,
			FrequencyPenalty:    h.config.Conversation.Summary.FrequencyPenalty,
			PresencePenalty:     h.config.Conversation.Summary.PresencePenalty,
			RepeatPenalty:       h.config.Conversation.Summary.RepeatPenalty,
			Prompt:              h.config.Conversation.Summary.Prompt,
			ContextTemplate:     h.config.Conversation.Summary.ContextTemplate,
			NoMatchPrefix:       h.config.Conversation.Summary.NoMatchPrefix,
			Temperature:         h.config.Conversation.Summary.Temperature,
			Seed:                h.config.Conversation.Summary.Seed,
			MaxCompletionTokens: h.config.Conversation.Summary.MaxCompletionTokens,
		}

		logger.Debug(ctx, "Using default session strategy")
	}

	kb, err := h.knowledgebaseService.GetKnowledgeBaseByID(ctx, request.KnowledgeBaseID)
	if err != nil {
		logger.Error(ctx, "Failed to get knowledge base", err)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// Get model IDs from knowledge base if not provided
	if createdSession.SummaryModelID == "" {
		createdSession.SummaryModelID = kb.SummaryModelID
	}
	if createdSession.RerankModelID == "" {
		createdSession.RerankModelID = kb.RerankModelID
	}

	// Call service to create session
	logger.Infof(ctx, "Calling session service to create session")
	createdSession, err = h.sessionService.CreateSession(ctx, createdSession)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// Return created session
	logger.Infof(ctx, "Session created successfully, ID: %s", createdSession.ID)
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    createdSession,
	})
}

// GetSession retrieves a session by its ID
func (h *SessionHandler) GetSession(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start retrieving session")

	// Get session ID from URL parameter
	id := c.Param("id")
	if id == "" {
		logger.Error(ctx, "Session ID is empty")
		c.Error(errors.NewBadRequestError(errors.ErrInvalidSessionID.Error()))
		return
	}

	// Call service to get session details
	logger.Infof(ctx, "Retrieving session, ID: %s", id)
	session, err := h.sessionService.GetSession(ctx, id)
	if err != nil {
		if err == errors.ErrSessionNotFound {
			logger.Warnf(ctx, "Session not found, ID: %s", id)
			c.Error(errors.NewNotFoundError(err.Error()))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// Return session data
	logger.Infof(ctx, "Session retrieved successfully, ID: %s", id)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    session,
	})
}

// GetSessionsByTenant retrieves all sessions for the current tenant with pagination
func (h *SessionHandler) GetSessionsByTenant(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start retrieving all sessions for tenant")

	// Parse pagination parameters from query
	var pagination types.Pagination
	if err := c.ShouldBindQuery(&pagination); err != nil {
		logger.Error(ctx, "Failed to parse pagination parameters", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	logger.Debugf(ctx, "Using pagination parameters: page=%d, page_size=%d", pagination.Page, pagination.PageSize)

	// Use paginated query to get sessions
	result, err := h.sessionService.GetPagedSessionsByTenant(ctx, &pagination)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// Return sessions with pagination data
	logger.Infof(ctx, "Successfully retrieved tenant sessions, total: %d", result.Total)
	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"data":      result.Data,
		"total":     result.Total,
		"page":      result.Page,
		"page_size": result.PageSize,
	})
}

// UpdateSession updates an existing session's properties
func (h *SessionHandler) UpdateSession(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start updating session")

	// Get session ID from URL parameter
	id := c.Param("id")
	if id == "" {
		logger.Error(ctx, "Session ID is empty")
		c.Error(errors.NewBadRequestError(errors.ErrInvalidSessionID.Error()))
		return
	}

	// Verify tenant ID from context for authorization
	tenantID, exists := c.Get(types.TenantIDContextKey.String())
	if !exists {
		logger.Error(ctx, "Failed to get tenant ID")
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	// Parse request body to session object
	var session types.Session
	if err := c.ShouldBindJSON(&session); err != nil {
		logger.Error(ctx, "Failed to parse session data", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// Set session ID and tenant ID
	logger.Infof(ctx, "Updating session, ID: %s, tenant ID: %d", id, tenantID.(uint))
	session.ID = id
	session.TenantID = tenantID.(uint)

	// Call service to update session
	if err := h.sessionService.UpdateSession(ctx, &session); err != nil {
		if err == errors.ErrSessionNotFound {
			logger.Warnf(ctx, "Session not found, ID: %s", id)
			c.Error(errors.NewNotFoundError(err.Error()))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// Return updated session
	logger.Infof(ctx, "Session updated successfully, ID: %s", id)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    session,
	})
}

// DeleteSession deletes a session by its ID
func (h *SessionHandler) DeleteSession(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start deleting session")

	// Get session ID from URL parameter
	id := c.Param("id")
	if id == "" {
		logger.Error(ctx, "Session ID is empty")
		c.Error(errors.NewBadRequestError(errors.ErrInvalidSessionID.Error()))
		return
	}

	// Call service to delete session
	logger.Infof(ctx, "Deleting session, ID: %s", id)
	if err := h.sessionService.DeleteSession(ctx, id); err != nil {
		if err == errors.ErrSessionNotFound {
			logger.Warnf(ctx, "Session not found, ID: %s", id)
			c.Error(errors.NewNotFoundError(err.Error()))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// Return success message
	logger.Infof(ctx, "Session deleted successfully, ID: %s", id)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Session deleted successfully",
	})
}

// GenerateTitleRequest defines the request structure for generating a session title
type GenerateTitleRequest struct {
	Messages []types.Message `json:"messages" binding:"required"` // Messages to use as context for title generation
}

// GenerateTitle generates a title for a session based on message content
func (h *SessionHandler) GenerateTitle(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start generating session title")

	// Get session ID from URL parameter
	sessionID := c.Param("session_id")
	if sessionID == "" {
		logger.Error(ctx, "Session ID is empty")
		c.Error(errors.NewBadRequestError(errors.ErrInvalidSessionID.Error()))
		return
	}

	// Parse request body
	var request GenerateTitleRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.Error(ctx, "Failed to parse request data", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// Call service to generate title
	logger.Infof(ctx, "Generating session title, session ID: %s, message count: %d", sessionID, len(request.Messages))
	title, err := h.sessionService.GenerateTitle(ctx, sessionID, request.Messages)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// Return generated title
	logger.Infof(ctx, "Session title generated successfully, session ID: %s, title: %s", sessionID, title)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    title,
	})
}

// CreateKnowledgeQARequest defines the request structure for knowledge QA
type CreateKnowledgeQARequest struct {
	Query string `json:"query" binding:"required"` // Query text for knowledge base search
}

// SearchKnowledgeRequest defines the request structure for searching knowledge without LLM summarization
type SearchKnowledgeRequest struct {
	Query           string `json:"query" binding:"required"`             // Query text to search for
	KnowledgeBaseID string `json:"knowledge_base_id" binding:"required"` // ID of the knowledge base to search
}

// SearchKnowledge performs knowledge base search without LLM summarization
func (h *SessionHandler) SearchKnowledge(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	logger.Info(ctx, "Start processing knowledge search request")

	// Parse request body
	var request SearchKnowledgeRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.Error(ctx, "Failed to parse request data", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// Validate request parameters
	if request.Query == "" {
		logger.Error(ctx, "Query content is empty")
		c.Error(errors.NewBadRequestError("Query content cannot be empty"))
		return
	}

	if request.KnowledgeBaseID == "" {
		logger.Error(ctx, "Knowledge base ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge base ID cannot be empty"))
		return
	}

	logger.Infof(
		ctx,
		"Knowledge search request, knowledge base ID: %s, query: %s",
		request.KnowledgeBaseID,
		request.Query,
	)

	// Directly call knowledge retrieval service without LLM summarization
	searchResults, err := h.sessionService.SearchKnowledge(ctx, request.KnowledgeBaseID, request.Query)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge search completed, found %d results", len(searchResults))

	// Return search results
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    searchResults,
	})
}

// ContinueStream handles continued streaming of an active response stream
func (h *SessionHandler) ContinueStream(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start continuing stream response processing")

	// Get session ID from URL parameter
	sessionID := c.Param("session_id")
	if sessionID == "" {
		logger.Error(ctx, "Session ID is empty")
		c.Error(errors.NewBadRequestError(errors.ErrInvalidSessionID.Error()))
		return
	}

	// Get message ID from query parameter
	messageID := c.Query("message_id")
	if messageID == "" {
		logger.Error(ctx, "Message ID is empty")
		c.Error(errors.NewBadRequestError("Missing message ID"))
		return
	}

	logger.Infof(ctx, "Continuing stream, session ID: %s, message ID: %s", sessionID, messageID)

	// Verify that the session exists and belongs to this tenant
	_, err := h.sessionService.GetSession(ctx, sessionID)
	if err != nil {
		if err == errors.ErrSessionNotFound {
			logger.Warnf(ctx, "Session not found, ID: %s", sessionID)
			c.Error(errors.NewNotFoundError(err.Error()))
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError(err.Error()))
		}
		return
	}

	// Get the incomplete message
	message, err := h.messageService.GetMessage(ctx, sessionID, messageID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	if message == nil {
		logger.Warnf(ctx, "Incomplete message not found, session ID: %s, message ID: %s", sessionID, messageID)
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "Incomplete message not found",
		})
		return
	}

	// Get stream information
	streamInfo, err := h.streamManager.GetStream(ctx, sessionID, messageID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(fmt.Sprintf("Failed to get stream data: %s", err.Error())))
		return
	}

	if streamInfo == nil {
		logger.Warnf(ctx, "Active stream not found, session ID: %s, message ID: %s", sessionID, messageID)
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "Active stream not found",
		})
		return
	}

	// If stream is already completed, return the full message
	if streamInfo.IsCompleted {
		logger.Infof(
			ctx, "Stream already completed, returning directly, session ID: %s, message ID: %s", sessionID, messageID,
		)
		c.JSON(http.StatusOK, gin.H{
			"id":         message.ID,
			"role":       message.Role,
			"content":    message.Content,
			"created_at": message.CreatedAt,
			"done":       true,
		})
		return
	}

	logger.Infof(
		ctx, "Preparing to set SSE headers and send stream data, session ID: %s, message ID: %s", sessionID, messageID,
	)

	// Send knowledge references first if available
	if len(streamInfo.KnowledgeReferences) > 0 {
		logger.Debug(ctx, "Sending knowledge references")
		c.SSEvent("message", &types.StreamResponse{
			ID:                  message.RequestID,
			ResponseType:        types.ResponseTypeReferences,
			Done:                false,
			KnowledgeReferences: streamInfo.KnowledgeReferences,
		})
	}

	// Send existing content
	if streamInfo.Content != "" {
		logger.Debug(ctx, "Sending current existing content")
		c.SSEvent("message", &types.StreamResponse{
			ID:           message.RequestID,
			ResponseType: types.ResponseTypeAnswer,
			Content:      streamInfo.Content,
			Done:         streamInfo.IsCompleted,
		})
	}

	// Create channels to monitor content updates
	contentCh := make(chan string, 10)
	doneCh := make(chan bool, 1)

	logger.Debug(ctx, "Starting content update monitoring")

	// Start a goroutine to monitor for content updates
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		currentContent := streamInfo.Content

		for {
			select {
			case <-ticker.C:
				latestStreamInfo, err := h.streamManager.GetStream(ctx, sessionID, messageID)
				if err != nil {
					logger.Errorf(ctx, "Failed to get stream data: %v", err)
					doneCh <- true
					return
				}

				if latestStreamInfo == nil {
					logger.Debug(ctx, "Stream no longer exists")
					doneCh <- true
					return
				}

				if latestStreamInfo.IsCompleted {
					logger.Debug(ctx, "Stream completed")
					doneCh <- true
					return
				}

				// Calculate new content delta
				if len(latestStreamInfo.Content) > len(currentContent) {
					newContent := latestStreamInfo.Content[len(currentContent):]
					contentCh <- newContent
					currentContent = latestStreamInfo.Content
					logger.Debugf(ctx, "Sending new content: %d bytes", len(newContent))
				}

			case <-c.Request.Context().Done():
				logger.Debug(ctx, "Client connection closed")
				return
			}
		}
	}()

	logger.Info(ctx, "Starting stream response")

	// Stream updated content to client
	c.Stream(func(w io.Writer) bool {
		select {
		case <-c.Request.Context().Done():
			logger.Debug(ctx, "Client connection closed")
			return false

		case <-doneCh:
			logger.Debug(ctx, "Stream completed, sending completion notification")
			c.SSEvent("message", &types.StreamResponse{
				ID:           message.RequestID,
				ResponseType: types.ResponseTypeAnswer,
				Content:      "",
				Done:         true,
			})
			return false

		case content := <-contentCh:
			logger.Debugf(ctx, "Sending content fragment: %d bytes", len(content))
			c.SSEvent("message", &types.StreamResponse{
				ID:           message.RequestID,
				ResponseType: types.ResponseTypeAnswer,
				Content:      content,
				Done:         false,
			})
			return true
		}
	})
}

// KnowledgeQA handles knowledge base question answering requests with LLM summarization
func (h *SessionHandler) KnowledgeQA(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	logger.Info(ctx, "Start processing knowledge QA request")

	// Get session ID from URL parameter
	sessionID := c.Param("session_id")
	if sessionID == "" {
		logger.Error(ctx, "Session ID is empty")
		c.Error(errors.NewBadRequestError(errors.ErrInvalidSessionID.Error()))
		return
	}

	// Parse request body
	var request CreateKnowledgeQARequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.Error(ctx, "Failed to parse request data", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// Create assistant message
	assistantMessage := &types.Message{
		SessionID:   sessionID,
		Role:        "assistant",
		RequestID:   c.GetString(types.RequestIDContextKey.String()),
		IsCompleted: false,
	}
	defer h.completeAssistantMessage(ctx, assistantMessage)

	// Validate query content
	if request.Query == "" {
		logger.Error(ctx, "Query content is empty")
		c.Error(errors.NewBadRequestError("Query content cannot be empty"))
		return
	}

	logger.Infof(ctx, "Knowledge QA request, session ID: %s, query: %s", sessionID, request.Query)

	// Create user message
	if _, err := h.messageService.CreateMessage(ctx, &types.Message{
		SessionID:   sessionID,
		Role:        "user",
		Content:     request.Query,
		RequestID:   c.GetString(types.RequestIDContextKey.String()),
		CreatedAt:   time.Now(),
		IsCompleted: true,
	}); err != nil {
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// Create assistant message (response)
	assistantMessage.CreatedAt = time.Now()
	if _, err := h.messageService.CreateMessage(ctx, assistantMessage); err != nil {
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	logger.Infof(ctx, "Calling knowledge QA service, session ID: %s", sessionID)

	// Call service to perform knowledge QA
	searchResults, respCh, err := h.sessionService.KnowledgeQA(ctx, sessionID, request.Query)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	assistantMessage.KnowledgeReferences = searchResults

	// Register new stream with stream manager
	requestID := c.GetString(types.RequestIDContextKey.String())
	if err := h.streamManager.RegisterStream(ctx, sessionID, assistantMessage.ID, request.Query); err != nil {
		logger.GetLogger(ctx).Error("Register stream failed", "error", err)
	}

	// Send knowledge references if available
	if len(searchResults) > 0 {
		logger.Debugf(ctx, "Sending reference content, total %d", len(searchResults))
		c.SSEvent("message", &types.StreamResponse{
			ID:                  requestID,
			ResponseType:        types.ResponseTypeReferences,
			KnowledgeReferences: searchResults,
		})
		c.Writer.Flush()
	} else {
		logger.Debug(ctx, "No reference content to send")
	}

	// Process streamed response
	func() {
		defer func() {
			// Mark stream as completed when done
			if err := h.streamManager.CompleteStream(ctx, sessionID, assistantMessage.ID); err != nil {
				logger.GetLogger(ctx).Error("Complete stream failed", "error", err)
			}
		}()
		for response := range respCh {
			response.ID = requestID
			c.SSEvent("message", response)
			c.Writer.Flush()
			if response.ResponseType == types.ResponseTypeAnswer {
				assistantMessage.Content += response.Content
				// Update stream manager with new content
				if err := h.streamManager.UpdateStream(
					ctx, sessionID, assistantMessage.ID, response.Content, searchResults,
				); err != nil {
					logger.GetLogger(ctx).Error("Update stream content failed", "error", err)
				}
			}
		}
	}()
}

// completeAssistantMessage marks an assistant message as complete and updates it
func (h *SessionHandler) completeAssistantMessage(ctx context.Context, assistantMessage *types.Message) {
	assistantMessage.UpdatedAt = time.Now()
	assistantMessage.IsCompleted = true
	_ = h.messageService.UpdateMessage(ctx, assistantMessage)
}
