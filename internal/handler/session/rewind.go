package session

import (
	"context"
	stderrors "errors"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// sessionRewinder is the rewind surface the handler needs. Declaring it here
// rather than depending on *service.SessionRewindService keeps the handler
// testable with a stub.
type sessionRewinder interface {
	Rewind(
		ctx context.Context,
		tenantID uint64,
		userID, sessionID, messageID string,
	) (*service.RewindResult, error)
}

// RewindSessionRequest is the rewind endpoint's body.
type RewindSessionRequest struct {
	// MessageID is the message to rewind to. A user message deletes itself and
	// everything after (the client prefills that question). An assistant
	// message keeps itself and deletes everything after.
	MessageID string `json:"message_id" binding:"required"`
}

// RewindSession godoc
// @Summary      回滚会话
// @Description  将当前会话回滚到指定的用户或助手消息：删除该点之后的消息，并在可到达时把沙箱工作区 git reset 到对应 checkpoint。用户消息：删除自身及之后并预填该问题；助手消息：保留该回答，删除其后的内容。
// @Tags         会话
// @Accept       json
// @Produce      json
// @Param        session_id  path      string                true  "会话 ID"
// @Param        request     body      RewindSessionRequest  true  "回滚请求"
// @Success      200         {object}  map[string]interface{}  "回滚结果"
// @Failure      400         {object}  errors.AppError         "请求参数错误 / 回滚点角色不支持"
// @Failure      404         {object}  errors.AppError         "会话或消息不存在"
// @Failure      409         {object}  errors.AppError         "会话正在生成中"
// @Failure      500         {object}  errors.AppError         "工作区回滚失败"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/{session_id}/rewind [post]
func (h *Handler) RewindSession(c *gin.Context) {
	ctx := c.Request.Context()

	sessionID := strings.TrimSpace(c.Param("session_id"))
	if sessionID == "" {
		sessionID = strings.TrimSpace(c.Param("id"))
	}
	if sessionID == "" {
		_ = c.Error(errors.NewBadRequestError("session ID is required"))
		return
	}

	var req RewindSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("message_id is required"))
		return
	}
	if strings.TrimSpace(req.MessageID) == "" {
		_ = c.Error(errors.NewBadRequestError("message_id is required"))
		return
	}

	if h.rewindService == nil {
		_ = c.Error(errors.NewBadRequestError("session rewind is not available"))
		return
	}

	tenantID, _ := types.TenantIDFromContext(ctx)
	userID := types.SessionOwnerIDFromContext(ctx)

	result, err := h.rewindService.Rewind(
		ctx, tenantID, userID, sessionID, strings.TrimSpace(req.MessageID),
	)
	if err != nil {
		if stderrors.Is(err, service.ErrRewindSourceBusy) {
			c.JSON(http.StatusConflict, gin.H{
				"success": false,
				"error":   "session has an active turn",
				"code":    "REWIND_SOURCE_BUSY",
			})
			return
		}
		if stderrors.Is(err, service.ErrRewindSessionNotFound) {
			_ = c.Error(errors.NewNotFoundError("session not found"))
			return
		}
		if stderrors.Is(err, service.ErrRewindMessageNotFound) {
			_ = c.Error(errors.NewNotFoundError("message not found"))
			return
		}
		if stderrors.Is(err, service.ErrRewindMessageRole) {
			_ = c.Error(errors.NewBadRequestError("rewind point must be a user or assistant message"))
			return
		}
		logger.Errorf(ctx, "rewind session %s failed: %v", sessionID, err)
		_ = c.Error(errors.NewInternalServerError("rewind session failed"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}
