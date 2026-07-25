package session

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// SubmitMessageFeedback godoc
// @Summary      提交问答回复反馈
// @Description  对 assistant 回复提交点赞或点踩反馈，并归因到该回复引用的知识库片段
// @Tags         会话
// @Accept       json
// @Produce      json
// @Param        session_id  path      string                       true  "会话ID"
// @Param        message_id  path      string                       true  "消息ID"
// @Param        request     body      types.MessageFeedbackRequest true  "反馈请求"
// @Success      200         {object}  map[string]interface{}       "反馈结果"
// @Failure      400         {object}  errors.AppError              "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/{session_id}/messages/{message_id}/feedback [post]
func (h *Handler) SubmitMessageFeedback(c *gin.Context) {
	ctx := c.Request.Context()
	sessionID := sessionIDParam(c)
	messageID := secutils.SanitizeForLog(c.Param("message_id"))

	var req types.MessageFeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	if !req.Action.Valid() {
		c.Error(errors.NewBadRequestError("action must be like or dislike"))
		return
	}

	feedback, err := h.messageFeedbackService.UpsertFeedback(ctx, sessionID, messageID, &req)
	if err != nil {
		c.Error(messageFeedbackError(err))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": types.MessageFeedbackResponse{Feedback: feedback},
	})
}

// CancelMessageFeedback godoc
// @Summary      取消问答回复反馈
// @Description  取消当前用户对 assistant 回复的点赞或点踩反馈，并回退关联知识库片段统计
// @Tags         会话
// @Produce      json
// @Param        session_id  path      string                 true  "会话ID"
// @Param        message_id  path      string                 true  "消息ID"
// @Success      200         {object}  map[string]interface{} "取消结果"
// @Failure      400         {object}  errors.AppError        "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/{session_id}/messages/{message_id}/feedback [delete]
func (h *Handler) CancelMessageFeedback(c *gin.Context) {
	ctx := c.Request.Context()
	sessionID := sessionIDParam(c)
	messageID := secutils.SanitizeForLog(c.Param("message_id"))

	if err := h.messageFeedbackService.CancelFeedback(ctx, sessionID, messageID); err != nil {
		c.Error(messageFeedbackError(err))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": types.MessageFeedbackResponse{},
	})
}

func messageFeedbackError(err error) *errors.AppError {
	switch err.Error() {
	case "invalid feedback action",
		"feedback can only be submitted for assistant messages",
		"feedback can only be submitted after the assistant message is completed":
		return errors.NewBadRequestError(err.Error())
	default:
		return errors.NewInternalServerError(err.Error())
	}
}
