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

// qaRequestContext QA 요청에 필요한 모든 공통 데이터를 보유합니다.
type qaRequestContext struct {
	ctx              context.Context
	c                *gin.Context
	sessionID        string
	requestID        string
	query            string
	session          *types.Session
	customAgent      *types.CustomAgent
	assistantMessage *types.Message
	knowledgeBaseIDs []string
	knowledgeIDs     []string
	summaryModelID   string
	webSearchEnabled bool
	mentionedItems   types.MentionedItems
}

// parseQARequest QA 요청을 파싱하고 검증하며, 요청 컨텍스트를 반환합니다.
func (h *Handler) parseQARequest(c *gin.Context, logPrefix string) (*qaRequestContext, *CreateKnowledgeQARequest, error) {
	ctx := logger.CloneContext(c.Request.Context())
	logger.Infof(ctx, "[%s] Start processing request", logPrefix)

	// URL 매개변수에서 세션 ID 가져오기
	sessionID := secutils.SanitizeForLog(c.Param("session_id"))
	if sessionID == "" {
		logger.Error(ctx, "Session ID is empty")
		return nil, nil, errors.NewBadRequestError(errors.ErrInvalidSessionID.Error())
	}

	// 요청 본문 파싱
	var request CreateKnowledgeQARequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.Error(ctx, "Failed to parse request data", err)
		return nil, nil, errors.NewBadRequestError(err.Error())
	}

	// 쿼리 내용 검증
	if request.Query == "" {
		logger.Error(ctx, "Query content is empty")
		return nil, nil, errors.NewBadRequestError("Query content cannot be empty")
	}

	// 요청 세부 정보 로깅
	if requestJSON, err := json.Marshal(request); err == nil {
		logger.Infof(ctx, "[%s] Request: session_id=%s, request=%s",
			logPrefix, sessionID, secutils.SanitizeForLog(string(requestJSON)))
	}

	// 세션 가져오기
	session, err := h.sessionService.GetSession(ctx, sessionID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get session, session ID: %s, error: %v", sessionID, err)
		return nil, nil, errors.NewNotFoundError("Session not found")
	}

	// agent_id가 제공된 경우 사용자 정의 에이전트 가져오기
	var customAgent *types.CustomAgent
	if request.AgentID != "" {
		logger.Infof(ctx, "Fetching custom agent, agent ID: %s", secutils.SanitizeForLog(request.AgentID))
		agent, err := h.customAgentService.GetAgentByID(ctx, request.AgentID)
		if err != nil {
			logger.Warnf(ctx, "Failed to get custom agent, agent ID: %s, error: %v, using default config",
				secutils.SanitizeForLog(request.AgentID), err)
		} else {
			customAgent = agent
			logger.Infof(ctx, "Using custom agent: ID=%s, Name=%s, IsBuiltin=%v, AgentMode=%s",
				customAgent.ID, customAgent.Name, customAgent.IsBuiltin, customAgent.Config.AgentMode)
		}
	}

	// 요청 컨텍스트 구축
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
		},
		knowledgeBaseIDs: secutils.SanitizeForLogArray(request.KnowledgeBaseIDs),
		knowledgeIDs:     secutils.SanitizeForLogArray(request.KnowledgeIds),
		summaryModelID:   secutils.SanitizeForLog(request.SummaryModelID),
		webSearchEnabled: request.WebSearchEnabled,
		mentionedItems:   convertMentionedItems(request.MentionedItems),
	}

	return reqCtx, &request, nil
}

// sseStreamContext SSE 스트리밍을 위한 컨텍스트를 보유합니다.
type sseStreamContext struct {
	eventBus         *event.EventBus
	asyncCtx         context.Context
	cancel           context.CancelFunc
	assistantMessage *types.Message
}

