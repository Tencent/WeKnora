package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"time"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// SearchKnowledge godoc
// @Summary      知识搜索
// @Description  在知识库中搜索（不使用LLM总结）
// @Tags         问答
// @Accept       json
// @Produce      json
// @Param        request  body      SearchKnowledgeRequest  true  "搜索请求"
// @Success      200      {object}  map[string]interface{}  "搜索结果"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Router       /sessions/search [post]
func (h *Handler) SearchKnowledge(c *gin.Context) {
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
		secutils.SanitizeForLog(request.KnowledgeBaseID),
		secutils.SanitizeForLog(request.Query),
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

// KnowledgeQA godoc
// @Summary      知识问答
// @Description  基于知识库的问答（使用LLM总结），支持SSE流式响应
// @Tags         问答
// @Accept       json
// @Produce      text/event-stream
// @Param        session_id  path      string                   true  "会话ID"
// @Param        request     body      CreateKnowledgeQARequest true  "问答请求"
// @Success      200         {object}  map[string]interface{}   "问答结果（SSE流）"
// @Failure      400         {object}  errors.AppError          "请求参数错误"
// @Security     Bearer
// @Router       /sessions/{session_id}/knowledge-qa [post]
func (h *Handler) KnowledgeQA(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	logger.Info(ctx, "Start processing knowledge QA request")

	// Get session ID from URL parameter
	sessionID := secutils.SanitizeForLog(c.Param("session_id"))
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

	// Validate query content
	if request.Query == "" {
		logger.Error(ctx, "Query content is empty")
		c.Error(errors.NewBadRequestError("Query content cannot be empty"))
		return
	}

	logger.Infof(
		ctx,
		"Knowledge QA request, session ID: %s, query: %s",
		sessionID,
		secutils.SanitizeForLog(request.Query),
	)

	// Get session to prepare knowledge base IDs
	session, err := h.sessionService.GetSession(ctx, sessionID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get session, session ID: %s, error: %v", sessionID, err)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// Prepare knowledge base IDs
	knowledgeBaseIDs := request.KnowledgeBaseIDs
	if len(knowledgeBaseIDs) == 0 && session.KnowledgeBaseID != "" {
		knowledgeBaseIDs = []string{session.KnowledgeBaseID}
		logger.Infof(
			ctx,
			"No knowledge base IDs in request, using session default: %s",
			secutils.SanitizeForLog(session.KnowledgeBaseID),
		)
	}

	// Use shared function to handle KnowledgeQA request
	h.handleKnowledgeQARequest(ctx, c, session, secutils.SanitizeForLog(request.Query),
		secutils.SanitizeForLogArray(knowledgeBaseIDs),
		assistantMessage, true, secutils.SanitizeForLog(request.SummaryModelID), request.WebSearchEnabled)
}

// AgentQA godoc
// @Summary      Agent问答
// @Description  基于Agent的智能问答，支持多轮对话和SSE流式响应
// @Tags         问答
// @Accept       json
// @Produce      text/event-stream
// @Param        session_id  path      string                   true  "会话ID"
// @Param        request     body      CreateKnowledgeQARequest true  "问答请求"
// @Success      200         {object}  map[string]interface{}   "问答结果（SSE流）"
// @Failure      400         {object}  errors.AppError          "请求参数错误"
// @Security     Bearer
// @Router       /sessions/{session_id}/agent-qa [post]
func (h *Handler) AgentQA(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())
	logger.Info(ctx, "Start processing agent QA request")

	// Get session ID from URL parameter
	sessionID := secutils.SanitizeForLog(c.Param("session_id"))
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
	if requestJSON, err := json.Marshal(request); err == nil {
		logger.Infof(ctx, "Agent QA request, request: %s", secutils.SanitizeForLog(string(requestJSON)))
	} else {
		logger.Warnf(ctx, "failed to marshal for logging: %s", secutils.SanitizeForLog(err.Error()))
	}

	// Validate query content
	if request.Query == "" {
		logger.Error(ctx, "Query content is empty")
		c.Error(errors.NewBadRequestError("Query content cannot be empty"))
		return
	}

	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)

	// Get session information first
	session, err := h.sessionService.GetSession(ctx, sessionID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get session, session ID: %s, error: %v", sessionID, err)
		c.Error(errors.NewNotFoundError("Session not found"))
		return
	}
	sessionJSON, err := json.Marshal(session)
	if err != nil {
		logger.Errorf(ctx, "Failed to marshal session, session ID: %s, error: %v", sessionID, err)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	logger.Infof(ctx, "Before AgentQA, Session: %s", secutils.SanitizeForLog(string(sessionJSON)))

	// Create assistant message
	assistantMessage := &types.Message{
		SessionID:   sessionID,
		Role:        "assistant",
		RequestID:   c.GetString(types.RequestIDContextKey.String()),
		IsCompleted: false,
	}

	// Initialize AgentConfig if it doesn't exist
	if session.AgentConfig == nil {
		session.AgentConfig = &types.SessionAgentConfig{}
	}

	// Detect if knowledge bases or agent mode has changed
	knowledgeBasesChanged := false
	configChanged := false

	// Check if knowledge bases array has changed
	if len(request.KnowledgeBaseIDs) > 0 {
		// Compare arrays to detect changes
		currentKBs := session.AgentConfig.KnowledgeBases
		if len(currentKBs) != len(request.KnowledgeBaseIDs) {
			knowledgeBasesChanged = true
			configChanged = true
		} else {
			// Check if contents are different
			kbMap := make(map[string]bool)
			for _, kb := range currentKBs {
				kbMap[kb] = true
			}
			for _, kb := range request.KnowledgeBaseIDs {
				if !kbMap[kb] {
					knowledgeBasesChanged = true
					configChanged = true
					break
				}
			}
		}
		if knowledgeBasesChanged {
			logger.Infof(ctx, "Knowledge bases changed from %s to %s",
				secutils.SanitizeForLog(fmt.Sprintf("%v", session.AgentConfig.KnowledgeBases)),
				secutils.SanitizeForLog(fmt.Sprintf("%v", request.KnowledgeBaseIDs)),
			)
		}
	}

	// Check if agent mode has changed
	currentAgentEnabled := session.AgentConfig.AgentModeEnabled
	if request.AgentEnabled != currentAgentEnabled {
		logger.Infof(ctx, "Agent mode changed from %v to %v", currentAgentEnabled, request.AgentEnabled)
		configChanged = true
	}
	currentWebSearchEnabled := session.AgentConfig.WebSearchEnabled
	if request.WebSearchEnabled != currentWebSearchEnabled {
		logger.Infof(ctx, "Web search mode changed from %v to %v", currentWebSearchEnabled, request.WebSearchEnabled)
		configChanged = true
	}
	summaryModelID := secutils.SanitizeForLog(request.SummaryModelID)
	if summaryModelID == "" {
		summaryModelID = session.SummaryModelID
	}
	if summaryModelID == "" && tenantInfo.ConversationConfig != nil {
		summaryModelID = tenantInfo.ConversationConfig.SummaryModelID
	}
	if summaryModelID != session.SummaryModelID {
		configChanged = true
		session.SummaryModelID = summaryModelID
	}

	// If configuration changed, clear context and update session
	if configChanged {
		logger.Warnf(ctx, "Configuration changed, clearing context for session: %s", sessionID)
		// Clear the LLM context to prevent contamination
		if err := h.sessionService.ClearContext(ctx, sessionID); err != nil {
			logger.Errorf(ctx, "Failed to clear context for session %s: %v", sessionID, err)
			// Continue anyway - this is not a fatal error
		}
		if err := h.sessionService.DeleteWebSearchTempKBState(ctx, sessionID); err != nil {
			logger.Errorf(ctx, "Failed to delete temp knowledge base for session %s: %v", sessionID, err)
			// Continue anyway - this is not a fatal error
		}
		session.AgentConfig.KnowledgeBases = secutils.SanitizeForLogArray(request.KnowledgeBaseIDs)
		session.AgentConfig.AgentModeEnabled = request.AgentEnabled
		session.AgentConfig.WebSearchEnabled = request.WebSearchEnabled
		session.SummaryModelID = secutils.SanitizeForLog(summaryModelID)
		// Persist the session changes
		if err := h.sessionService.UpdateSession(ctx, session); err != nil {
			logger.Errorf(ctx, "Failed to update session %s: %v", sessionID, err)
			c.Error(errors.NewInternalServerError("Failed to update session configuration"))
			return
		}
		logger.Infof(ctx, "Session configuration updated successfully for session: %s", sessionID)
	}

	// If Agent mode is disabled, delegate to KnowledgeQA
	if !request.AgentEnabled {
		logger.Infof(ctx, "Agent mode disabled, delegating to KnowledgeQA for session: %s", sessionID)

		// Use knowledge bases from request or session config
		knowledgeBaseIDs := secutils.SanitizeForLogArray(request.KnowledgeBaseIDs)
		if len(knowledgeBaseIDs) == 0 {
			knowledgeBaseIDs = session.AgentConfig.KnowledgeBases
		}

		// If still empty, use session default knowledge base
		if len(knowledgeBaseIDs) == 0 && session.KnowledgeBaseID != "" {
			knowledgeBaseIDs = []string{session.KnowledgeBaseID}
			logger.Infof(
				ctx,
				"Using session default knowledge base: %s",
				secutils.SanitizeForLog(session.KnowledgeBaseID),
			)
		}

		// Validate at least one knowledge base is available
		if len(knowledgeBaseIDs) == 0 {
			logger.Error(ctx, "No knowledge base available for delegation")
			c.Error(
				errors.NewBadRequestError("No knowledge base available. Please configure at least one knowledge base."),
			)
			return
		}

		logger.Infof(
			ctx,
			"Delegating to KnowledgeQA with knowledge bases: %s",
			secutils.SanitizeForLog(fmt.Sprintf("%v", knowledgeBaseIDs)),
		)

		// Use shared function to handle KnowledgeQA request (no title generation for AgentQA fallback)
		h.handleKnowledgeQARequest(
			ctx,
			c,
			session,
			secutils.SanitizeForLog(request.Query),
			secutils.SanitizeForLogArray(
				knowledgeBaseIDs,
			),
			assistantMessage,
			false,
			secutils.SanitizeForLog(request.SummaryModelID),
			request.WebSearchEnabled,
		)
		return
	}

	// Emit agent query event to create user message
	requestID := secutils.SanitizeForLog(c.GetString(types.RequestIDContextKey.String()))
	if err := event.Emit(ctx, event.Event{
		Type:      event.EventAgentQuery,
		SessionID: sessionID,
		RequestID: requestID,
		Data: event.AgentQueryData{
			SessionID: sessionID,
			Query:     secutils.SanitizeForLog(request.Query),
			RequestID: requestID,
		},
	}); err != nil {
		logger.Errorf(ctx, "Failed to emit agent query event: %v", err)
		return
	}

	// Set headers for SSE immediately
	setSSEHeaders(c)

	// Create user message
	if err := h.createUserMessage(ctx, sessionID, secutils.SanitizeForLog(request.Query), requestID); err != nil {
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// Create assistant message (response)
	assistantMessagePtr, err := h.createAssistantMessage(ctx, assistantMessage)
	if err != nil {
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	assistantMessage = assistantMessagePtr

	logger.Infof(ctx, "Calling agent QA service, session ID: %s", sessionID)

	// Write initial agent_query event to StreamManager
	h.writeAgentQueryEvent(ctx, sessionID, assistantMessage.ID)

	eventBus := event.NewEventBus()
	// Create cancellable context for async operations
	asyncCtx, cancel := context.WithCancel(logger.CloneContext(ctx))
	// Create and subscribe stream handler
	h.setupStreamHandler(asyncCtx, sessionID, assistantMessage.ID, requestID, assistantMessage, eventBus)

	// Start async title generation if session has no title
	if session.Title == "" {
		logger.Infof(ctx, "Session has no title, starting async title generation, session ID: %s", sessionID)
		h.sessionService.GenerateTitleAsync(asyncCtx, session, secutils.SanitizeForLog(request.Query), eventBus)
	}

	// Register stop event handler to cancel the context
	h.setupStopEventHandler(eventBus, sessionID, assistantMessage, cancel)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				buf := make([]byte, 1024)
				runtime.Stack(buf, true)
				logger.ErrorWithFields(asyncCtx,
					errors.NewInternalServerError(fmt.Sprintf("Agent QA service panicked: %v\n%s", r, string(buf))),
					map[string]interface{}{
						"session_id": sessionID,
					})
			}
			h.completeAssistantMessage(asyncCtx, assistantMessage)
			logger.Infof(asyncCtx, "Agent QA service completed for session: %s", sessionID)
		}()
		err := h.sessionService.AgentQA(
			asyncCtx,
			session,
			secutils.SanitizeForLog(request.Query),
			assistantMessage.ID,
			eventBus,
		)
		if err != nil {
			logger.ErrorWithFields(asyncCtx, err, nil)
			// Emit error event to dedicated EventBus
			eventBus.Emit(asyncCtx, event.Event{
				Type:      event.EventError,
				SessionID: sessionID,
				Data: event.ErrorData{
					Error:     err.Error(),
					Stage:     "agent_execution",
					SessionID: sessionID,
				},
			})
			return
		}
	}()

	// Handle events for SSE (blocking until connection is done)
	h.handleAgentEventsForSSE(ctx, c, sessionID, assistantMessage.ID, requestID, eventBus)
}

