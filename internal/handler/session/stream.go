package session

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// ContinueStream godoc
// @Summary      스트림 응답 계속
// @Description  진행 중인 스트림 응답 계속 받기
// @Tags         질의응답
// @Accept       json
// @Produce      text/event-stream
// @Param        session_id  path      string  true  "세션 ID"
// @Param        message_id  query     string  true  "메시지 ID"
// @Success      200         {object}  map[string]interface{}  "스트림 응답"
// @Failure      404         {object}  errors.AppError         "세션 또는 메시지가 없음"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/{session_id}/continue [get]
func (h *Handler) ContinueStream(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start continuing stream response processing")

	// URL 매개변수에서 세션 ID 가져오기
	sessionID := secutils.SanitizeForLog(c.Param("session_id"))
	if sessionID == "" {
		logger.Error(ctx, "Session ID is empty")
		c.Error(errors.NewBadRequestError(errors.ErrInvalidSessionID.Error()))
		return
	}

	// 쿼리 매개변수에서 메시지 ID 가져오기
	messageID := secutils.SanitizeForLog(c.Query("message_id"))
	if messageID == "" {
		logger.Error(ctx, "Message ID is empty")
		c.Error(errors.NewBadRequestError("Missing message ID"))
		return
	}

	logger.Infof(ctx, "Continuing stream, session ID: %s, message ID: %s", sessionID, messageID)

	// 세션이 존재하고 이 테넌트에 속하는지 확인
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

	// 완료되지 않은 메시지 가져오기
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

	// 스트림에서 초기 이벤트 가져오기 (오프셋 0)
	events, currentOffset, err := h.streamManager.GetEvents(ctx, sessionID, messageID, 0)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(fmt.Sprintf("Failed to get stream data: %s", err.Error())))
		return
	}

	if len(events) == 0 {
		logger.Warnf(ctx, "No events found in stream, session ID: %s, message ID: %s", sessionID, messageID)
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "No stream events found",
		})
		return
	}

	logger.Infof(
		ctx, "Preparing to replay %d events and continue streaming, session ID: %s, message ID: %s",
		len(events), sessionID, messageID,
	)

	// SSE 헤더 설정
	setSSEHeaders(c)

	// 스트림이 이미 완료되었는지 확인
	streamCompleted := false
	for _, evt := range events {
		if evt.Type == "complete" {
			streamCompleted = true
			break
		}
	}

	// 기존 이벤트 재생
	logger.Debugf(ctx, "Replaying %d existing events", len(events))
	for _, evt := range events {
		response := buildStreamResponse(evt, message.RequestID)
		c.SSEvent("message", response)
		c.Writer.Flush()
	}

	// 스트림이 이미 완료된 경우, 최종 이벤트 전송 후 반환
	if streamCompleted {
		logger.Infof(ctx, "Stream already completed, session ID: %s, message ID: %s", sessionID, messageID)
		sendCompletionEvent(c, message.RequestID)
		return
	}

	// 새로운 이벤트 폴링 계속
	logger.Debug(ctx, "Starting event update monitoring")
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-c.Request.Context().Done():
			logger.Debug(ctx, "Client connection closed")
			return

		case <-ticker.C:
			// 현재 오프셋에서 새 이벤트 가져오기
			newEvents, newOffset, err := h.streamManager.GetEvents(ctx, sessionID, messageID, currentOffset)
			if err != nil {
				logger.Errorf(ctx, "Failed to get new events: %v", err)
				return
			}

			// 새 이벤트 전송
			streamCompletedNow := false
			for _, evt := range newEvents {
				// 완료 이벤트 확인
				if evt.Type == "complete" {
					streamCompletedNow = true
				}

				response := buildStreamResponse(evt, message.RequestID)
				c.SSEvent("message", response)
				c.Writer.Flush()
			}

			// 오프셋 업데이트
			currentOffset = newOffset

			// 스트림 완료 시, 최종 이벤트 전송 후 종료
			if streamCompletedNow {
				logger.Infof(ctx, "Stream completed, session ID: %s, message ID: %s", sessionID, messageID)
				sendCompletionEvent(c, message.RequestID)
				return
			}
		}
	}
}

