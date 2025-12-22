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
// @Summary      继续流式响应
// @Description  继续获取正在进行的流式响应
// @Tags         问答
// @Accept       json
// @Produce      text/event-stream
// @Param        session_id  path      string  true  "会话ID"
// @Param        message_id  query     string  true  "消息ID"
// @Success      200         {object}  map[string]interface{}  "流式响应"
// @Failure      404         {object}  errors.AppError         "会话或消息不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/{session_id}/continue [get]
func (h *Handler) ContinueStream(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start continuing stream response processing")

	// Get session ID from URL parameter
	sessionID := secutils.SanitizeForLog(c.Param("session_id"))
	if sessionID == "" {
		logger.Error(ctx, "Session ID is empty")
		c.Error(errors.NewBadRequestError(errors.ErrInvalidSessionID.Error()))
		return
	}

	// Get message ID from query parameter
	messageID := secutils.SanitizeForLog(c.Query("message_id"))
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

	// Get initial events from stream (offset 0)
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

	// Set headers for SSE
	setSSEHeaders(c)

	// Check if stream is already completed
	streamCompleted := false
	for _, evt := range events {
		if evt.Type == "complete" {
			streamCompleted = true
			break
		}
	}

	// Replay existing events
	logger.Debugf(ctx, "Replaying %d existing events", len(events))
	for _, evt := range events {
		response := buildStreamResponse(evt, message.RequestID)
		c.SSEvent("message", response)
		c.Writer.Flush()
	}

	// If stream is already completed, send final event and return
	if streamCompleted {
		logger.Infof(ctx, "Stream already completed, session ID: %s, message ID: %s", sessionID, messageID)
		sendCompletionEvent(c, message.RequestID)
		return
	}

	// Continue polling for new events
	logger.Debug(ctx, "Starting event update monitoring")
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-c.Request.Context().Done():
			logger.Debug(ctx, "Client connection closed")
			return

		case <-ticker.C:
			// Get new events from current offset
			newEvents, newOffset, err := h.streamManager.GetEvents(ctx, sessionID, messageID, currentOffset)
			if err != nil {
				logger.Errorf(ctx, "Failed to get new events: %v", err)
				return
			}

			// Send new events
			streamCompletedNow := false
			for _, evt := range newEvents {
				// Check for completion event
				if evt.Type == "complete" {
					streamCompletedNow = true
				}

				response := buildStreamResponse(evt, message.RequestID)
				c.SSEvent("message", response)
				c.Writer.Flush()
			}

			// Update offset
			currentOffset = newOffset

			// If stream completed, send final event and exit
			if streamCompletedNow {
				logger.Infof(ctx, "Stream completed, session ID: %s, message ID: %s", sessionID, messageID)
				sendCompletionEvent(c, message.RequestID)
				return
			}
		}
	}
}

// StopSession godoc
// @Summary      停止生成
// @Description  停止当前正在进行的生成任务
// @Tags         问答
// @Accept       json
// @Produce      json
// @Param        session_id  path      string              true  "会话ID"
// @Param        request     body      StopSessionRequest  true  "停止请求"
// @Success      200         {object}  map[string]interface{}  "停止成功"
// @Failure      404         {object}  errors.AppError         "会话或消息不存在"
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

	// Parse request body to get message_id
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

	// Get tenant ID from context
	tenantID, exists := c.Get(types.TenantIDContextKey.String())
	if !exists {
		logger.Error(ctx, "Failed to get tenant ID")
		c.JSON(401, gin.H{"error": "Unauthorized"})
		return
	}
	tenantIDUint := tenantID.(uint64)

	// Verify message ownership and status
	message, err := h.messageService.GetMessage(ctx, sessionID, assistantMessageID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": sessionID,
			"message_id": assistantMessageID,
		})
		c.JSON(404, gin.H{"error": "Message not found"})
		return
	}

	// Verify message belongs to this session (double check)
	if message.SessionID != sessionID {
		logger.Warnf(ctx, "Message %s does not belong to session %s", assistantMessageID, sessionID)
		c.JSON(403, gin.H{"error": "Message does not belong to this session"})
		return
	}

	// Verify message belongs to the current tenant
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

	// Check if message is already completed (stopped)
	if message.IsCompleted {
		logger.Infof(ctx, "Message %s is already completed, no need to stop", assistantMessageID)
		c.JSON(200, gin.H{
			"success": true,
			"message": "Message already completed",
		})
		return
	}

	// Write stop event to StreamManager for distributed support
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

// handleAgentEventsForSSE handles agent events for SSE streaming using an existing handler
// The handler is already subscribed to events and AgentQA is already running
// This function polls StreamManager and pushes events to SSE, allowing graceful handling of disconnections
// waitForTitle: if true, wait for title event after completion (for new sessions without title)
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
			// Connection closed, exit gracefully without panic
			log.Infof(
				"Client disconnected, stopping SSE streaming for session=%s, message=%s",
				sessionID,
				assistantMessageID,
			)
			return

		case <-ticker.C:
			// Get new events from StreamManager using offset
			events, newOffset, err := h.streamManager.GetEvents(ctx, sessionID, assistantMessageID, lastOffset)
			if err != nil {
				log.Warnf("Failed to get events from stream: %v", err)
				continue
			}

			// Send any new events
			streamCompleted := false
			titleReceived := false
			for _, evt := range events {
				// Check for stop event
				if evt.Type == types.ResponseType(event.EventStop) {
					log.Infof("Detected stop event, triggering stop via EventBus for session=%s", sessionID)

					// Emit stop event to the EventBus to trigger context cancellation
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

					// Send stop notification to frontend
					c.SSEvent("message", &types.StreamResponse{
						ID:           requestID,
						ResponseType: "stop",
						Content:      "Generation stopped by user",
						Done:         true,
					})
					c.Writer.Flush()
					return
				}

				// Build StreamResponse from StreamEvent
				response := buildStreamResponse(evt, requestID)

				// Check for completion event
				if evt.Type == "complete" {
					streamCompleted = true
				}

				// Check for title event
				if evt.Type == types.ResponseTypeSessionTitle {
					titleReceived = true
				}

				// Check if connection is still alive before writing
				if c.Request.Context().Err() != nil {
					log.Info("Connection closed during event sending, stopping")
					return
				}

				c.SSEvent("message", response)
				c.Writer.Flush()
			}

			// Update offset
			lastOffset = newOffset

			// Check if stream is completed - wait for title event only if needed and not already received
			if streamCompleted {
				if waitForTitle && !titleReceived {
					log.Infof("Stream completed for session=%s, message=%s, waiting for title event", sessionID, assistantMessageID)
					// Wait up to 3 seconds for title event after completion
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
							// Check for new events (title event)
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
									// If we got the title, we can exit
									if evt.Type == types.ResponseTypeSessionTitle {
										log.Infof("Title event received: %s", evt.Content)
										break titleWaitLoop
									}
								}
								lastOffset = newOff
							} else {
								// No events, wait a bit before checking again
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
