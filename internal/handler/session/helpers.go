package session

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// convertMentionedItems MentionedItemRequest 슬라이스를 types.MentionedItems로 변환
func convertMentionedItems(items []MentionedItemRequest) types.MentionedItems {
	if len(items) == 0 {
		return nil
	}
	result := make(types.MentionedItems, len(items))
	for i, item := range items {
		result[i] = types.MentionedItem{
			ID:     item.ID,
			Name:   item.Name,
			Type:   item.Type,
			KBType: item.KBType,
		}
	}
	return result
}

// setSSEHeaders 표준 Server-Sent Events 헤더 설정
func setSSEHeaders(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
}

// buildStreamResponse StreamEvent에서 StreamResponse 생성
func buildStreamResponse(evt interfaces.StreamEvent, requestID string) *types.StreamResponse {
	response := &types.StreamResponse{
		ID:           requestID,
		ResponseType: evt.Type,
		Content:      evt.Content,
		Done:         evt.Done,
		Data:         evt.Data,
	}

	// agent_query 이벤트에 대해 session_id 및 assistant_message_id 추출
	if evt.Type == types.ResponseTypeAgentQuery {
		if sid, ok := evt.Data["session_id"].(string); ok {
			response.SessionID = sid
		}
		if amid, ok := evt.Data["assistant_message_id"].(string); ok {
			response.AssistantMessageID = amid
		}
	}

	// references 이벤트에 대한 특수 처리
	if evt.Type == types.ResponseTypeReferences {
		refsData := evt.Data["references"]
		if refs, ok := refsData.(types.References); ok {
			response.KnowledgeReferences = refs
		} else if refs, ok := refsData.([]*types.SearchResult); ok {
			response.KnowledgeReferences = types.References(refs)
		} else if refs, ok := refsData.([]interface{}); ok {
			// 데이터가 직렬화/역직렬화된 경우(예: Redis에서) 처리
			searchResults := make([]*types.SearchResult, 0, len(refs))
			for _, ref := range refs {
				if refMap, ok := ref.(map[string]interface{}); ok {
					sr := &types.SearchResult{
						ID:                getString(refMap, "id"),
						Content:           getString(refMap, "content"),
						KnowledgeID:       getString(refMap, "knowledge_id"),
						ChunkIndex:        int(getFloat64(refMap, "chunk_index")),
						KnowledgeTitle:    getString(refMap, "knowledge_title"),
						StartAt:           int(getFloat64(refMap, "start_at")),
						EndAt:             int(getFloat64(refMap, "end_at")),
						Seq:               int(getFloat64(refMap, "seq")),
						Score:             getFloat64(refMap, "score"),
						ChunkType:         getString(refMap, "chunk_type"),
						ParentChunkID:     getString(refMap, "parent_chunk_id"),
						ImageInfo:         getString(refMap, "image_info"),
						KnowledgeFilename: getString(refMap, "knowledge_filename"),
						KnowledgeSource:   getString(refMap, "knowledge_source"),
					}
					searchResults = append(searchResults, sr)
				}
			}
			response.KnowledgeReferences = types.References(searchResults)
		}
	}

	return response
}

// sendCompletionEvent 클라이언트에 최종 완료 이벤트 전송
func sendCompletionEvent(c *gin.Context, requestID string) {
	c.SSEvent("message", &types.StreamResponse{
		ID:           requestID,
		ResponseType: types.ResponseTypeAnswer,
		Content:      "",
		Done:         true,
	})
	c.Writer.Flush()
}

// createAgentQueryEvent 표준 에이전트 쿼리 이벤트 생성
func createAgentQueryEvent(sessionID, assistantMessageID string) interfaces.StreamEvent {
	return interfaces.StreamEvent{
		ID:        fmt.Sprintf("query-%d", time.Now().UnixNano()),
		Type:      types.ResponseTypeAgentQuery,
		Content:   "",
		Done:      true,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"session_id":           sessionID,
			"assistant_message_id": assistantMessageID,
		},
	}
}

// createUserMessage 사용자 메시지 생성
func (h *Handler) createUserMessage(ctx context.Context, sessionID, query, requestID string, mentionedItems types.MentionedItems) error {
	_, err := h.messageService.CreateMessage(ctx, &types.Message{
		SessionID:      sessionID,
		Role:           "user",
		Content:        query,
		RequestID:      requestID,
		CreatedAt:      time.Now(),
		IsCompleted:    true,
		MentionedItems: mentionedItems,
	})
	return err
}

// createAssistantMessage 어시스턴트 메시지 생성
func (h *Handler) createAssistantMessage(ctx context.Context, assistantMessage *types.Message) (*types.Message, error) {
	assistantMessage.CreatedAt = time.Now()
	return h.messageService.CreateMessage(ctx, assistantMessage)
}

// setupStreamHandler 스트림 핸들러 생성 및 구독
func (h *Handler) setupStreamHandler(
	ctx context.Context,
	sessionID, assistantMessageID, requestID string,
	assistantMessage *types.Message,
	eventBus *event.EventBus,
) *AgentStreamHandler {
	streamHandler := NewAgentStreamHandler(
		ctx, sessionID, assistantMessageID, requestID,
		assistantMessage, h.streamManager, eventBus,
	)
	streamHandler.Subscribe()
	return streamHandler
}