// StopSession godoc
// @Summary      생성 중지
// @Description  현재 진행 중인 생성 작업 중지
// @Tags         질의응답
// @Accept       json
// @Produce      json
// @Param        session_id  path      string              true  "세션 ID"
// @Param        request     body      StopSessionRequest  true  "중지 요청"
// @Success      200         {object}  map[string]interface{}  "중지 성공"
// @Failure      404         {object}  errors.AppError         "세션 또는 메시지가 없음"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/{session_id}/stop [post]
func (h *Handler) StopSession(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())
	sessionID := secutils.SanitizeForLog(c.Param("session_id"))

	if sessionID == "" {
		c.JSON(400, gin.H{"error": "Session ID is required"})
		return
	}

	// 요청 본문 파싱하여 message_id 가져오기
	var req StopSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": sessionID,
		})
		c.JSON(400, gin.H{"error": "message_id is required"})
		return
	}

	assistantMessageID := secutils.SanitizeForLog(req.MessageID)
	logger.Infof(ctx, "Stop generation request for session: %s, message: %s", sessionID, assistantMessageID)

	// 컨텍스트에서 테넌트 ID 가져오기
	tenantID, exists := c.Get(types.TenantIDContextKey.String())
	if !exists {
		logger.Error(ctx, "Failed to get tenant ID")
		c.JSON(401, gin.H{"error": "Unauthorized"})
		return
	}
	tenantIDUint := tenantID.(uint64)

	// 메시지 소유권 및 상태 확인
	message, err := h.messageService.GetMessage(ctx, sessionID, assistantMessageID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": sessionID,
			"message_id": assistantMessageID,
		})
		c.JSON(404, gin.H{"error": "Message not found"})
		return
	}

	// 메시지가 이 세션에 속하는지 확인 (이중 확인)
	if message.SessionID != sessionID {
		logger.Warnf(ctx, "Message %s does not belong to session %s", assistantMessageID, sessionID)
		c.JSON(403, gin.H{"error": "Message does not belong to this session"})
		return
	}

	// 메시지가 현재 테넌트에 속하는지 확인
	session, err := h.sessionService.GetSession(ctx, sessionID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": sessionID,
		})
		c.JSON(404, gin.H{"error": "Session not found"})
		return
	}

	if session.TenantID != tenantIDUint {
		logger.Warnf(ctx, "Session %s does not belong to tenant %d", sessionID, tenantIDUint)
		c.JSON(403, gin.H{"error": "Access denied"})
		return
	}

	// 메시지가 이미 완료(중지)되었는지 확인
	if message.IsCompleted {
		logger.Infof(ctx, "Message %s is already completed, no need to stop", assistantMessageID)
		c.JSON(200, gin.H{
			"success": true,
			"message": "Message already completed",
		})
		return
	}

	// 분산 지원을 위해 StreamManager에 중지 이벤트 기록
	stopEvent := interfaces.StreamEvent{
		ID:        fmt.Sprintf("stop-%d", time.Now().UnixNano()),
		Type:      types.ResponseType(event.EventStop),
		Content:   "",
		Done:      true,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"session_id": sessionID,
			"message_id": assistantMessageID,
			"reason":     "user_requested",
		},
	}

	if err := h.streamManager.AppendEvent(ctx, sessionID, assistantMessageID, stopEvent); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": sessionID,
			"message_id": assistantMessageID,
		})
		c.JSON(500, gin.H{"error": "Failed to write stop event"})
		return
	}

	logger.Infof(ctx, "Stop event written successfully for session: %s, message: %s", sessionID, assistantMessageID)
	c.JSON(200, gin.H{
		"success": true,
		"message": "Generation stopped",
	})
}

