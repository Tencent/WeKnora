package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/gin-gonic/gin"
)

type CodexOAuthStartRequest struct {
	AuthFile string `json:"auth_file"`
}

type CodexOAuthCompleteRequest struct {
	AuthFile    string `json:"auth_file"`
	CallbackURL string `json:"callback_url"`
	Code        string `json:"code"`
	State       string `json:"state"`
}

func (h *ModelHandler) StartCodexOAuth(c *gin.Context) {
	var req CodexOAuthStartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	result, err := chat.StartCodexOAuth(req.AuthFile)
	if err != nil {
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

func (h *ModelHandler) CompleteCodexOAuth(c *gin.Context) {
	var req CodexOAuthCompleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	status, err := chat.CompleteCodexOAuth(c.Request.Context(), chat.CodexOAuthCompleteRequest{
		AuthFile:    req.AuthFile,
		CallbackURL: req.CallbackURL,
		Code:        req.Code,
		State:       req.State,
	})
	if err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": status})
}

func (h *ModelHandler) CodexOAuthStatus(c *gin.Context) {
	status, err := chat.GetCodexOAuthStatus(c.Query("auth_file"))
	if err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": status})
}
