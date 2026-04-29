package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// qaRequestContext holds all the common data needed for QA requests
type qaRequestContext struct {
	ctx               context.Context
	c                 *gin.Context
	sessionID         string
	requestID         string
	query             string
	session           *types.Session
	customAgent       *types.CustomAgent
	assistantMessage  *types.Message
	knowledgeBaseIDs  []string
	knowledgeIDs      []string
	summaryModelID    string
	webSearchEnabled  bool
	enableMemory      bool // Whether memory feature is enabled
	mentionedItems    types.MentionedItems
	effectiveTenantID uint64            // when using shared agent, tenant ID for model/KB/MCP resolution; 0 = use context tenant
	images            []ImageAttachment // Uploaded images with analysis text
	userMessageID     string            // Created user message ID (populated after createUserMessage)
	channel           string            // Source channel: "web", "api", "im", etc.
	attachments       types.MessageAttachments // Processed file attachments
}

// buildQARequest converts the qaRequestContext into a types.QARequest for service invocation.
func (rc *qaRequestContext) buildQARequest() *types.QARequest {
	imageURLs, imageDescription := extractImageURLsAndOCRText(rc.images)
	return &types.QARequest{
		Session:            rc.session,
		Query:              rc.query,
		AssistantMessageID: rc.assistantMessage.ID,
		SummaryModelID:     rc.summaryModelID,
		CustomAgent:        rc.customAgent,
		KnowledgeBaseIDs:   rc.knowledgeBaseIDs,
		KnowledgeIDs:       rc.knowledgeIDs,
		ImageURLs:          imageURLs,
		ImageDescription:   imageDescription,
		UserMessageID:      rc.userMessageID,
		WebSearchEnabled:   rc.webSearchEnabled,
		EnableMemory:       rc.enableMemory,
		Attachments:        rc.attachments,
	}
}