// handleAgentEventsForSSE 기존 핸들러를 사용하여 SSE 스트리밍을 위한 에이전트 이벤트 처리
// 핸들러는 이미 이벤트를 구독하고 있으며 AgentQA는 이미 실행 중입니다.
// 이 함수는 StreamManager를 폴링하고 이벤트를 SSE로 푸시하여 연결 끊김을 정상적으로 처리합니다.
// waitForTitle: true인 경우 완료 후 제목 이벤트를 기다림 (제목이 없는 새 세션의 경우)
func (h *Handler) handleAgentEventsForSSE(
	ctx context.Context,
	c *gin.Context,
	sessionID, assistantMessageID, requestID string,
	eventBus *event.EventBus,
	waitForTitle bool,
) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	lastOffset := 0
	log := logger.GetLogger(ctx)

	log.Infof("Starting pull-based SSE streaming for session=%s, message=%s", sessionID, assistantMessageID)

	for {
		select {
		case <-c.Request.Context().Done():
			// 연결 종료, 패닉 없이 정상 종료
			log.Infof(
				"Client disconnected, stopping SSE streaming for session=%s, message=%s",
				sessionID,
				assistantMessageID,
			)
			return

		case <-ticker.C:
			// 오프셋을 사용하여 StreamManager에서 새 이벤트 가져오기
			events, newOffset, err := h.streamManager.GetEvents(ctx, sessionID, assistantMessageID, lastOffset)
			if err != nil {
				log.Warnf("Failed to get events from stream: %v", err)
				continue
			}

			// 새 이벤트 전송
			streamCompleted := false
			titleReceived := false
			for _, evt := range events {
				// 중지 이벤트 확인
				if evt.Type == types.ResponseType(event.EventStop) {
					log.Infof("Detected stop event, triggering stop via EventBus for session=%s", sessionID)

					// 컨텍스트 취소를 트리거하기 위해 EventBus에 중지 이벤트 방출
					if eventBus != nil {
						eventBus.Emit(ctx, event.Event{
							Type:      event.EventStop,
							SessionID: sessionID,
							Data: event.StopData{
								SessionID: sessionID,
								MessageID: assistantMessageID,
								Reason:    "user_requested",
							},
						})
					}

					// 프론트엔드에 중지 알림 전송
					c.SSEvent("message", &types.StreamResponse{
						ID:           requestID,
						ResponseType: "stop",
						Content:      "Generation stopped by user",
						Done:         true,
					})
					c.Writer.Flush()
					return
				}

				// StreamEvent에서 StreamResponse 생성
				response := buildStreamResponse(evt, requestID)

				// 완료 이벤트 확인
				if evt.Type == "complete" {
					streamCompleted = true
				}

				// 제목 이벤트 확인
				if evt.Type == types.ResponseTypeSessionTitle {
					titleReceived = true
				}

				// 쓰기 전에 연결이 여전히 살아있는지 확인
				if c.Request.Context().Err() != nil {
					log.Info("Connection closed during event sending, stopping")
					return
				}

				c.SSEvent("message", response)
				c.Writer.Flush()
			}

			// 오프셋 업데이트
			lastOffset = newOffset

			// 스트림이 완료되었는지 확인 - 필요한 경우 제목 이벤트를 기다리고 이미 수신되지 않은 경우에만
			if streamCompleted {
				if waitForTitle && !titleReceived {
					log.Infof("Stream completed for session=%s, message=%s, waiting for title event", sessionID, assistantMessageID)
					// 완료 후 제목 이벤트를 위해 최대 3초 대기
					titleTimeout := time.After(3 * time.Second)
				titleWaitLoop:
					for {
						select {
						case <-titleTimeout:
							log.Info("Title wait timeout, closing stream")
							break titleWaitLoop
						case <-c.Request.Context().Done():
							log.Info("Connection closed while waiting for title")
							return
						default:
							// 새 이벤트 확인 (제목 이벤트)
							events, newOff, err := h.streamManager.GetEvents(c.Request.Context(), sessionID, assistantMessageID, lastOffset)
							if err != nil {
								log.Warnf("Error getting events while waiting for title: %v", err)
								break titleWaitLoop
							}
							if len(events) > 0 {
								for _, evt := range events {
									response := buildStreamResponse(evt, requestID)
									c.SSEvent("message", response)
									c.Writer.Flush()
									// 제목을 받으면 종료 가능
									if evt.Type == types.ResponseTypeSessionTitle {
										log.Infof("Title event received: %s", evt.Content)
										break titleWaitLoop
									}
								}
								lastOffset = newOff
							} else {
								// 이벤트 없음, 다시 확인하기 전에 잠시 대기
								time.Sleep(100 * time.Millisecond)
							}
						}
					}
				} else {
					log.Infof("Stream completed for session=%s, message=%s", sessionID, assistantMessageID)
				}
				sendCompletionEvent(c, requestID)
				return
			}
		}
	}
}
