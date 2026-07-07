package handler

import (
	stderrors "errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// MessageHandler handles HTTP requests related to messages within chat sessions
// It provides endpoints for loading and managing message history
type MessageHandler struct {
	MessageService interfaces.MessageService // Service that implements message business logic
}

// NewMessageHandler creates a new message handler instance with the required service
// Parameters:
//   - messageService: Service that implements message business logic
//
// Returns a pointer to a new MessageHandler
func NewMessageHandler(messageService interfaces.MessageService) *MessageHandler {
	return &MessageHandler{
		MessageService: messageService,
	}
}

// LoadMessages godoc
// @Summary      加载消息历史
// @Description  加载会话的消息历史，支持分页和时间筛选
// @Tags         消息
// @Accept       json
// @Produce      json
// @Param        session_id   path      string  true   "会话ID"
// @Param        limit        query     int     false  "返回数量"  default(20)
// @Param        before_time  query     string  false  "在此时间之前的消息（RFC3339Nano格式）"
// @Success      200          {object}  map[string]interface{}  "消息列表"
// @Failure      400          {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /messages/{session_id}/load [get]
func (h *MessageHandler) LoadMessages(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start loading messages")

	// Get path parameters and query parameters
	sessionID := secutils.SanitizeForLog(c.Param("session_id"))
	limit := secutils.SanitizeForLog(c.DefaultQuery("limit", "20"))
	beforeTimeStr := secutils.SanitizeForLog(c.DefaultQuery("before_time", ""))

	logger.Infof(ctx, "Loading messages params, session ID: %s, limit: %s, before time: %s",
		sessionID, limit, beforeTimeStr)

	// Parse limit parameter with fallback to default
	limitInt, err := strconv.Atoi(limit)
	if err != nil {
		logger.Warnf(ctx, "Invalid limit value, using default value 20, input: %s", limit)
		limitInt = 20
	}

	// If no beforeTime is provided, retrieve the most recent messages
	if beforeTimeStr == "" {
		logger.Infof(ctx, "Getting recent messages for session, session ID: %s, limit: %d", sessionID, limitInt)
		messages, err := h.MessageService.GetRecentMessagesBySession(ctx, sessionID, limitInt)
		if err != nil {
			if stderrors.Is(err, errors.ErrSessionNotFound) {
				// PR #1309 plumbed user-scope into the message service's
				// session existence check; non-owner / wrong-tenant lookups
				// surface as ErrSessionNotFound. Map to 404 so clients can
				// tell "wrong URL" from a real 5xx.
				logger.Warnf(ctx, "Session not found, ID: %s", sessionID)
				c.Error(errors.NewNotFoundError(err.Error()))
				return
			}
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError(err.Error()))
			return
		}

		logger.Infof(
			ctx,
			"Successfully retrieved recent messages, session ID: %s, message count: %d",
			sessionID, len(messages),
		)
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    messages,
		})
		return
	}

	// If beforeTime is provided, parse the timestamp (RFC3339Nano or RFC3339).
	beforeTime, err := parseMessageBeforeTime(beforeTimeStr)
	if err != nil {
		logger.Errorf(
			ctx,
			"Invalid time format, please use RFC3339/RFC3339Nano format, err: %v, beforeTimeStr: %s",
			err, beforeTimeStr,
		)
		c.Error(errors.NewBadRequestError("Invalid time format, please use RFC3339 or RFC3339Nano format"))
		return
	}

	// Retrieve messages before the specified timestamp
	logger.Infof(ctx, "Getting messages before specific time, session ID: %s, before time: %s, limit: %d",
		sessionID, beforeTime.Format(time.RFC3339Nano), limitInt)
	messages, err := h.MessageService.GetMessagesBySessionBeforeTime(ctx, sessionID, beforeTime, limitInt)
	if err != nil {
		if stderrors.Is(err, errors.ErrSessionNotFound) {
			// See note on the GetRecentMessagesBySession path above.
			logger.Warnf(ctx, "Session not found, ID: %s", sessionID)
			c.Error(errors.NewNotFoundError(err.Error()))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(
		ctx,
		"Successfully retrieved messages before time, session ID: %s, message count: %d",
		sessionID, len(messages),
	)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    messages,
	})
}