// setupStopEventHandler 중지 이벤트 핸들러 등록
func (h *Handler) setupStopEventHandler(
	eventBus *event.EventBus,
	sessionID string,
	assistantMessage *types.Message,
	cancel context.CancelFunc,
) {
	eventBus.On(event.EventStop, func(ctx context.Context, evt event.Event) error {
		logger.Infof(ctx, "Received stop event, cancelling async operations for session: %s", sessionID)
		cancel()
		assistantMessage.Content = "사용자가 대화를 중지했습니다"
		h.completeAssistantMessage(ctx, assistantMessage)
		return nil
	})
}

// writeAgentQueryEvent 스트림 관리자에 에이전트 쿼리 이벤트 기록
func (h *Handler) writeAgentQueryEvent(ctx context.Context, sessionID, assistantMessageID string) {
	agentQueryEvent := createAgentQueryEvent(sessionID, assistantMessageID)
	if err := h.streamManager.AppendEvent(ctx, sessionID, assistantMessageID, agentQueryEvent); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": sessionID,
			"message_id": assistantMessageID,
		})
		// 치명적이지 않은 오류, 계속 진행
	}
}

// getRequestID gin 컨텍스트에서 요청 ID 가져오기
func getRequestID(c *gin.Context) string {
	return c.GetString(types.RequestIDContextKey.String())
}

// 기본값으로 타입 단언을 위한 헬퍼 함수
func getString(m map[string]interface{}, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

func getFloat64(m map[string]interface{}, key string) float64 {
	if val, ok := m[key].(float64); ok {
		return val
	}
	if val, ok := m[key].(int); ok {
		return float64(val)
	}
	return 0.0
}

// createDefaultSummaryConfig 구성에서 기본 요약 구성 생성
// 테넌트 수준 ConversationConfig를 우선으로 하고, config.yaml 기본값으로 대체
func (h *Handler) createDefaultSummaryConfig(ctx context.Context) *types.SummaryConfig {
	// 컨텍스트에서 테넌트 가져오기 시도
	tenant, _ := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)

	// config.yaml 기본값으로 초기화
	cfg := &types.SummaryConfig{
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

	// 사용 가능한 경우 테넌트 수준 대화 구성으로 덮어쓰기
	if tenant != nil && tenant.ConversationConfig != nil {
		// 제공된 경우 사용자 정의 프롬프트 사용
		if tenant.ConversationConfig.Prompt != "" {
			cfg.Prompt = tenant.ConversationConfig.Prompt
		}

		// 제공된 경우 사용자 정의 컨텍스트 템플릿 사용
		if tenant.ConversationConfig.ContextTemplate != "" {
			cfg.ContextTemplate = tenant.ConversationConfig.ContextTemplate
		}
		if tenant.ConversationConfig.Temperature > 0 {
			cfg.Temperature = tenant.ConversationConfig.Temperature
		}
		if tenant.ConversationConfig.MaxCompletionTokens > 0 {
			cfg.MaxCompletionTokens = tenant.ConversationConfig.MaxCompletionTokens
		}
	}

	return cfg
}

// fillSummaryConfigDefaults 요약 구성의 누락된 필드를 기본값으로 채우기
// 테넌트 수준 ConversationConfig를 우선으로 하고, config.yaml 기본값으로 대체
func (h *Handler) fillSummaryConfigDefaults(ctx context.Context, config *types.SummaryConfig) {
	// 컨텍스트에서 테넌트 가져오기 시도
	tenant, _ := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)

	// 기본값 결정: 테넌트 구성 우선, 그 다음 config.yaml
	var defaultPrompt, defaultContextTemplate, defaultNoMatchPrefix string
	var defaultTemperature float64
	var defaultMaxCompletionTokens int

	if tenant != nil && tenant.ConversationConfig != nil {
		// 제공된 경우 사용자 정의 프롬프트 사용
		if tenant.ConversationConfig.Prompt != "" {
			defaultPrompt = tenant.ConversationConfig.Prompt
		}

		// 제공된 경우 사용자 정의 컨텍스트 템플릿 사용
		if tenant.ConversationConfig.ContextTemplate != "" {
			defaultContextTemplate = tenant.ConversationConfig.ContextTemplate
		}
		defaultTemperature = tenant.ConversationConfig.Temperature
		defaultMaxCompletionTokens = tenant.ConversationConfig.MaxCompletionTokens
	}

	// 테넌트 구성이 비어 있으면 config.yaml로 대체
	if defaultPrompt == "" {
		defaultPrompt = h.config.Conversation.Summary.Prompt
	}
	if defaultContextTemplate == "" {
		defaultContextTemplate = h.config.Conversation.Summary.ContextTemplate
	}
	if defaultTemperature == 0 {
		defaultTemperature = h.config.Conversation.Summary.Temperature
	}
	if defaultMaxCompletionTokens == 0 {
		defaultMaxCompletionTokens = h.config.Conversation.Summary.MaxCompletionTokens
	}
	defaultNoMatchPrefix = h.config.Conversation.Summary.NoMatchPrefix

	// 누락된 필드 채우기
	if config.Prompt == "" {
		config.Prompt = defaultPrompt
	}
	if config.ContextTemplate == "" {
		config.ContextTemplate = defaultContextTemplate
	}
	if config.Temperature == 0 {
		config.Temperature = defaultTemperature
	}
	if config.MaxCompletionTokens == 0 {
		config.MaxCompletionTokens = defaultMaxCompletionTokens
	}
	if config.NoMatchPrefix == "" {
		config.NoMatchPrefix = defaultNoMatchPrefix
	}
}
