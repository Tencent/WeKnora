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
// @Description  回滚当前会话到指定用户或助手消息：删除其后消息，可到达时 git reset 工作区。
// @Tags         会话
// @Accept       json
// @Produce      json
// @Param        session_id  path      string                true  "会话 ID"
// @Param        request     body      RewindSessionRequest  true  "回滚请求"
// @Success      200         {object}  map[string]interface{}  "回滚结果"
// @Failure      400         {object}  errors.AppError         "请求参数错误 / 回滚点角色不支持"
// @Failure      404         {object}  errors.AppError         "会话或消息不存在"
// @Failure      409         {object}  errors.AppError         "会话正在生成中 / 无检查点 / 沙箱已更换"
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
		_ = c.Error(errors.NewServiceUnavailableError("session rewind is not available"))
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
		if stderrors.Is(err, service.ErrRewindNoCheckpoint) {
			c.JSON(http.StatusConflict, gin.H{
				"success": false,
				"error":   "no reachable workspace checkpoint",
				"code":    "REWIND_NO_CHECKPOINT",
			})
			return
		}
		if stderrors.Is(err, service.ErrRewindSandboxReplaced) {
			c.JSON(http.StatusConflict, gin.H{
				"success": false,
				"error":   "sandbox was replaced",
				"code":    "REWIND_SANDBOX_REPLACED",
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
