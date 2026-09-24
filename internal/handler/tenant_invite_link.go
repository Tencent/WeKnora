package handler

import (
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// frontendBaseURLFor returns the externally-visible SPA origin used to
// compose absolute share-link URLs. Lookup order:
//  1. config.FrontendBaseURL (set in YAML or via ${FRONTEND_BASE_URL})
//  2. FRONTEND_BASE_URL env at request time (so an operator can roll
//     the value out without restarting the server)
//  3. empty — handler emits a host-relative URL ("/register?token=…")
//     and the SPA resolves it against the current origin.
//
// Trailing slashes are stripped so callers can append "/register?…"
// cleanly.
func frontendBaseURLFor(cfg *config.Config) string {
	candidate := ""
	if cfg != nil {
		candidate = strings.TrimSpace(cfg.FrontendBaseURL)
	}
	if candidate == "" {
		candidate = strings.TrimSpace(os.Getenv("FRONTEND_BASE_URL"))
	}
	return strings.TrimRight(candidate, "/")
}

// buildInviteRegisterURL composes the registration URL Admin+ hand
// to invitees. Plaintext token is URL-safe by construction (base64url)
// so no extra escaping is required.
func buildInviteRegisterURL(cfg *config.Config, plainToken string) string {
	if plainToken == "" {
		return ""
	}
	return frontendBaseURLFor(cfg) + "/register?token=" + plainToken
}

// createInviteLinkRequest is the body for POST /tenants/:id/invite-links.
// Only role + optional message — share-link rows have no specific
// invitee, so Admin+ just picks "what role does the holder get".
type createInviteLinkRequest struct {
	Role      types.TenantRole `json:"role"    binding:"required"`
	OrgUnitID string           `json:"org_unit_id"`
	Message   string           `json:"message"`
}

// CreateInviteLink godoc
// @Summary      生成共享邀请链接
// @Description  生成一条一次性共享邀请链接：有人通过链接注册并加入后自动失效。
// @Description  未使用前可持续复制；也可手动撤销或等待过期。
// @Tags         空间邀请
// @Accept       json
// @Produce      json
// @Param        id       path  string                   true  "空间 ID"
// @Param        request  body  createInviteLinkRequest  true  "共享链接配置"
// @Success      201  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /tenants/{id}/invite-links [post]
func (h *TenantInvitationHandler) CreateInviteLink(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := parseTenantIDFromPath(c)
	if !ok {
		return
	}
	var req createInviteLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("invalid request body").WithDetails(err.Error()))
		return
	}
	if !req.Role.IsValid() {
		c.Error(apperrors.NewValidationError("role must be one of owner/admin/contributor/viewer"))
		return
	}

	caller, _ := types.UserIDFromContext(ctx)
	var invitedBy *string
	if caller != "" && !types.IsSyntheticUserID(caller) {
		invitedBy = &caller
	}

	inv, _, err := h.invitationService.CreateShareLink(
		ctx, tenantID, req.Role, invitedBy, req.Message, req.OrgUnitID,
	)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidTenantRole):
			c.Error(apperrors.NewValidationError(err.Error()))
		case errors.Is(err, service.ErrOnlySystemAdminCanAssignOwner):
			c.Error(apperrors.NewForbiddenError(err.Error()))
		case errors.Is(err, service.ErrOrgUnitRequired),
			errors.Is(err, service.ErrInviterOrgUnitRequired),
			errors.Is(err, service.ErrOrgUnitNotInviteable):
			c.Error(apperrors.NewValidationError(err.Error()))
		default:
			if appErr, ok := apperrors.IsAppError(err); ok {
				c.Error(appErr)
				return
			}
			logger.Errorf(ctx, "CreateShareLink failed: tenant=%d err=%v", tenantID, err)
			c.Error(apperrors.NewInternalServerError("failed to create share link").WithDetails(err.Error()))
		}
		return
	}
	usersByID := map[string]*types.User{}
	if invitedBy != nil {
		if iu, lookupErr := h.userService.GetUserByID(ctx, *invitedBy); lookupErr == nil && iu != nil {
			usersByID[iu.ID] = iu
		}
	}
	orgUnitsByID := h.hydrateOrgUnits(c, []*types.TenantInvitation{inv})
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    h.projectInvitationWithLink(inv, usersByID, nil, orgUnitsByID),
	})
}