// setupSSEStream SSE 스트리밍 컨텍스트를 설정합니다.
func (h *Handler) setupSSEStream(reqCtx *qaRequestContext, generateTitle bool) *sseStreamContext {
	// SSE 헤더 설정
	setSSEHeaders(reqCtx.c)

	// 초기 agent_query 이벤트 기록
	h.writeAgentQueryEvent(reqCtx.ctx, reqCtx.sessionID, reqCtx.assistantMessage.ID)

	// EventBus 및 취소 가능한 컨텍스트 생성
	eventBus := event.NewEventBus()
	asyncCtx, cancel := context.WithCancel(logger.CloneContext(reqCtx.ctx))

	streamCtx := &sseStreamContext{
		eventBus:         eventBus,
		asyncCtx:         asyncCtx,
		cancel:           cancel,
		assistantMessage: reqCtx.assistantMessage,
	}

	// 중지 이벤트 핸들러 설정
	h.setupStopEventHandler(eventBus, reqCtx.sessionID, reqCtx.assistantMessage, cancel)

	// 스트림 핸들러 설정
	h.setupStreamHandler(asyncCtx, reqCtx.sessionID, reqCtx.assistantMessage.ID,
		reqCtx.requestID, reqCtx.assistantMessage, eventBus)

	// 필요한 경우 제목 생성
	if generateTitle && reqCtx.session.Title == "" {
		// 제목 생성에 대화와 동일한 모델 사용
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
// @Summary      지식 검색
// @Description  지식베이스에서 검색 (LLM 요약 미사용)
// @Tags         질의응답
// @Accept       json
// @Produce      json
// @Param        request  body      SearchKnowledgeRequest  true  "검색 요청"
// @Success      200      {object}  map[string]interface{}  "검색 결과"
// @Failure      400      {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/search [post]
func (h *Handler) SearchKnowledge(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())
	logger.Info(ctx, "Start processing knowledge search request")

	// 요청 본문 파싱
	var request SearchKnowledgeRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.Error(ctx, "Failed to parse request data", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// 요청 매개변수 검증
	if request.Query == "" {
		logger.Error(ctx, "Query content is empty")
		c.Error(errors.NewBadRequestError("Query content cannot be empty"))
		return
	}

	// 하위 호환성을 위해 단일 knowledge_base_id를 knowledge_base_ids에 병합
	knowledgeBaseIDs := request.KnowledgeBaseIDs
	if request.KnowledgeBaseID != "" {
		// 중복 방지를 위해 이미 목록에 있는지 확인
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

	// LLM 요약 없이 지식 검색 서비스 직접 호출
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
// @Summary      지식 질의응답
// @Description  지식베이스 기반 질의응답 (LLM 요약 사용), SSE 스트리밍 응답 지원
// @Tags         질의응답
// @Accept       json
// @Produce      text/event-stream
// @Param        session_id  path      string                   true  "세션 ID"
// @Param        request     body      CreateKnowledgeQARequest true  "질의응답 요청"
// @Success      200         {object}  map[string]interface{}   "질의응답 결과 (SSE 스트림)"
// @Failure      400         {object}  errors.AppError          "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/{session_id}/knowledge-qa [post]
func (h *Handler) KnowledgeQA(c *gin.Context) {
	// 요청 파싱 및 검증
	reqCtx, request, err := h.parseQARequest(c, "KnowledgeQA")
	if err != nil {
		c.Error(err)
		return
	}

	// 일반 모드 QA 실행, 비활성화되지 않은 경우 제목 생성
	h.executeNormalModeQA(reqCtx, !request.DisableTitle)
}

// AgentQA godoc
// @Summary      에이전트 질의응답
// @Description  에이전트 기반 지능형 질의응답, 멀티턴 대화 및 SSE 스트리밍 응답 지원
// @Tags         질의응답
// @Accept       json
// @Produce      text/event-stream
// @Param        session_id  path      string                   true  "세션 ID"
// @Param        request     body      CreateKnowledgeQARequest true  "질의응답 요청"
// @Success      200         {object}  map[string]interface{}   "질의응답 결과 (SSE 스트림)"
// @Failure      400         {object}  errors.AppError          "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/{session_id}/agent-qa [post]
func (h *Handler) AgentQA(c *gin.Context) {
	// 요청 파싱 및 검증
	reqCtx, request, err := h.parseQARequest(c, "AgentQA")
	if err != nil {
		c.Error(err)
		return
	}

	// 에이전트 모드 활성화 여부 결정
	// 우선순위: customAgent.IsAgentMode() > request.AgentEnabled
	agentModeEnabled := request.AgentEnabled
	if reqCtx.customAgent != nil {
		agentModeEnabled = reqCtx.customAgent.IsAgentMode()
		logger.Infof(reqCtx.ctx, "Agent mode determined by custom agent: %v (config.agent_mode=%s)",
			agentModeEnabled, reqCtx.customAgent.Config.AgentMode)
	}

	// 에이전트 모드에 따라 적절한 핸들러로 라우팅
	if agentModeEnabled {
		h.executeAgentModeQA(reqCtx)
	} else {
		logger.Infof(reqCtx.ctx, "Agent mode disabled, delegating to normal mode for session: %s", reqCtx.sessionID)
		// 지식베이스는 요청 또는 사용자 정의 에이전트에서 지정되어야 함
		h.executeNormalModeQA(reqCtx, false)
	}
}

// executeNormalModeQA 일반 (KnowledgeQA) 모드 실행
func (h *Handler) executeNormalModeQA(reqCtx *qaRequestContext, generateTitle bool) {
	ctx := reqCtx.ctx
	sessionID := reqCtx.sessionID

	// 사용자 메시지 생성
	if err := h.createUserMessage(ctx, sessionID, reqCtx.query, reqCtx.requestID, reqCtx.mentionedItems); err != nil {
		reqCtx.c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// 어시스턴트 메시지 생성
	if _, err := h.createAssistantMessage(ctx, reqCtx.assistantMessage); err != nil {
		reqCtx.c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Using knowledge bases: %v", reqCtx.knowledgeBaseIDs)

	// SSE 스트림 설정
	streamCtx := h.setupSSEStream(reqCtx, generateTitle)

	// 일반 모드에 대한 완료 핸들러 설정
	streamCtx.eventBus.On(event.EventAgentFinalAnswer, func(ctx context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentFinalAnswerData)
		if !ok {
			return nil
		}
		streamCtx.assistantMessage.Content += data.Content
		if data.Done {
			logger.Infof(streamCtx.asyncCtx, "Knowledge QA service completed for session: %s", sessionID)
			h.completeAssistantMessage(streamCtx.asyncCtx, streamCtx.assistantMessage)
			streamCtx.eventBus.Emit(streamCtx.asyncCtx, event.Event{
				Type:      event.EventAgentComplete,
				SessionID: sessionID,
				Data:      event.AgentCompleteData{FinalAnswer: streamCtx.assistantMessage.Content},
			})
			streamCtx.cancel()
		}
		return nil
	})

	// KnowledgeQA 비동기 실행
	go func() {
		defer func() {
			if r := recover(); r != nil {
				buf := make([]byte, 10240)
				runtime.Stack(buf, true)
				logger.ErrorWithFields(streamCtx.asyncCtx,
					errors.NewInternalServerError(fmt.Sprintf("Knowledge QA service panicked: %v\n%s", r, string(buf))), nil)
			}
		}()

		err := h.sessionService.KnowledgeQA(
			streamCtx.asyncCtx,
			reqCtx.session,
			reqCtx.query,
			reqCtx.knowledgeBaseIDs,
			reqCtx.knowledgeIDs,
			reqCtx.assistantMessage.ID,
			reqCtx.summaryModelID,
			reqCtx.webSearchEnabled,
			streamCtx.eventBus,
			reqCtx.customAgent,
		)
		if err != nil {
			logger.ErrorWithFields(streamCtx.asyncCtx, err, nil)
			streamCtx.eventBus.Emit(streamCtx.asyncCtx, event.Event{
				Type:      event.EventError,
				SessionID: sessionID,
				Data: event.ErrorData{
					Error:     err.Error(),
					Stage:     "knowledge_qa_execution",
					SessionID: sessionID,
				},
			})
		}
	}()

	// SSE 이벤트 처리 (블로킹)
	shouldWaitForTitle := generateTitle && reqCtx.session.Title == ""
	h.handleAgentEventsForSSE(ctx, reqCtx.c, sessionID, reqCtx.assistantMessage.ID,
		reqCtx.requestID, streamCtx.eventBus, shouldWaitForTitle)
}

// executeAgentModeQA 에이전트 모드 실행
func (h *Handler) executeAgentModeQA(reqCtx *qaRequestContext) {
	ctx := reqCtx.ctx
	sessionID := reqCtx.sessionID

	// 에이전트 쿼리 이벤트 방출
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

	// 사용자 메시지 생성
	if err := h.createUserMessage(ctx, sessionID, reqCtx.query, reqCtx.requestID, reqCtx.mentionedItems); err != nil {
		reqCtx.c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// 어시스턴트 메시지 생성
	assistantMessagePtr, err := h.createAssistantMessage(ctx, reqCtx.assistantMessage)
	if err != nil {
		reqCtx.c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	reqCtx.assistantMessage = assistantMessagePtr

	logger.Infof(ctx, "Calling agent QA service, session ID: %s", sessionID)

	// SSE 스트림 설정 (에이전트 모드는 항상 제목 생성)
	streamCtx := h.setupSSEStream(reqCtx, true)

	// AgentQA 비동기 실행
	go func() {
		defer func() {
			if r := recover(); r != nil {
				buf := make([]byte, 1024)
				runtime.Stack(buf, true)
				logger.ErrorWithFields(streamCtx.asyncCtx,
					errors.NewInternalServerError(fmt.Sprintf("Agent QA service panicked: %v\n%s", r, string(buf))),
					map[string]interface{}{"session_id": sessionID})
			}
			h.completeAssistantMessage(streamCtx.asyncCtx, streamCtx.assistantMessage)
			logger.Infof(streamCtx.asyncCtx, "Agent QA service completed for session: %s", sessionID)
		}()

		err := h.sessionService.AgentQA(
			streamCtx.asyncCtx,
			reqCtx.session,
			reqCtx.query,
			reqCtx.assistantMessage.ID,
			reqCtx.summaryModelID,
			streamCtx.eventBus,
			reqCtx.customAgent,
			reqCtx.knowledgeBaseIDs,
			reqCtx.knowledgeIDs,
		)
		if err != nil {
			logger.ErrorWithFields(streamCtx.asyncCtx, err, nil)
			streamCtx.eventBus.Emit(streamCtx.asyncCtx, event.Event{
				Type:      event.EventError,
				SessionID: sessionID,
				Data: event.ErrorData{
					Error:     err.Error(),
					Stage:     "agent_execution",
					SessionID: sessionID,
				},
			})
		}
	}()

	// SSE 이벤트 처리 (블로킹)
	h.handleAgentEventsForSSE(ctx, reqCtx.c, sessionID, reqCtx.assistantMessage.ID,
		reqCtx.requestID, streamCtx.eventBus, reqCtx.session.Title == "")
}

// completeAssistantMessage 어시스턴트 메시지를 완료로 표시하고 업데이트
func (h *Handler) completeAssistantMessage(ctx context.Context, assistantMessage *types.Message) {
	assistantMessage.UpdatedAt = time.Now()
	assistantMessage.IsCompleted = true
	_ = h.messageService.UpdateMessage(ctx, assistantMessage)
}