// DeleteMessage godoc
// @Summary      删除消息
// @Description  从会话中删除指定消息
// @Tags         消息
// @Accept       json
// @Produce      json
// @Param        session_id  path      string  true  "会话ID"
// @Param        id          path      string  true  "消息ID"
// @Success      200         {object}  map[string]interface{}  "删除成功"
// @Failure      500         {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /messages/{session_id}/{id} [delete]
func (h *MessageHandler) DeleteMessage(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start deleting message")

	// Get path parameters for session and message identification
	sessionID := secutils.SanitizeForLog(c.Param("session_id"))
	messageID := secutils.SanitizeForLog(c.Param("id"))

	logger.Infof(ctx, "Deleting message, session ID: %s, message ID: %s", sessionID, messageID)

	// Delete the message using the message service
	if err := h.MessageService.DeleteMessage(ctx, sessionID, messageID); err != nil {
		if stderrors.Is(err, errors.ErrSessionNotFound) {
			// See note on LoadMessages above — message-service operations
			// surface ErrSessionNotFound when the caller can't see the
			// owning session (post-#1309 user scope).
			logger.Warnf(ctx, "Session not found, ID: %s", sessionID)
			c.Error(errors.NewNotFoundError(err.Error()))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Message deleted successfully, session ID: %s, message ID: %s", sessionID, messageID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Message deleted successfully",
	})
}

// SearchMessages godoc
// @Summary      搜索历史对话
// @Description  通过关键词和/或向量相似度搜索历史对话记录，支持关键词、向量、混合三种模式
// @Tags         消息
// @Accept       json
// @Produce      json
// @Param        request  body      SearchMessagesRequest  true  "搜索请求"
// @Success      200      {object}  map[string]interface{}  "搜索结果"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /messages/search [post]
func (h *MessageHandler) SearchMessages(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start searching messages")

	var request SearchMessagesRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.Error(ctx, "Failed to parse search request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	if request.Query == "" {
		logger.Error(ctx, "Query content is empty")
		c.Error(errors.NewBadRequestError("Query content cannot be empty"))
		return
	}

	params := &types.MessageSearchParams{
		Query:      secutils.SanitizeForLog(request.Query),
		Mode:       types.MessageSearchMode(request.Mode),
		Limit:      request.Limit,
		SessionIDs: request.SessionIDs,
	}

	logger.Infof(ctx, "Searching messages with params: query=%s, mode=%s, limit=%d, session_ids=%v",
		params.Query, params.Mode, params.Limit, params.SessionIDs)

	result, err := h.MessageService.SearchMessages(ctx, params)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Message search completed, found %d results", result.Total)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

// SaveMessageFeedback godoc
// @Summary      Save answer feedback
// @Description  Persist like/dislike feedback for one assistant answer.
// @Tags         messages
// @Accept       json
// @Produce      json
// @Param        session_id  path      string  true  "Session ID"
// @Param        id          path      string  true  "Assistant message ID"
// @Param        request     body      SaveMessageFeedbackRequest  true  "Feedback"
// @Success      200         {object}  map[string]interface{}  "Saved feedback"
// @Failure      400         {object}  errors.AppError         "Invalid request"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /messages/{session_id}/{id}/feedback [post]
func (h *MessageHandler) SaveMessageFeedback(c *gin.Context) {
	ctx := c.Request.Context()
	sessionID := secutils.SanitizeForLog(c.Param("session_id"))
	messageID := secutils.SanitizeForLog(c.Param("id"))
	if sessionID == "" || messageID == "" {
		c.Error(errors.NewBadRequestError("session_id and message id are required"))
		return
	}

	var request SaveMessageFeedbackRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.Error(ctx, "Failed to parse message feedback request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	feedback, err := h.MessageService.SaveMessageFeedback(ctx, sessionID, messageID, request.FeedbackType, request.Reason)
	if err != nil {
		if stderrors.Is(err, errors.ErrSessionNotFound) {
			logger.Warnf(ctx, "Session not found, ID: %s", sessionID)
			c.Error(errors.NewNotFoundError(err.Error()))
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": sessionID,
			"message_id": messageID,
		})
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    feedback,
	})
}

