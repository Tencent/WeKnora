package session

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// AgentStreamHandler SSE 스트리밍을 위한 에이전트 이벤트를 처리합니다.
// SessionID 필터링을 피하기 위해 요청당 전용 EventBus를 사용합니다.
// 이벤트는 누적되지 않고 StreamManager에 추가됩니다.
type AgentStreamHandler struct {
	ctx                context.Context
	sessionID          string
	assistantMessageID string
	requestID          string
	assistantMessage   *types.Message
	streamManager      interfaces.StreamManager

	eventBus *event.EventBus

	// 상태 추적
	knowledgeRefs   []*types.SearchResult
	finalAnswer     string
	eventStartTimes map[string]time.Time // 기간 계산을 위한 시작 시간 추적
	mu              sync.Mutex
}

// NewAgentStreamHandler 에이전트 SSE 스트리밍을 위한 새 핸들러 생성
func NewAgentStreamHandler(
	ctx context.Context,
	sessionID, assistantMessageID, requestID string,
	assistantMessage *types.Message,
	streamManager interfaces.StreamManager,
	eventBus *event.EventBus,
) *AgentStreamHandler {
	return &AgentStreamHandler{
		ctx:                ctx,
		sessionID:          sessionID,
		assistantMessageID: assistantMessageID,
		requestID:          requestID,
		assistantMessage:   assistantMessage,
		streamManager:      streamManager,
		eventBus:           eventBus,
		knowledgeRefs:      make([]*types.SearchResult, 0),
		eventStartTimes:    make(map[string]time.Time),
	}
}

// Subscribe 전용 EventBus의 모든 에이전트 스트리밍 이벤트를 구독합니다.
// 요청당 전용 EventBus가 있으므로 SessionID 필터링이 필요하지 않습니다.
func (h *AgentStreamHandler) Subscribe() {
	// 전용 EventBus의 모든 에이전트 스트리밍 이벤트 구독
	h.eventBus.On(event.EventAgentThought, h.handleThought)
	h.eventBus.On(event.EventAgentToolCall, h.handleToolCall)
	h.eventBus.On(event.EventAgentToolResult, h.handleToolResult)
	h.eventBus.On(event.EventAgentReferences, h.handleReferences)
	h.eventBus.On(event.EventAgentFinalAnswer, h.handleFinalAnswer)
	h.eventBus.On(event.EventAgentReflection, h.handleReflection)
	h.eventBus.On(event.EventError, h.handleError)
	h.eventBus.On(event.EventSessionTitle, h.handleSessionTitle)
	h.eventBus.On(event.EventAgentComplete, h.handleComplete)
}

// handleThought 에이전트 생각 이벤트 처리
func (h *AgentStreamHandler) handleThought(ctx context.Context, evt event.Event) error {
	data, ok := evt.Data.(event.AgentThoughtData)
	if !ok {
		return nil
	}

	h.mu.Lock()

	// 첫 번째 청크에서 시작 시간 추적
	if _, exists := h.eventStartTimes[evt.ID]; !exists {
		h.eventStartTimes[evt.ID] = time.Now()
	}

	// 완료 시 기간 계산
	var metadata map[string]interface{}
	if data.Done {
		startTime := h.eventStartTimes[evt.ID]
		duration := time.Since(startTime)
		metadata = map[string]interface{}{
			"event_id":     evt.ID,
			"duration_ms":  duration.Milliseconds(),
			"completed_at": time.Now().Unix(),
		}
		delete(h.eventStartTimes, evt.ID)
	} else {
		metadata = map[string]interface{}{
			"event_id": evt.ID,
		}
	}

	h.mu.Unlock()

	// 이 청크를 스트림에 추가 (누적 없음 - 프론트엔드에서 누적)
	if err := h.streamManager.AppendEvent(h.ctx, h.sessionID, h.assistantMessageID, interfaces.StreamEvent{
		ID:        evt.ID,
		Type:      types.ResponseTypeThinking,
		Content:   data.Content, // 이 청크만
		Done:      data.Done,
		Timestamp: time.Now(),
		Data:      metadata,
	}); err != nil {
		logger.GetLogger(h.ctx).Error("Append thought event to stream failed", "error", err)
	}

	return nil
}

