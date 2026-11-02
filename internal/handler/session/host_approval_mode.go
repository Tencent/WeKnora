package session

import (
	"context"
	stderrors "errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// HostApprovalModes reads and switches the Lite host approval mode of one
// session. Nil outside Lite, where the routes answer 404.
type HostApprovalModes interface {
	SessionApprovalMode(ctx context.Context, sessionID string) (mode, defaultMode string)
	SetSessionApprovalMode(ctx context.Context, sessionID, mode string) error
}

type hostApprovalModeBody struct {
	Mode string `json:"mode"`
}

// checkHostModeSession answers 404 outside Lite and for sessions the caller
// cannot read. Reading uses GetSession, which lets a tenant admin see a
// channel session. Writing uses GetOwnedSession, so that same admin cannot
// switch the session to auto.
func (h *Handler) checkHostModeSession(c *gin.Context) (string, bool) {
	return h.lookupHostModeSession(c, false)
}

func (h *Handler) checkOwnedHostModeSession(c *gin.Context) (string, bool) {
	return h.lookupHostModeSession(c, true)
}

func (h *Handler) lookupHostModeSession(c *gin.Context, write bool) (string, bool) {
	ctx := c.Request.Context()
	if h.hostModes == nil {
		_ = c.Error(errors.NewNotFoundError("host approval mode is only available in WeKnora Lite"))
		return "", false
	}
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		_ = c.Error(errors.NewBadRequestError(errors.ErrInvalidSessionID.Error()))
		return "", false
	}
	var err error
	if write {
		_, err = h.sessionService.GetOwnedSession(ctx, id)
	} else {
		_, err = h.sessionService.GetSession(ctx, id)
	}
	if err != nil {
		if stderrors.Is(err, errors.ErrSessionNotFound) {
			_ = c.Error(errors.NewNotFoundError(err.Error()))
			return "", false
		}
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return "", false
	}
	return id, true
}

// GetHostApprovalMode returns the session's host approval mode and the default.
func (h *Handler) GetHostApprovalMode(c *gin.Context) {
	id, ok := h.checkHostModeSession(c)
	if !ok {
		return
	}
	mode, def := h.hostModes.SessionApprovalMode(c.Request.Context(), id)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"mode": mode, "default": def}})
}

// SetHostApprovalMode switches the session's host approval mode.
func (h *Handler) SetHostApprovalMode(c *gin.Context) {
	id, ok := h.checkOwnedHostModeSession(c)
	if !ok {
		return
	}
	var body hostApprovalModeBody
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	switch body.Mode {
	case "ask", "auto":
	case "full":
		_ = c.Error(errors.NewBadRequestError("approval mode \"full\" is not available yet"))
		return
	default:
		_ = c.Error(errors.NewBadRequestError("mode must be ask or auto"))
		return
	}
	ctx := c.Request.Context()
	if err := h.hostModes.SetSessionApprovalMode(ctx, id, body.Mode); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	logger.Infof(ctx, "[LocalSandbox] approval mode session=%s mode=%s", id, body.Mode)
	mode, def := h.hostModes.SessionApprovalMode(ctx, id)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"mode": mode, "default": def}})
}