// parseQARequest parses and validates a QA request, returns the request context
func (h *Handler) parseQARequest(c *gin.Context, logPrefix string) (*qaRequestContext, *CreateKnowledgeQARequest, error) {
	ctx := logger.CloneContext(c.Request.Context())
	logger.Infof(ctx, "[%s] Start processing request", logPrefix)

	// Get session ID from URL parameter
	sessionID := secutils.SanitizeForLog(c.Param("session_id"))
	if sessionID == "" {
		logger.Error(ctx, "Session ID is empty")
		return nil, nil, errors.NewBadRequestError(errors.ErrInvalidSessionID.Error())
	}

	// Parse request body
	var request CreateKnowledgeQARequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.Error(ctx, "Failed to parse request data", err)
		return nil, nil, errors.NewBadRequestError(err.Error())
	}

	// Validate query content
	if request.Query == "" {
		logger.Error(ctx, "Query content is empty")
		return nil, nil, errors.NewBadRequestError("Query content cannot be empty")
	}

	// SSRF protection: strip client-supplied URL/Caption fields from image attachments.
	// The URL field must only be populated server-side by saveImageAttachments; an
	// attacker could inject internal network URLs to trigger SSRF via the LLM provider.
	for i := range request.Images {
		request.Images[i].URL = ""
		request.Images[i].Caption = ""
	}

	// Log request details
	if requestJSON, err := json.Marshal(request); err == nil {
		logger.Infof(ctx, "[%s] Request: session_id=%s, request=%s",
			logPrefix, sessionID, secutils.SanitizeForLog(secutils.CompactImageDataURLForLog(string(requestJSON))))
	}

	// Get session
	session, err := h.sessionService.GetSession(ctx, sessionID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get session, session ID: %s, error: %v", sessionID, err)
		return nil, nil, errors.NewNotFoundError("Session not found")
	}

	// Get custom agent if agent_id is provided. Backend resolves shared agent from share relation (no client-provided tenant).
	customAgent, effectiveTenantID := h.resolveAgent(ctx, c, request.AgentID)

	// Merge @mentioned items into knowledge_base_ids and knowledge_ids
	kbIDs, knowledgeIDs := mergeKnowledgeTargets(request.KnowledgeBaseIDs, request.KnowledgeIds, request.MentionedItems)

	// Log merge results for debugging
	logger.Infof(ctx, "[%s] @mention merge: request.KnowledgeBaseIDs=%v, request.MentionedItems=%d, merged kbIDs=%v, merged knowledgeIDs=%v",
		logPrefix, request.KnowledgeBaseIDs, len(request.MentionedItems), kbIDs, knowledgeIDs)

	// Process inline base64 images: decode and save to storage.
	// VLM analysis for RAG paths is deferred to the pipeline rewrite step.
	// For pure chat paths with non-vision models, VLM analysis runs here as fallback.
	if len(request.Images) > 0 {
		if customAgent == nil || !customAgent.Config.ImageUploadEnabled {
			logger.Warnf(ctx, "[%s] Image upload is not enabled for this agent, rejecting %d images", logPrefix, len(request.Images))
			return nil, nil, errors.NewBadRequestError("Image upload is not enabled for this agent")
		}
		tenantID := c.GetUint64(types.TenantIDContextKey.String())
		agentStorageProvider := customAgent.Config.ImageStorageProvider
		if err := h.saveImageAttachments(ctx, request.Images, tenantID, agentStorageProvider); err != nil {
			logger.Errorf(ctx, "[%s] Failed to save images: %v", logPrefix, err)
			return nil, nil, errors.NewBadRequestError(fmt.Sprintf("Image save failed: %v", err))
		}

		// VLM analysis is always deferred to after SSE stream is up:
		// - Agent mode: runs in async execution flow with tool_call/tool_result events
		// - Normal RAG mode: runs in the pipeline rewrite step with progress events
		// - Normal pure-chat mode: runs in the async goroutine with progress events
	}

	// Process file attachments: decode and save to storage, extract content
	var processedAttachments types.MessageAttachments
	if len(request.AttachmentUploads) > 0 {
		logger.Infof(ctx, "[%s] processing %d attachment(s)", logPrefix, len(request.AttachmentUploads))

		maxSize := secutils.GetMaxFileSize()
		for i, upload := range request.AttachmentUploads {
			if upload.FileSize > maxSize {
				return nil, nil, errors.NewBadRequestError(
					fmt.Sprintf("attachment %d exceeds size limit of %dMB", i+1, secutils.GetMaxFileSizeMB()))
			}
		}

		tenantID := c.GetUint64(types.TenantIDContextKey.String())

		// Use ASR only when the agent has audio upload enabled.
		asrModelID := ""
		if customAgent != nil && customAgent.Config.AudioUploadEnabled && customAgent.Config.ASRModelID != "" {
			asrModelID = customAgent.Config.ASRModelID
		}

		// Process all attachments concurrently.
		processedAttachments = make(types.MessageAttachments, len(request.AttachmentUploads))
		var wg sync.WaitGroup
		errChan := make(chan error, len(request.AttachmentUploads))

		for i, upload := range request.AttachmentUploads {
			wg.Add(1)
			go func(idx int, att AttachmentUpload) {
				defer wg.Done()

				data, err := DecodeBase64Attachment(att.Data)
				if err != nil {
					errChan <- fmt.Errorf("attachment %d decode failed: %w", idx+1, err)
					return
				}

				processed, err := h.attachmentProcessor.ProcessAttachment(
					ctx, data, att.FileName, att.FileSize, tenantID, asrModelID,
				)
				if err != nil {
					errChan <- fmt.Errorf("attachment %d processing failed: %w", idx+1, err)
					return
				}

				processedAttachments[idx] = *processed
			}(i, upload)
		}

		wg.Wait()
		close(errChan)

		if len(errChan) > 0 {
			err := <-errChan
			logger.Errorf(ctx, "[%s] attachment processing failed: %v", logPrefix, err)
			return nil, nil, errors.NewBadRequestError(fmt.Sprintf("attachment processing failed: %v", err))
		}

		logger.Infof(ctx, "[%s] all attachments processed", logPrefix)
	}

	// Build request context
	reqCtx := &qaRequestContext{
		ctx:         ctx,
		c:           c,
		sessionID:   sessionID,
		requestID:   secutils.SanitizeForLog(c.GetString(types.RequestIDContextKey.String())),
		query:       secutils.SanitizeForLog(request.Query),
		session:     session,
		customAgent: customAgent,
		assistantMessage: &types.Message{
			SessionID:   sessionID,
			Role:        "assistant",
			RequestID:   c.GetString(types.RequestIDContextKey.String()),
			IsCompleted: false,
			Channel:     request.Channel,
		},
		knowledgeBaseIDs:  secutils.SanitizeForLogArray(kbIDs),
		knowledgeIDs:      secutils.SanitizeForLogArray(knowledgeIDs),
		summaryModelID:    secutils.SanitizeForLog(request.SummaryModelID),
		webSearchEnabled:  request.WebSearchEnabled,
		enableMemory:      request.EnableMemory,
		mentionedItems:    convertMentionedItems(request.MentionedItems),
		effectiveTenantID: effectiveTenantID,
		images:            request.Images,
		channel:           request.Channel,
		attachments:       processedAttachments,
	}

	return reqCtx, &request, nil
}