// handleToolCall 도구 호출 이벤트 처리
func (h *AgentStreamHandler) handleToolCall(ctx context.Context, evt event.Event) error {
	data, ok := evt.Data.(event.AgentToolCallData)
	if !ok {
		return nil
	}

	h.mu.Lock()
	// 이 도구 호출에 대한 시작 시간 추적 (tool_call_id를 키로 사용)
	h.eventStartTimes[data.ToolCallID] = time.Now()
	h.mu.Unlock()

	metadata := map[string]interface{}{
		"tool_name":    data.ToolName,
		"arguments":    data.Arguments,
		"tool_call_id": data.ToolCallID,
	}

	// 이벤트를 스트림에 추가
	if err := h.streamManager.AppendEvent(h.ctx, h.sessionID, h.assistantMessageID, interfaces.StreamEvent{
		ID:        evt.ID,
		Type:      types.ResponseTypeToolCall,
		Content:   fmt.Sprintf("Calling tool: %s", data.ToolName),
		Done:      false,
		Timestamp: time.Now(),
		Data:      metadata,
	}); err != nil {
		logger.GetLogger(h.ctx).Error("Append tool call event to stream failed", "error", err)
	}

	return nil
}

// handleToolResult 도구 결과 이벤트 처리
func (h *AgentStreamHandler) handleToolResult(ctx context.Context, evt event.Event) error {
	data, ok := evt.Data.(event.AgentToolResultData)
	if !ok {
		return nil
	}

	h.mu.Lock()
	// 가능한 경우 시작 시간에서 기간 계산, 그렇지 않으면 제공된 기간 사용
	var durationMs int64
	if startTime, exists := h.eventStartTimes[data.ToolCallID]; exists {
		durationMs = time.Since(startTime).Milliseconds()
		delete(h.eventStartTimes, data.ToolCallID)
	} else if data.Duration > 0 {
		// 시작 시간이 추적되지 않은 경우 제공된 기간으로 대체
		durationMs = data.Duration
	}
	h.mu.Unlock()

	// SSE 응답 전송 (성공 및 실패 모두)
	responseType := types.ResponseTypeToolResult
	content := data.Output
	if !data.Success {
		responseType = types.ResponseTypeError
		if data.Error != "" {
			content = data.Error
		}
	}

	// 풍부한 프론트엔드 렌더링을 위해 도구 결과 데이터를 포함한 메타데이터 구축
	metadata := map[string]interface{}{
		"tool_name":    data.ToolName,
		"success":      data.Success,
		"output":       data.Output,
		"error":        data.Error,
		"duration_ms":  durationMs,
		"tool_call_id": data.ToolCallID,
	}

	// 도구 결과 데이터 병합 (display_type, 포맷된 결과 등 포함)
	if data.Data != nil {
		for k, v := range data.Data {
			metadata[k] = v
		}
	}

	// 이벤트를 스트림에 추가
	if err := h.streamManager.AppendEvent(h.ctx, h.sessionID, h.assistantMessageID, interfaces.StreamEvent{
		ID:        evt.ID,
		Type:      responseType,
		Content:   content,
		Done:      false,
		Timestamp: time.Now(),
		Data:      metadata,
	}); err != nil {
		logger.GetLogger(h.ctx).Error("Append tool result event to stream failed", "error", err)
	}

	return nil
}