// DeleteMessageFeedback godoc
// @Summary      Delete answer feedback
// @Description  Remove like/dislike feedback for one assistant answer.
// @Tags         messages
// @Produce      json
// @Param        session_id  path      string  true  "Session ID"
// @Param        id          path      string  true  "Assistant message ID"
// @Success      200         {object}  map[string]interface{}  "Deleted feedback"
// @Failure      400         {object}  errors.AppError         "Invalid request"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /messages/{session_id}/{id}/feedback [delete]
func (h *MessageHandler) DeleteMessageFeedback(c *gin.Context) {
	ctx := c.Request.Context()
	sessionID := secutils.SanitizeForLog(c.Param("session_id"))
	messageID := secutils.SanitizeForLog(c.Param("id"))
	if sessionID == "" || messageID == "" {
		c.Error(errors.NewBadRequestError("session_id and message id are required"))
		return
	}

	if err := h.MessageService.DeleteMessageFeedback(ctx, sessionID, messageID); err != nil {
		if stderrors.Is(err, errors.ErrSessionNotFound) {
			logger.Warnf(ctx, "Session not found, ID: %s", sessionID)
			c.Error(errors.NewNotFoundError(err.Error()))
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": sessionID,
			"message_id": messageID,
		})
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// SaveMessageKnowledgeChunks godoc
// @Summary      Save answer knowledge chunks
// @Description  Persist the normalized mapping between an assistant answer and referenced knowledge chunks.
// @Tags         messages
// @Accept       json
// @Produce      json
// @Param        session_id  path      string  true  "Session ID"
// @Param        id          path      string  true  "Assistant message ID"
// @Param        request     body      SaveMessageKnowledgeChunksRequest  true  "Referenced chunks"
// @Success      200         {object}  map[string]interface{}  "Saved chunks"
// @Failure      400         {object}  errors.AppError         "Invalid request"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /messages/{session_id}/{id}/knowledge-chunks [post]
func (h *MessageHandler) SaveMessageKnowledgeChunks(c *gin.Context) {
	ctx := c.Request.Context()
	sessionID := secutils.SanitizeForLog(c.Param("session_id"))
	messageID := secutils.SanitizeForLog(c.Param("id"))
	if sessionID == "" || messageID == "" {
		c.Error(errors.NewBadRequestError("session_id and message id are required"))
		return
	}

	var request SaveMessageKnowledgeChunksRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.Error(ctx, "Failed to parse message knowledge chunks request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	refs := request.References
	if len(refs) == 0 && len(request.ChunkIDs) > 0 {
		refs = make([]types.MessageKnowledgeChunkReference, 0, len(request.ChunkIDs))
		for _, chunkID := range request.ChunkIDs {
			chunkID = strings.TrimSpace(chunkID)
			if chunkID == "" {
				continue
			}
			refs = append(refs, types.MessageKnowledgeChunkReference{ChunkID: chunkID})
		}
	}

	chunks, err := h.MessageService.SaveMessageKnowledgeChunks(ctx, sessionID, messageID, refs)
	if err != nil {
		if stderrors.Is(err, errors.ErrSessionNotFound) {
			logger.Warnf(ctx, "Session not found, ID: %s", sessionID)
			c.Error(errors.NewNotFoundError(err.Error()))
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": sessionID,
			"message_id": messageID,
		})
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    chunks,
	})
}

// SaveMessageFeedbackRequest defines the request body for answer feedback persistence.
type SaveMessageFeedbackRequest struct {
	FeedbackType string `json:"feedback_type" binding:"required"`
	Reason       string `json:"reason"`
}

// SaveMessageKnowledgeChunksRequest defines the request body for answer-to-chunk persistence.
type SaveMessageKnowledgeChunksRequest struct {
	References []types.MessageKnowledgeChunkReference `json:"references"`
	ChunkIDs   []string                               `json:"chunk_ids"`
}

// SearchMessagesRequest defines the request structure for searching messages
type SearchMessagesRequest struct {
	// Query text for search
	Query string `json:"query" binding:"required"`
	// Search mode: "keyword", "vector", "hybrid" (default: "hybrid")
	Mode string `json:"mode"`
	// Maximum number of results to return (default: 20)
	Limit int `json:"limit"`
	// Filter by specific session IDs (optional)
	SessionIDs []string `json:"session_ids"`
}

// GetChatHistoryKBStats godoc
// @Summary      获取聊天历史知识库统计
// @Description  获取聊天历史知识库的统计信息（已索引消息数、知识库大小等）
// @Tags         消息
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "统计信息"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /messages/chat-history-stats [get]
func (h *MessageHandler) GetChatHistoryKBStats(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Getting chat history KB stats")

	stats, err := h.MessageService.GetChatHistoryKBStats(ctx)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    stats,
	})
}

// parseMessageBeforeTime parses the `before_time` query used by LoadMessages.
// Frontend cursors may be RFC3339 (no fractional seconds) or RFC3339Nano.
func parseMessageBeforeTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, stderrors.New("empty before_time")
	}
	layouts := []string{time.RFC3339Nano, time.RFC3339}
	var lastErr error
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, nil
		} else {
			lastErr = err
		}
	}
	return time.Time{}, lastErr
}
