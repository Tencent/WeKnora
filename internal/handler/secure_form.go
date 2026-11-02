package handler

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/application/service/credential"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
)

// maxSecureFormBody bounds a submission: 12 extra fields and two login parts
// of at most 4 KiB each, plus JSON overhead.
const maxSecureFormBody = 96 << 10

type secureFormSubmitter interface {
	Submit(ctx context.Context, owner credential.Owner, req credential.SubmitRequest) (credential.SubmitResult, error)
}

// WithSecureForms lets the handler accept secure credential form submissions.
func (h *MCPServiceHandler) WithSecureForms(s *credential.Submitter) *MCPServiceHandler {
	if s != nil {
		h.secureForms = s
	}
	return h
}

type secureFormSubmitBody struct {
	Action      string            `json:"action"`
	Choice      string            `json:"choice"`
	VaultItemID string            `json:"vault_item_id"`
	Values      map[string]string `json:"values"`
}

// SubmitSecureForm godoc
// @Summary      提交加密凭据表单
// @Description  提交 agent 发起的加密凭据表单（secure_form）。值只用于生成句柄或存入保险库，响应不回显任何值。
// @Tags         Agent
// @Accept       json
// @Produce      json
// @Param        prompt_id  path      string                  true  "交互 ID（事件中的 pending_id）"
// @Success      200        {object}  map[string]interface{}  "{success: true, resumed: bool}"
// @Failure      400        {object}  apperrors.AppError      "请求不合法或交互已结束"
// @Failure      409        {object}  map[string]interface{}  "凭据已保存，但交互已结束"
// @Security     Bearer
// @Router       /agent/secure-forms/{prompt_id} [post]
func (h *MCPServiceHandler) SubmitSecureForm(c *gin.Context) {
	if h.secureForms == nil {
		_ = c.Error(apperrors.NewServiceUnavailableError("the secure credential form is not enabled on this server"))
		return
	}
	ctx := c.Request.Context()
	owner, err := credential.OwnerFromContext(ctx)
	if err != nil {
		_ = c.Error(err)
		return
	}
	var body secureFormSubmitBody
	dec := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, maxSecureFormBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		_ = c.Error(apperrors.NewBadRequestError("invalid request body"))
		return
	}
	promptID := c.Param("prompt_id")
	res, err := h.secureForms.Submit(ctx, owner, credential.SubmitRequest{
		PromptID: promptID, Action: body.Action, Choice: body.Choice,
		VaultItemID: body.VaultItemID, Values: body.Values,
	})
	for k := range body.Values {
		body.Values[k] = ""
	}
	if err != nil {
		if res.SavedButClosed {
			c.JSON(http.StatusConflict, gin.H{
				"success": false, "saved": true,
				"error": gin.H{"message": "the credential was saved, but this request has already ended"},
			})
			return
		}
		var appErr *apperrors.AppError
		if stderrors.As(err, &appErr) {
			_ = c.Error(appErr)
			return
		}
		if stderrors.Is(err, approval.ErrPendingNotFound) || stderrors.Is(err, approval.ErrAlreadyResolved) ||
			stderrors.Is(err, approval.ErrTenantMismatch) || stderrors.Is(err, approval.ErrUserMismatch) ||
			stderrors.Is(err, approval.ErrInvalidDecision) {
			h.finishResolve(c, promptID, res.Resolution, err)
			return
		}
		_ = c.Error(apperrors.NewInternalServerError("could not record the submission"))
		return
	}
	h.finishResolve(c, promptID, res.Resolution, nil)
}