// handleReferences 지식 참조 이벤트 처리
func (h *AgentStreamHandler) handleReferences(ctx context.Context, evt event.Event) error {
	data, ok := evt.Data.(event.AgentReferencesData)
	if !ok {
		return nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// 지식 참조 추출
	// 먼저 []*types.SearchResult로 직접 캐스팅 시도
	if searchResults, ok := data.References.([]*types.SearchResult); ok {
		h.knowledgeRefs = append(h.knowledgeRefs, searchResults...)
	} else if refs, ok := data.References.([]interface{}); ok {
		// 대체: []interface{}에서 변환
		for _, ref := range refs {
			if sr, ok := ref.(*types.SearchResult); ok {
				h.knowledgeRefs = append(h.knowledgeRefs, sr)
			} else if refMap, ok := ref.(map[string]interface{}); ok {
				// 필요한 경우 맵에서 파싱
				searchResult := &types.SearchResult{
					ID:             getString(refMap, "id"),
					Content:        getString(refMap, "content"),
					Score:          getFloat64(refMap, "score"),
					KnowledgeID:    getString(refMap, "knowledge_id"),
					KnowledgeTitle: getString(refMap, "knowledge_title"),
					ChunkIndex:     int(getFloat64(refMap, "chunk_index")),
				}

				if meta, ok := refMap["metadata"].(map[string]interface{}); ok {
					metadata := make(map[string]string)
					for k, v := range meta {
						if strVal, ok := v.(string); ok {
							metadata[k] = strVal
						}
					}
					searchResult.Metadata = metadata
				}

				h.knowledgeRefs = append(h.knowledgeRefs, searchResult)
			}
		}
	}

	// 어시스턴트 메시지 참조 업데이트
	h.assistantMessage.KnowledgeReferences = h.knowledgeRefs

	// 참조 이벤트를 스트림에 추가
	if err := h.streamManager.AppendEvent(h.ctx, h.sessionID, h.assistantMessageID, interfaces.StreamEvent{
		ID:        evt.ID,
		Type:      types.ResponseTypeReferences,
		Content:   "",
		Done:      false,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"references": types.References(h.knowledgeRefs),
		},
	}); err != nil {
		logger.GetLogger(h.ctx).Error("Append references event to stream failed", "error", err)
	}

	return nil
}

// handleFinalAnswer 최종 답변 이벤트 처리
func (h *AgentStreamHandler) handleFinalAnswer(ctx context.Context, evt event.Event) error {
	data, ok := evt.Data.(event.AgentFinalAnswerData)
	if !ok {
		return nil
	}

	h.mu.Lock()
	// 첫 번째 청크에서 시작 시간 추적
	if _, exists := h.eventStartTimes[evt.ID]; !exists {
		h.eventStartTimes[evt.ID] = time.Now()
	}

	// 어시스턴트 메시지(데이터베이스)를 위해 로컬에 최종 답변 누적
	h.finalAnswer += data.Content

	// 완료 시 기간 계산
	var metadata map[string]interface{}
	if data.Done {
		startTime := h.eventStartTimes[evt.ID]
		duration := time.Since(startTime)
		metadata = map[string]interface{}{
			"event_id":     evt.ID,
			"duration_ms":  duration.Milliseconds(),
			"completed_at": time.Now().Unix(),
		}
		delete(h.eventStartTimes, evt.ID)
	} else {
		metadata = map[string]interface{}{
			"event_id": evt.ID,
		}
	}
	h.mu.Unlock()

	// 이 청크를 스트림에 추가 (프론트엔드에서 이벤트 ID별로 누적)
	if err := h.streamManager.AppendEvent(h.ctx, h.sessionID, h.assistantMessageID, interfaces.StreamEvent{
		ID:        evt.ID,
		Type:      types.ResponseTypeAnswer,
		Content:   data.Content, // 이 청크만
		Done:      data.Done,
		Timestamp: time.Now(),
		Data:      metadata,
	}); err != nil {
		logger.GetLogger(h.ctx).Error("Append answer event to stream failed", "error", err)
	}

	return nil
}

// handleReflection 에이전트 성찰 이벤트 처리
func (h *AgentStreamHandler) handleReflection(ctx context.Context, evt event.Event) error {
	data, ok := evt.Data.(event.AgentReflectionData)
	if !ok {
		return nil
	}

	// 이 청크를 스트림에 추가 (프론트엔드에서 이벤트 ID별로 누적)
	if err := h.streamManager.AppendEvent(h.ctx, h.sessionID, h.assistantMessageID, interfaces.StreamEvent{
		ID:        evt.ID,
		Type:      types.ResponseTypeReflection,
		Content:   data.Content, // 이 청크만
		Done:      data.Done,
		Timestamp: time.Now(),
	}); err != nil {
		logger.GetLogger(h.ctx).Error("Append reflection event to stream failed", "error", err)
	}

	return nil
}