// resolveAgent resolves the custom agent by ID, trying shared agent first, then own agent.
// Returns (nil, 0) if agentID is empty or not found.
func (h *Handler) resolveAgent(ctx context.Context, c *gin.Context, agentID string) (*types.CustomAgent, uint64) {
	if agentID == "" {
		return nil, 0
	}

	logger.Infof(ctx, "Resolving agent, agent ID: %s", secutils.SanitizeForLog(agentID))

	// Try shared agent first
	var customAgent *types.CustomAgent
	var effectiveTenantID uint64
	userIDVal, _ := c.Get(types.UserIDContextKey.String())
	currentTenantID := c.GetUint64(types.TenantIDContextKey.String())
	if h.agentShareService != nil && userIDVal != nil && currentTenantID != 0 {
		userID, _ := userIDVal.(string)
		agent, err := h.agentShareService.GetSharedAgentForUser(ctx, userID, currentTenantID, agentID)
		if err == nil && agent != nil {
			effectiveTenantID = agent.TenantID
			customAgent = agent
			logger.Infof(ctx, "Using shared agent: ID=%s, Name=%s, effectiveTenantID=%d (retrieval scope)",
				customAgent.ID, customAgent.Name, effectiveTenantID)
		}
	}

	// Fall back to own agent
	if customAgent == nil {
		agent, err := h.customAgentService.GetAgentByID(ctx, agentID)
		if err == nil {
			customAgent = agent
			logger.Infof(ctx, "Using own agent: ID=%s, Name=%s, AgentMode=%s",
				customAgent.ID, customAgent.Name, customAgent.Config.AgentMode)
		} else {
			logger.Warnf(ctx, "Failed to get custom agent, agent ID: %s, error: %v, using default config",
				secutils.SanitizeForLog(agentID), err)
		}
	} else {
		logger.Infof(ctx, "Using custom agent: ID=%s, Name=%s, IsBuiltin=%v, AgentMode=%s, effectiveTenantID=%d",
			customAgent.ID, customAgent.Name, customAgent.IsBuiltin, customAgent.Config.AgentMode, effectiveTenantID)
	}

	return customAgent, effectiveTenantID
}

// mergeKnowledgeTargets merges request KB/knowledge IDs with @mentioned items into deduplicated slices.
func mergeKnowledgeTargets(requestKBIDs []string, requestKnowledgeIDs []string, mentionedItems []MentionedItemRequest) (kbIDs []string, knowledgeIDs []string) {
	kbIDSet := make(map[string]bool)
	kbIDs = make([]string, 0, len(requestKBIDs)+len(mentionedItems))
	for _, id := range requestKBIDs {
		if id != "" && !kbIDSet[id] {
			kbIDs = append(kbIDs, id)
			kbIDSet[id] = true
		}
	}

	knowledgeIDSet := make(map[string]bool)
	knowledgeIDs = make([]string, 0, len(requestKnowledgeIDs)+len(mentionedItems))
	for _, id := range requestKnowledgeIDs {
		if id != "" && !knowledgeIDSet[id] {
			knowledgeIDs = append(knowledgeIDs, id)
			knowledgeIDSet[id] = true
		}
	}

	for _, item := range mentionedItems {
		if item.ID == "" {
			continue
		}
		switch item.Type {
		case "kb":
			if !kbIDSet[item.ID] {
				kbIDs = append(kbIDs, item.ID)
				kbIDSet[item.ID] = true
			}
		case "file":
			if !knowledgeIDSet[item.ID] {
				knowledgeIDs = append(knowledgeIDs, item.ID)
				knowledgeIDSet[item.ID] = true
			}
		}
	}
	return kbIDs, knowledgeIDs
}