// handleKnowledgeQARequest handles a KnowledgeQA request with the given parameters
// This is a shared function used by both KnowledgeQA endpoint and AgentQA fallback
func (h *Handler) handleKnowledgeQARequest(
	ctx context.Context,
	c *gin.Context,
	session *types.Session,
	query string,
	knowledgeBaseIDs []string,
	assistantMessage *types.Message,
	generateTitle bool, // Whether to generate title if session has no title
	summaryModelID string, // Optional summary model ID (overrides session default)
	webSearchEnabled bool, // Whether web search is enabled
) {
	sessionID := session.ID
	requestID := getRequestID(c)

	// Create user message
	if err := h.createUserMessage(ctx, sessionID, query, requestID); err != nil {
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// Create assistant message (response)
	if _, err := h.createAssistantMessage(ctx, assistantMessage); err != nil {
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// Validate knowledge bases
	if len(knowledgeBaseIDs) == 0 {
		logger.Error(ctx, "No knowledge base ID available")
		c.Error(errors.NewBadRequestError("At least one knowledge base ID is required"))
		return
	}

	logger.Infof(ctx, "Using knowledge bases: %s", secutils.SanitizeForLog(fmt.Sprintf("%v", knowledgeBaseIDs)))

	// Set headers for SSE
	setSSEHeaders(c)

	// Write initial agent_query event to StreamManager
	h.writeAgentQueryEvent(ctx, sessionID, assistantMessage.ID)

	// Create dedicated EventBus for this request
	eventBus := event.NewEventBus()
	// Create cancellable context for async operations
	asyncCtx, cancel := context.WithCancel(logger.CloneContext(ctx))

	// Register stop event handler and setup stream handler
	h.setupStopEventHandler(eventBus, sessionID, assistantMessage, cancel)
	h.setupStreamHandler(asyncCtx, sessionID, assistantMessage.ID, requestID, assistantMessage, eventBus)

	// Generate title if needed
	if generateTitle && session.Title == "" {
		logger.Infof(ctx, "Session has no title, starting async title generation, session ID: %s", sessionID)
		h.sessionService.GenerateTitleAsync(asyncCtx, session, query, eventBus)
	}

	eventBus.On(event.EventAgentFinalAnswer, func(ctx context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentFinalAnswerData)
		if !ok {
			return nil
		}
		assistantMessage.Content += data.Content
		if data.Done {
			logger.Infof(asyncCtx, "Knowledge QA service completed for session: %s", sessionID)
			h.completeAssistantMessage(asyncCtx, assistantMessage)
			// Emit completion event when stream finishes
			if err := eventBus.Emit(asyncCtx, event.Event{
				Type:      event.EventAgentComplete,
				SessionID: sessionID,
				Data: event.AgentCompleteData{
					FinalAnswer: assistantMessage.Content,
				},
			}); err != nil {
				logger.Errorf(asyncCtx, "Failed to emit completion event: %v", err)
			}
			cancel() // Clean up context
			return nil
		}
		return nil
	})

	// Call service to perform knowledge QA (async, emits events)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				buf := make([]byte, 10240)
				runtime.Stack(buf, true)
				logger.ErrorWithFields(
					asyncCtx,
					errors.NewInternalServerError(fmt.Sprintf("Knowledge QA service panicked: %v\n%s", r, string(buf))),
					nil,
				)
			}
		}()
		err := h.sessionService.KnowledgeQA(
			asyncCtx,
			session,
			query,
			knowledgeBaseIDs,
			assistantMessage.ID,
			summaryModelID,
			webSearchEnabled,
			eventBus,
		)
		if err != nil {
			logger.ErrorWithFields(asyncCtx, err, nil)
			// Emit error event to dedicated EventBus
			eventBus.Emit(asyncCtx, event.Event{
				Type:      event.EventError,
				SessionID: sessionID,
				Data: event.ErrorData{
					Error:     err.Error(),
					Stage:     "knowledge_qa_execution",
					SessionID: sessionID,
				},
			})
			return
		}
	}()

	// Handle events for SSE (blocking until connection is done)
	h.handleAgentEventsForSSE(ctx, c, sessionID, assistantMessage.ID, requestID, eventBus)
}

// completeAssistantMessage marks an assistant message as complete and updates it
func (h *Handler) completeAssistantMessage(ctx context.Context, assistantMessage *types.Message) {
	assistantMessage.UpdatedAt = time.Now()
	assistantMessage.IsCompleted = true
	_ = h.messageService.UpdateMessage(ctx, assistantMessage)
}