// handleError 오류 이벤트 처리
func (h *AgentStreamHandler) handleError(ctx context.Context, evt event.Event) error {
	data, ok := evt.Data.(event.ErrorData)
	if !ok {
		return nil
	}

	// 오류 메타데이터 구축
	metadata := map[string]interface{}{
		"stage": data.Stage,
		"error": data.Error,
	}

	// 오류 이벤트를 스트림에 추가
	if err := h.streamManager.AppendEvent(h.ctx, h.sessionID, h.assistantMessageID, interfaces.StreamEvent{
		ID:        evt.ID,
		Type:      types.ResponseTypeError,
		Content:   data.Error,
		Done:      true,
		Timestamp: time.Now(),
		Data:      metadata,
	}); err != nil {
		logger.GetLogger(h.ctx).Error("Append error event to stream failed", "error", err)
	}

	return nil
}

// handleSessionTitle 세션 제목 업데이트 이벤트 처리
func (h *AgentStreamHandler) handleSessionTitle(ctx context.Context, evt event.Event) error {
	data, ok := evt.Data.(event.SessionTitleData)
	if !ok {
		return nil
	}

	// 제목 이벤트는 스트림 완료 후에 도착할 수 있으므로 백그라운드 컨텍스트 사용
	bgCtx := context.Background()

	// 제목 이벤트를 스트림에 추가
	if err := h.streamManager.AppendEvent(bgCtx, h.sessionID, h.assistantMessageID, interfaces.StreamEvent{
		ID:        evt.ID,
		Type:      types.ResponseTypeSessionTitle,
		Content:   data.Title,
		Done:      true,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"session_id": data.SessionID,
			"title":      data.Title,
		},
	}); err != nil {
		logger.GetLogger(h.ctx).Warn("Append session title event to stream failed (stream may have ended)", "error", err)
	}

	return nil
}

// handleComplete 에이전트 완료 이벤트 처리
func (h *AgentStreamHandler) handleComplete(ctx context.Context, evt event.Event) error {
	data, ok := evt.Data.(event.AgentCompleteData)
	if !ok {
		return nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// 최종 데이터로 어시스턴트 메시지 업데이트
	if data.MessageID == h.assistantMessageID {
		// h.assistantMessage.Content = data.FinalAnswer
		h.assistantMessage.IsCompleted = true

		// 제공된 경우 지식 참조 업데이트
		if len(data.KnowledgeRefs) > 0 {
			knowledgeRefs := make([]*types.SearchResult, 0, len(data.KnowledgeRefs))
			for _, ref := range data.KnowledgeRefs {
				if sr, ok := ref.(*types.SearchResult); ok {
					knowledgeRefs = append(knowledgeRefs, sr)
				}
			}
			h.assistantMessage.KnowledgeReferences = knowledgeRefs
		}

		// 제공된 경우 에이전트 단계 업데이트
		if data.AgentSteps != nil {
			if steps, ok := data.AgentSteps.([]types.AgentStep); ok {
				h.assistantMessage.AgentSteps = steps
			}
		}
	}

	// SSE가 완료를 감지할 수 있도록 완료 이벤트를 스트림 관리자에 전송
	if err := h.streamManager.AppendEvent(h.ctx, h.sessionID, h.assistantMessageID, interfaces.StreamEvent{
		ID:        evt.ID,
		Type:      types.ResponseTypeComplete,
		Content:   "",
		Done:      true,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"total_steps":       data.TotalSteps,
			"total_duration_ms": data.TotalDurationMs,
		},
	}); err != nil {
		logger.GetLogger(h.ctx).Errorf("Append complete event to stream failed: %v", err)
	}

	return nil
}