// sseStreamContext holds the context for SSE streaming
type sseStreamContext struct {
	eventBus         *event.EventBus
	asyncCtx         context.Context
	cancel           context.CancelFunc
	assistantMessage *types.Message
}

// setupSSEStream sets up the SSE streaming context
func (h *Handler) setupSSEStream(reqCtx *qaRequestContext, generateTitle bool) *sseStreamContext {
	// Set SSE headers
	setSSEHeaders(reqCtx.c)

	// Write initial agent_query event
	h.writeAgentQueryEvent(reqCtx.ctx, reqCtx.sessionID, reqCtx.assistantMessage.ID)

	// Base context for async work: when using shared agent, use source tenant for model/KB/MCP resolution
	baseCtx := reqCtx.ctx
	if reqCtx.effectiveTenantID != 0 && h.tenantService != nil {
		if tenant, err := h.tenantService.GetTenantByID(reqCtx.ctx, reqCtx.effectiveTenantID); err == nil && tenant != nil {
			baseCtx = context.WithValue(context.WithValue(reqCtx.ctx, types.TenantIDContextKey, reqCtx.effectiveTenantID), types.TenantInfoContextKey, tenant)
			logger.Infof(reqCtx.ctx, "Using effective tenant %d for shared agent (model/KB/MCP)", reqCtx.effectiveTenantID)
		}
	}

	// Create EventBus and cancellable context
	eventBus := event.NewEventBus()
	asyncCtx, cancel := context.WithCancel(logger.CloneContext(baseCtx))

	streamCtx := &sseStreamContext{
		eventBus:         eventBus,
		asyncCtx:         asyncCtx,
		cancel:           cancel,
		assistantMessage: reqCtx.assistantMessage,
	}

	// Setup stop event handler
	h.setupStopEventHandler(eventBus, reqCtx.sessionID, reqCtx.session.TenantID, reqCtx.assistantMessage, cancel)

	// Setup stream handler
	h.setupStreamHandler(asyncCtx, reqCtx.sessionID, reqCtx.assistantMessage.ID,
		reqCtx.requestID, reqCtx.assistantMessage, eventBus)

	// Generate title if needed
	if generateTitle && reqCtx.session.Title == "" {
		// Use the same model as the conversation for title generation
		modelID := ""
		if reqCtx.customAgent != nil && reqCtx.customAgent.Config.ModelID != "" {
			modelID = reqCtx.customAgent.Config.ModelID
		}
		logger.Infof(reqCtx.ctx, "Session has no title, starting async title generation, session ID: %s, model: %s", reqCtx.sessionID, modelID)
		h.sessionService.GenerateTitleAsync(asyncCtx, reqCtx.session, reqCtx.query, modelID, eventBus)
	}

	return streamCtx
}

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
// @Security     ApiKeyAuth
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

	// Merge single knowledge_base_id into knowledge_base_ids for backward compatibility
	knowledgeBaseIDs := request.KnowledgeBaseIDs
	if request.KnowledgeBaseID != "" {
		// Check if it's already in the list to avoid duplicates
		found := false
		for _, id := range knowledgeBaseIDs {
			if id == request.KnowledgeBaseID {
				found = true
				break
			}
		}
		if !found {
			knowledgeBaseIDs = append(knowledgeBaseIDs, request.KnowledgeBaseID)
		}
	}

	if len(knowledgeBaseIDs) == 0 && len(request.KnowledgeIDs) == 0 {
		logger.Error(ctx, "No knowledge base IDs or knowledge IDs provided")
		c.Error(errors.NewBadRequestError("At least one knowledge_base_id, knowledge_base_ids or knowledge_ids must be provided"))
		return
	}

	logger.Infof(
		ctx,
		"Knowledge search request, knowledge base IDs: %v, knowledge IDs: %v, query: %s",
		secutils.SanitizeForLogArray(knowledgeBaseIDs),
		secutils.SanitizeForLogArray(request.KnowledgeIDs),
		secutils.SanitizeForLog(request.Query),
	)

	// Directly call knowledge retrieval service without LLM summarization
	searchResults, err := h.sessionService.SearchKnowledge(ctx, knowledgeBaseIDs, request.KnowledgeIDs, request.Query)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge search completed, found %d results", len(searchResults))
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
// @Security     ApiKeyAuth
// @Router       /sessions/{session_id}/knowledge-qa [post]
func (h *Handler) KnowledgeQA(c *gin.Context) {
	// Parse and validate request
	reqCtx, request, err := h.parseQARequest(c, "KnowledgeQA")
	if err != nil {
		c.Error(err)
		return
	}

	// Execute normal mode QA, generate title unless disabled
	h.executeQA(reqCtx, qaModeNormal, !request.DisableTitle)
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
// @Security     ApiKeyAuth
// @Router       /sessions/{session_id}/agent-qa [post]
func (h *Handler) AgentQA(c *gin.Context) {
	// Parse and validate request
	reqCtx, request, err := h.parseQARequest(c, "AgentQA")
	if err != nil {
		c.Error(err)
		return
	}

	// Determine if agent mode should be enabled
	// Priority: customAgent.IsAgentMode() > request.AgentEnabled
	agentModeEnabled := request.AgentEnabled
	if reqCtx.customAgent != nil {
		agentModeEnabled = reqCtx.customAgent.IsAgentMode()
		logger.Infof(reqCtx.ctx, "Agent mode determined by custom agent: %v (config.agent_mode=%s)",
			agentModeEnabled, reqCtx.customAgent.Config.AgentMode)
	}

	// Route to appropriate handler based on agent mode
	if agentModeEnabled {
		h.executeQA(reqCtx, qaModeAgent, true)
	} else {
		logger.Infof(reqCtx.ctx, "Agent mode disabled, delegating to normal mode for session: %s", reqCtx.sessionID)
		h.executeQA(reqCtx, qaModeNormal, false)
	}
}

// qaMode determines which QA execution path to use.
type qaMode int

const (
	qaModeNormal qaMode = iota // KnowledgeQA pipeline (RAG / pure chat)
	qaModeAgent                // Agent engine with tool calling
)

// executeQA is the unified execution flow for both KnowledgeQA and AgentQA modes.
// It handles message creation, SSE setup, VLM analysis, service invocation, and error handling.
func (h *Handler) executeQA(reqCtx *qaRequestContext, mode qaMode, generateTitle bool) {
	ctx := reqCtx.ctx
	sessionID := reqCtx.sessionID

	// Agent mode: emit agent query event before message creation
	if mode == qaModeAgent {
		if err := event.Emit(ctx, event.Event{
			Type:      event.EventAgentQuery,
			SessionID: sessionID,
			RequestID: reqCtx.requestID,
			Data: event.AgentQueryData{
				SessionID: sessionID,
				Query:     reqCtx.query,
				RequestID: reqCtx.requestID,
			},
		}); err != nil {
			logger.Errorf(ctx, "Failed to emit agent query event: %v", err)
			return
		}
	}

	// Create user message
	userMsg, err := h.createUserMessage(ctx, sessionID, reqCtx.query, reqCtx.requestID, reqCtx.mentionedItems, convertImageAttachments(reqCtx.images), reqCtx.attachments, reqCtx.channel)
	if err != nil {
		reqCtx.c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	reqCtx.userMessageID = userMsg.ID

	// Create assistant message
	assistantMessagePtr, err := h.createAssistantMessage(ctx, reqCtx.assistantMessage)
	if err != nil {
		reqCtx.c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	reqCtx.assistantMessage = assistantMessagePtr

	if mode == qaModeNormal {
		logger.Infof(ctx, "Using knowledge bases: %v", reqCtx.knowledgeBaseIDs)
	} else {
		logger.Infof(ctx, "Calling agent QA service, session ID: %s", sessionID)
	}

	// Setup SSE stream
	streamCtx := h.setupSSEStream(reqCtx, generateTitle)

	// Normal mode: register completion handler on EventAgentFinalAnswer
	// (Agent mode handles completion in the defer block instead)
	if mode == qaModeNormal {
		var completionHandled bool
		streamCtx.eventBus.On(event.EventAgentFinalAnswer, func(ctx context.Context, evt event.Event) error {
			data, ok := evt.Data.(event.AgentFinalAnswerData)
			if !ok {
				return nil
			}
			streamCtx.assistantMessage.Content += data.Content
			if data.IsFallback {
				streamCtx.assistantMessage.IsFallback = true
			}
			if data.Done {
				if completionHandled {
					return nil
				}
				completionHandled = true

				logger.Infof(streamCtx.asyncCtx, "Knowledge QA service completed for session: %s", sessionID)
				updateCtx := context.WithValue(streamCtx.asyncCtx, types.TenantIDContextKey, reqCtx.session.TenantID)
				h.completeAssistantMessage(updateCtx, streamCtx.assistantMessage, reqCtx.query)
				streamCtx.eventBus.Emit(streamCtx.asyncCtx, event.Event{
					Type:      event.EventAgentComplete,
					SessionID: sessionID,
					Data:      event.AgentCompleteData{FinalAnswer: streamCtx.assistantMessage.Content},
				})
			}
			return nil
		})
	}

	// Execute QA asynchronously
	go func() {
		defer func() {
			if r := recover(); r != nil {
				buf := make([]byte, 10240)
				runtime.Stack(buf, true)
				stageName := "Knowledge QA"
				if mode == qaModeAgent {
					stageName = "Agent QA"
				}
				logger.ErrorWithFields(streamCtx.asyncCtx,
					errors.NewInternalServerError(fmt.Sprintf("%s service panicked: %v\n%s", stageName, r, string(buf))),
					map[string]interface{}{"session_id": sessionID})
			}
			// Agent mode: complete the assistant message in defer (normal mode does it via event handler)
			if mode == qaModeAgent {
				// Use WithoutCancel so a user-triggered stop (which cancels
				// asyncCtx) doesn't also cancel the GORM UPDATE that persists
				// AgentSteps/Content. Without this, cancelled-ctx makes
				// GORM skip the write and the agent's intermediate steps
				// (thinking / tool_call history) are lost on page refresh.
				updateCtx := context.WithValue(
					context.WithoutCancel(streamCtx.asyncCtx),
					types.TenantIDContextKey, reqCtx.session.TenantID,
				)
				h.completeAssistantMessage(updateCtx, streamCtx.assistantMessage, reqCtx.query)
				logger.Infof(streamCtx.asyncCtx, "Agent QA service completed for session: %s", sessionID)
			}
		}()

		// Run VLM image analysis if applicable
		h.runVLMAnalysisIfNeeded(streamCtx, reqCtx, mode)

		// Build QA request and invoke the appropriate service
		qaReq := reqCtx.buildQARequest()

		var serviceErr error
		var stageName string
		if mode == qaModeNormal {
			stageName = "knowledge_qa_execution"
			serviceErr = h.sessionService.KnowledgeQA(streamCtx.asyncCtx, qaReq, streamCtx.eventBus)
		} else {
			stageName = "agent_execution"
			serviceErr = h.sessionService.AgentQA(streamCtx.asyncCtx, qaReq, streamCtx.eventBus)
		}

		if serviceErr != nil {
			logger.ErrorWithFields(streamCtx.asyncCtx, serviceErr, nil)
			streamCtx.eventBus.Emit(streamCtx.asyncCtx, event.Event{
				Type:      event.EventError,
				SessionID: sessionID,
				Data: event.ErrorData{
					Error:     serviceErr.Error(),
					Stage:     stageName,
					SessionID: sessionID,
				},
			})
		}
	}()

	// Handle SSE events (blocking)
	shouldWaitForTitle := generateTitle && reqCtx.session.Title == ""
	h.handleAgentEventsForSSE(ctx, reqCtx.c, sessionID, reqCtx.assistantMessage.ID,
		reqCtx.requestID, streamCtx.eventBus, shouldWaitForTitle)
}

// runVLMAnalysisIfNeeded runs VLM image analysis within the async goroutine,
// emitting tool_call/tool_result events so the user can see progress.
// For normal mode, VLM only runs on the pure-chat path (no KB, no web search);
// RAG paths defer VLM to the pipeline rewrite step.
// For agent mode, VLM always runs when images and a VLM model are present.
func (h *Handler) runVLMAnalysisIfNeeded(streamCtx *sseStreamContext, reqCtx *qaRequestContext, mode qaMode) {
	if len(reqCtx.images) == 0 || reqCtx.customAgent == nil || reqCtx.customAgent.Config.VLMModelID == "" {
		return
	}

	sessionID := reqCtx.sessionID

	// In normal mode, only run VLM for pure-chat path
	if mode == qaModeNormal {
		hasRequestKBs := len(reqCtx.knowledgeBaseIDs) > 0 || len(reqCtx.knowledgeIDs) > 0
		agentWillResolveKBs := false
		if !hasRequestKBs && reqCtx.customAgent != nil && !reqCtx.customAgent.Config.RetrieveKBOnlyWhenMentioned {
			switch reqCtx.customAgent.Config.KBSelectionMode {
			case "all":
				agentWillResolveKBs = true
			case "selected", "":
				agentWillResolveKBs = len(reqCtx.customAgent.Config.KnowledgeBases) > 0
			case "none":
				agentWillResolveKBs = false
			default:
				agentWillResolveKBs = len(reqCtx.customAgent.Config.KnowledgeBases) > 0
			}
		}
		if hasRequestKBs || agentWillResolveKBs || reqCtx.webSearchEnabled {
			return // VLM will be handled by the pipeline rewrite step
		}
	}

	// Emit VLM tool call/result events
	toolCallID := uuid.New().String()
	iteration := 0 // agent mode uses iteration field

	streamCtx.eventBus.Emit(streamCtx.asyncCtx, event.Event{
		Type:      event.EventAgentToolCall,
		SessionID: sessionID,
		Data: event.AgentToolCallData{
			ToolCallID: toolCallID,
			ToolName:   "image_analysis",
			Iteration:  iteration,
		},
	})

	vlmStart := time.Now()
	h.analyzeImageAttachments(streamCtx.asyncCtx, reqCtx.images,
		reqCtx.customAgent.Config.VLMModelID, reqCtx.query)

	outputMsg := "已分析图片内容"
	if mode == qaModeAgent {
		outputMsg = "已查看图片内容"
	}
	streamCtx.eventBus.Emit(streamCtx.asyncCtx, event.Event{
		Type:      event.EventAgentToolResult,
		SessionID: sessionID,
		Data: event.AgentToolResultData{
			ToolCallID: toolCallID,
			ToolName:   "image_analysis",
			Output:     outputMsg,
			Success:    true,
			Duration:   time.Since(vlmStart).Milliseconds(),
			Iteration:  iteration,
		},
	})
}

// completeAssistantMessage marks an assistant message as complete, updates it,
// and asynchronously indexes the Q&A pair into the chat history knowledge base.
func (h *Handler) completeAssistantMessage(ctx context.Context, assistantMessage *types.Message, userQuery string) {
	assistantMessage.UpdatedAt = time.Now()
	assistantMessage.IsCompleted = true
	_ = h.messageService.UpdateMessage(ctx, assistantMessage)

	// Asynchronously index the Q&A pair into the chat history knowledge base for vector search.
	// Use WithoutCancel so the goroutine survives after the HTTP request context is done.
	bgCtx := context.WithoutCancel(ctx)
	go h.messageService.IndexMessageToKB(bgCtx, userQuery, assistantMessage.Content, assistantMessage.ID, assistantMessage.SessionID)
}
