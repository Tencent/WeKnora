package handler

import (
	stderrors "errors"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/userinput"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// UserInputHandler resolves structured questions for live Web Agent runs.
type UserInputHandler struct {
	resolver userinput.Resolver
	reader   userinput.PendingReader
}

func NewUserInputHandler(resolver userinput.Resolver) *UserInputHandler {
	reader, _ := resolver.(userinput.PendingReader)
	return &UserInputHandler{resolver: resolver, reader: reader}
}

// Resolve validates the caller and wakes the Agent waiting on pending_id.
func (h *UserInputHandler) Resolve(c *gin.Context) {
	ctx := c.Request.Context()
	pendingID := strings.TrimSpace(c.Param("pending_id"))
	if pendingID == "" {
		c.Error(apperrors.NewBadRequestError("pending_id is required"))
		return
	}
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.Error(apperrors.NewBadRequestError("Tenant ID cannot be empty"))
		return
	}
	principal, ok := types.PrincipalFromContext(ctx)
	if !ok || principal.StorageID() == "" {
		c.Error(apperrors.NewUnauthorizedError("authenticated user required to answer structured input"))
		return
	}
	if h == nil || h.resolver == nil {
		c.Error(apperrors.NewServiceUnavailableError("structured user input is unavailable"))
		return
	}
	var answer userinput.Answer
	if err := c.ShouldBindJSON(&answer); err != nil {
		c.Error(apperrors.NewBadRequestError(err.Error()))
		return
	}
	if err := h.resolver.Resolve(tenantID, principal.StorageID(), pendingID, answer); err != nil {
		handleUserInputResolveError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetPending restores the caller's live structured question after reconnect.
func (h *UserInputHandler) GetPending(c *gin.Context) {
	sessionID := strings.TrimSpace(c.Query("session_id"))
	if sessionID == "" {
		c.Error(apperrors.NewBadRequestError("session_id is required"))
		return
	}
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	principal, ok := types.PrincipalFromContext(c.Request.Context())
	if tenantID == 0 || !ok || principal.StorageID() == "" {
		c.Error(apperrors.NewUnauthorizedError("authenticated user required to read structured input"))
		return
	}
	if h == nil || h.reader == nil {
		c.Error(apperrors.NewServiceUnavailableError("structured user input restore is unavailable"))
		return
	}
	snapshot, err := h.reader.GetPending(
		c.Request.Context(), tenantID, principal.StorageID(), sessionID,
	)
	if err != nil {
		if stderrors.Is(err, userinput.ErrPendingNotFound) {
			c.Error(apperrors.NewNotFoundError("pending structured question not found"))
			return
		}
		c.Error(apperrors.NewInternalServerError("failed to read structured question"))
		return
	}
	c.JSON(http.StatusOK, snapshot)
}

func handleUserInputResolveError(c *gin.Context, err error) {
	switch {
	case stderrors.Is(err, userinput.ErrInvalidAnswer):
		c.Error(apperrors.NewBadRequestError("invalid structured answer"))
	case stderrors.Is(err, userinput.ErrTenantMismatch), stderrors.Is(err, userinput.ErrUserMismatch):
		c.Error(apperrors.NewForbiddenError("only the originating session owner may answer this question"))
	case stderrors.Is(err, userinput.ErrPendingNotFound):
		c.Error(apperrors.NewNotFoundError("pending structured question not found"))
	case stderrors.Is(err, userinput.ErrAlreadyResolved):
		c.Error(apperrors.NewConflictError("structured question is already resolved"))
	case stderrors.Is(err, userinput.ErrOwnerUnavailable):
		c.Error(apperrors.NewServiceUnavailableError("structured question owner is unavailable"))
	default:
		logger.ErrorWithFields(c.Request.Context(), err, map[string]interface{}{
			"pending_id": c.Param("pending_id"),
		})
		c.Error(apperrors.NewInternalServerError("failed to resolve structured question"))
	}
}
