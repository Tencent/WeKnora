package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// TenantInvitationHandler exposes the tenant-scoped CRUD on the
// `tenant_invitations` table plus the user-self-service inbox endpoints
// (/me/invitations*). Route-level RBAC: tenant routes are Owner-gated
// (POST/DELETE) or Viewer-gated (GET); inbox routes only require
// authentication (the service enforces "only invitee can act").
type TenantInvitationHandler struct {
	invitationService interfaces.TenantInvitationService
	userService       interfaces.UserService
	tenantService     interfaces.TenantService
	configInfo        *config.Config
}

// NewTenantInvitationHandler wires the dependencies. tenantService is
// used to hydrate tenant names in the inbox view so the invitee sees
// "join Foo Workspace" instead of a raw numeric tenant id. configInfo
// supplies FrontendBaseURL for share-link URL composition.
func NewTenantInvitationHandler(
	invitationService interfaces.TenantInvitationService,
	userService interfaces.UserService,
	tenantService interfaces.TenantService,
	configInfo *config.Config,
) *TenantInvitationHandler {
	return &TenantInvitationHandler{
		invitationService: invitationService,
		userService:       userService,
		tenantService:     tenantService,
		configInfo:        configInfo,
	}
}

// createInvitationRequest is the JSON body for POST /tenants/:id/invitations.
// Email is the user-facing identifier; the handler resolves it to a
// User row before delegating to the service. The optional Message is
// surfaced in the invitee's inbox.
type createInvitationRequest struct {
	Email   string           `json:"email" binding:"required,email"`
	Role    types.TenantRole `json:"role" binding:"required"`
	Message string           `json:"message"`
}

// parseInvitationIDFromPath reads :inv_id off the gin context.
func parseInvitationIDFromPath(c *gin.Context) (uint64, bool) {
	raw := strings.TrimSpace(c.Param("inv_id"))
	if raw == "" {
		c.Error(apperrors.NewValidationError("invitation id is required"))
		return 0, false
	}
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || v == 0 {
		c.Error(apperrors.NewValidationError("invitation id must be a positive integer"))
		return 0, false
	}
	return v, true
}

// projectInvitation hydrates a single TenantInvitation row into the
// API response shape. Inviter / invitee user fields and the tenant
// name are filled best-effort: a missing row degrades to the bare id
// rather than dropping the response. usersByID and tenantsByID let the
// caller batch the lookups when projecting a list.
//
// Share-link rows (InviteeUserID == "") get the IsShareLink flag set
// so the SPA can render them differently. invite_url is NOT populated
// here — projectInvitationWithLink layers it on for the management
// view that needs the copy-link affordance.
func projectInvitation(
	inv *types.TenantInvitation,
	usersByID map[string]*types.User,
	tenantsByID map[uint64]*types.Tenant,
) types.TenantInvitationResponse {
	resp := types.TenantInvitationResponse{
		ID:            inv.ID,
		TenantID:      inv.TenantID,
		InviteeUserID: inv.InviteeUserID,
		InvitedBy:     inv.InvitedBy,
		Role:          inv.Role,
		Status:        inv.Status,
		Message:       inv.Message,
		ExpiresAt:     inv.ExpiresAt,
		RespondedAt:   inv.RespondedAt,
		CreatedAt:     inv.CreatedAt,
		IsShareLink:   inv.InviteeUserID == "",
		AcceptedCount: inv.AcceptedCount,
	}
	if u, ok := usersByID[inv.InviteeUserID]; ok && u != nil {
		resp.InviteeEmail = u.Email
		resp.InviteeName = u.Username
	}
	if inv.InvitedBy != nil {
		if u, ok := usersByID[*inv.InvitedBy]; ok && u != nil {
			resp.InviterEmail = u.Email
			resp.InviterName = u.Username
		}
	}
	if t, ok := tenantsByID[inv.TenantID]; ok && t != nil {
		resp.TenantName = t.Name
	}
	return resp
}

// projectInvitationWithLink layers invite_url on top of projectInvitation
// for share-link rows that are still pending. The Owner-facing list and
// the create response go through here so a "copy link" button can sit
// on every active row — Owners can dispatch the link on demand without
// the "copy now or revoke" pressure.
//
// The /me/invitations inbox path does NOT use this helper: per-user
// invitees don't have a token to copy.
func (h *TenantInvitationHandler) projectInvitationWithLink(
	inv *types.TenantInvitation,
	usersByID map[string]*types.User,
	tenantsByID map[uint64]*types.Tenant,
) types.TenantInvitationResponse {
	resp := projectInvitation(inv, usersByID, tenantsByID)
	if inv.Status == types.TenantInvitationStatusPending && inv.Token != "" {
		resp.InviteURL = buildInviteRegisterURL(h.configInfo, inv.Token)
	}
	return resp
}

// hydrateUsers batches GetUsersByIDs over the (invitee, inviter) pairs.
// Best-effort: a transient lookup failure logs and returns an empty
// map so the projection falls back to ids.
func (h *TenantInvitationHandler) hydrateUsers(c *gin.Context, invs []*types.TenantInvitation) map[string]*types.User {
	if len(invs) == 0 {
		return map[string]*types.User{}
	}
	idSet := make(map[string]struct{}, len(invs)*2)
	for _, inv := range invs {
		idSet[inv.InviteeUserID] = struct{}{}
		if inv.InvitedBy != nil {
			idSet[*inv.InvitedBy] = struct{}{}
		}
	}
	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	users, err := h.userService.GetUsersByIDs(c.Request.Context(), ids)
	if err != nil {
		logger.Warnf(c.Request.Context(), "invitation list: batch user hydrate failed: %v", err)
		return map[string]*types.User{}
	}
	return users
}

// hydrateTenants is the same idea over the distinct tenant_ids touched
// by `invs`. Used by the /me inbox view where invitations span tenants.
func (h *TenantInvitationHandler) hydrateTenants(c *gin.Context, invs []*types.TenantInvitation) map[uint64]*types.Tenant {
	if len(invs) == 0 || h.tenantService == nil {
		return map[uint64]*types.Tenant{}
	}
	idSet := make(map[uint64]struct{}, len(invs))
	for _, inv := range invs {
		idSet[inv.TenantID] = struct{}{}
	}
	ids := make([]uint64, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	tenants, err := h.tenantService.GetTenantsByIDs(c.Request.Context(), ids)
	if err != nil {
		logger.Warnf(c.Request.Context(), "invitation list: batch tenant hydrate failed: %v", err)
		return map[uint64]*types.Tenant{}
	}
	return tenants
}

// ListTenantInvitations godoc
// @Summary      列出租户邀请
// @Description  按 tenant 列出待接受 / 历史邀请。query include_terminal=true 时附带 accepted/declined/revoked/expired。
// @Tags         租户邀请
// @Produce      json
// @Param        id                path   string  true   "租户 ID"
// @Param        include_terminal  query  bool    false  "是否包含终止态行（默认 false）"
// @Param        page              query  int     false  "页码（从 1 起）"  default(1)
// @Param        page_size         query  int     false  "每页数量"  default(20)
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /tenants/{id}/invitations [get]
func (h *TenantInvitationHandler) ListTenantInvitations(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := parseTenantIDFromPath(c)
	if !ok {
		return
	}
	includeTerminal := strings.EqualFold(c.Query("include_terminal"), "true")

	page, pageSize, ok := parseListPagination(c)
	if !ok {
		return
	}

	rows, total, err := h.invitationService.ListTenantInvitationsPage(ctx, tenantID, includeTerminal, page, pageSize)
	if err != nil {
		logger.Errorf(ctx, "ListTenantInvitationsPage failed: tenant=%d err=%v", tenantID, err)
		c.Error(apperrors.NewInternalServerError("failed to list invitations").WithDetails(err.Error()))
		return
	}

	usersByID := h.hydrateUsers(c, rows)
	showShareLinks := types.TenantRoleFromContext(ctx).HasPermission(types.TenantRoleOwner)
	resp := make([]types.TenantInvitationResponse, 0, len(rows))
	for _, inv := range rows {
		// Within the tenant view we don't bother hydrating tenant name
		// (the caller already knows the tenant). Pass an empty map.
		// Share-link URLs embed the registration token — only Owners may
		// re-copy them; other roles see metadata without invite_url.
		if showShareLinks {
			resp = append(resp, h.projectInvitationWithLink(inv, usersByID, nil))
		} else {
			resp = append(resp, projectInvitation(inv, usersByID, nil))
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"invitations": resp,
			"total":       total,
			"page":        page,
			"page_size":   pageSize,
		},
	})
}

// CreateInvitation godoc
// @Summary      发出租户邀请
// @Description  Owner 通过邮箱邀请已注册用户加入当前租户；被邀请人需要在 /me/invitations 接受后才会成为成员。
// @Tags         租户邀请
// @Accept       json
// @Produce      json
// @Param        id       path  string                   true  "租户 ID"
// @Param        request  body  createInvitationRequest  true  "邀请请求"
// @Success      201  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /tenants/{id}/invitations [post]
func (h *TenantInvitationHandler) CreateInvitation(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := parseTenantIDFromPath(c)
	if !ok {
		return
	}

	var req createInvitationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("invalid request body").WithDetails(err.Error()))
		return
	}
	if !req.Role.IsValid() {
		c.Error(apperrors.NewValidationError("role must be one of owner/admin/contributor/viewer"))
		return
	}

	user, err := h.userService.GetUserByEmail(ctx, strings.TrimSpace(req.Email))
	if err != nil {
		if errors.Is(err, apprepo.ErrUserNotFound) {
			c.Error(apperrors.NewNotFoundError(
				"user with this email is not registered; ask them to sign up first"))
			return
		}
		logger.Errorf(ctx, "GetUserByEmail failed: email=%s err=%v",
			secutils.SanitizeForLog(req.Email), err)
		c.Error(apperrors.NewInternalServerError("failed to look up user").WithDetails(err.Error()))
		return
	}

	caller, _ := types.UserIDFromContext(ctx)
	var invitedBy *string
	if caller != "" && !types.IsSyntheticUserID(caller) {
		invitedBy = &caller
	}

	inv, err := h.invitationService.Create(ctx, tenantID, user.ID, req.Role, invitedBy, req.Message)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidTenantRole):
			c.Error(apperrors.NewValidationError(err.Error()))
		case errors.Is(err, service.ErrPendingInvitationExists):
			c.Error(apperrors.NewConflictError(err.Error()))
		case errors.Is(err, service.ErrAlreadyMember):
			c.Error(apperrors.NewConflictError(err.Error()))
		default:
			logger.Errorf(ctx, "CreateInvitation failed: user=%s tenant=%d err=%v",
				user.ID, tenantID, err)
			c.Error(apperrors.NewInternalServerError("failed to create invitation").WithDetails(err.Error()))
		}
		return
	}

	usersByID := map[string]*types.User{user.ID: user}
	if invitedBy != nil {
		// Best-effort hydrate inviter too. Errors are swallowed —
		// the response degrades to just the invitee fields.
		if u, lookupErr := h.userService.GetUserByID(ctx, *invitedBy); lookupErr == nil && u != nil {
			usersByID[u.ID] = u
		}
	}
	resp := projectInvitation(inv, usersByID, nil)
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    resp,
	})
}

// RevokeInvitation godoc
// @Summary      撤销待接受邀请
// @Description  Owner 取消一条还在 pending 的邀请；已 accepted/declined/revoked/expired 的行不可再撤销。
// @Tags         租户邀请
// @Produce      json
// @Param        id      path  string  true  "租户 ID"
// @Param        inv_id  path  string  true  "邀请 ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /tenants/{id}/invitations/{inv_id} [delete]
func (h *TenantInvitationHandler) RevokeInvitation(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := parseTenantIDFromPath(c)
	if !ok {
		return
	}
	invID, ok := parseInvitationIDFromPath(c)
	if !ok {
		return
	}

	// Cross-check the invitation lives in the URL :id. The route layer
	// already verifies the caller has Owner on the active tenant AND
	// the URL :id matches the active tenant (PathTenantMatch), but the
	// invitation row itself carries its own tenant_id we have to honour
	// — otherwise an Owner of A could revoke invitations of B by URL-
	// crafting against /tenants/A/invitations/<inv-of-B>.
	inv, err := h.invitationService.GetByID(ctx, invID)
	if err != nil {
		logger.Errorf(ctx, "GetByID invitation failed: id=%d err=%v", invID, err)
		c.Error(apperrors.NewInternalServerError("failed to load invitation").WithDetails(err.Error()))
		return
	}
	if inv == nil {
		c.Error(apperrors.NewNotFoundError("invitation not found"))
		return
	}
	if inv.TenantID != tenantID {
		// Render the same 404 as "missing" so we don't leak existence
		// across tenants.
		c.Error(apperrors.NewNotFoundError("invitation not found"))
		return
	}

	if err := h.invitationService.Revoke(ctx, invID); err != nil {
		switch {
		case errors.Is(err, service.ErrInvitationNotFound):
			c.Error(apperrors.NewNotFoundError("invitation not found"))
		case errors.Is(err, service.ErrInvitationNotPending):
			c.Error(apperrors.NewConflictError(err.Error()))
		default:
			logger.Errorf(ctx, "RevokeInvitation failed: id=%d err=%v", invID, err)
			c.Error(apperrors.NewInternalServerError("failed to revoke invitation").WithDetails(err.Error()))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ListMyInvitations godoc
// @Summary      列出我的待接受邀请
// @Description  返回当前登录用户的待接受邀请（默认仅 pending），用于头像入口和 /invitations 收件箱页。
// @Tags         我的邀请
// @Produce      json
// @Param        include_terminal  query  bool  false  "是否包含已处理 / 已过期等终止态行（默认 false）"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /me/invitations [get]
func (h *TenantInvitationHandler) ListMyInvitations(c *gin.Context) {
	ctx := c.Request.Context()
	caller, ok := types.UserIDFromContext(ctx)
	if !ok || caller == "" {
		c.Error(apperrors.NewUnauthorizedError("caller user id missing from context"))
		return
	}
	includeTerminal := strings.EqualFold(c.Query("include_terminal"), "true")

	rows, err := h.invitationService.ListByInvitee(ctx, caller, includeTerminal)
	if err != nil {
		logger.Errorf(ctx, "ListByInvitee invitations failed: user=%s err=%v", caller, err)
		c.Error(apperrors.NewInternalServerError("failed to list invitations").WithDetails(err.Error()))
		return
	}

	usersByID := h.hydrateUsers(c, rows)
	tenantsByID := h.hydrateTenants(c, rows)
	resp := make([]types.TenantInvitationResponse, 0, len(rows))
	for _, inv := range rows {
		resp = append(resp, projectInvitation(inv, usersByID, tenantsByID))
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"invitations": resp,
			"total":       len(resp),
		},
	})
}

// CountMyPendingInvitations godoc
// @Summary      获取我的待处理邀请数
// @Description  轻量级 endpoint，返回当前登录用户的 pending 邀请数，用于头像旁的角标轮询。
// @Tags         我的邀请
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /me/invitations/pending-count [get]
func (h *TenantInvitationHandler) CountMyPendingInvitations(c *gin.Context) {
	ctx := c.Request.Context()
	caller, ok := types.UserIDFromContext(ctx)
	if !ok || caller == "" {
		c.Error(apperrors.NewUnauthorizedError("caller user id missing from context"))
		return
	}

	count, err := h.invitationService.CountPendingByInvitee(ctx, caller)
	if err != nil {
		logger.Errorf(ctx, "CountPendingByInvitee failed: user=%s err=%v", caller, err)
		c.Error(apperrors.NewInternalServerError("failed to count pending invitations").WithDetails(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"pending_count": count},
	})
}

// AcceptMyInvitation godoc
// @Summary      接受邀请
// @Description  当前登录用户接受一条 pending 邀请；服务端会同时写入 tenant_members 行。
// @Tags         我的邀请
// @Produce      json
// @Param        inv_id  path  string  true  "邀请 ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /me/invitations/{inv_id}/accept [post]
func (h *TenantInvitationHandler) AcceptMyInvitation(c *gin.Context) {
	ctx := c.Request.Context()
	caller, ok := types.UserIDFromContext(ctx)
	if !ok || caller == "" {
		c.Error(apperrors.NewUnauthorizedError("caller user id missing from context"))
		return
	}
	invID, ok := parseInvitationIDFromPath(c)
	if !ok {
		return
	}

	member, err := h.invitationService.Accept(ctx, invID, caller)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvitationNotFound):
			c.Error(apperrors.NewNotFoundError("invitation not found"))
		case errors.Is(err, service.ErrInvitationForbidden):
			c.Error(apperrors.NewForbiddenError(err.Error()))
		case errors.Is(err, service.ErrInvitationNotPending):
			c.Error(apperrors.NewConflictError(err.Error()))
		case errors.Is(err, service.ErrInvitationExpired):
			c.Error(apperrors.NewConflictError(err.Error()))
		default:
			logger.Errorf(ctx, "AcceptMyInvitation failed: id=%d user=%s err=%v",
				invID, caller, err)
			c.Error(apperrors.NewInternalServerError("failed to accept invitation").WithDetails(err.Error()))
		}
		return
	}

	// A tenantless account adopts the first accepted invitation as its
	// default tenant. Membership remains the authorization source; TenantID
	// only supplies the login/navigation default.
	if user, userErr := h.userService.GetUserByID(ctx, caller); userErr == nil && user != nil && user.TenantID == 0 {
		user.TenantID = member.TenantID
		if updateErr := h.userService.UpdateUser(ctx, user); updateErr != nil {
			logger.Errorf(ctx, "AcceptMyInvitation failed to set default tenant: user=%s tenant=%d err=%v",
				caller, member.TenantID, updateErr)
			c.Error(apperrors.NewInternalServerError("invitation accepted but default tenant update failed").WithDetails(updateErr.Error()))
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"membership": gin.H{
				"tenant_id": member.TenantID,
				"role":      member.Role,
				"status":    member.Status,
				"joined_at": member.JoinedAt,
			},
		},
	})
}

// DeclineMyInvitation godoc
// @Summary      拒绝邀请
// @Description  当前登录用户拒绝一条 pending 邀请；不创建 tenant_members 行。
// @Tags         我的邀请
// @Produce      json
// @Param        inv_id  path  string  true  "邀请 ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /me/invitations/{inv_id}/decline [post]
func (h *TenantInvitationHandler) DeclineMyInvitation(c *gin.Context) {
	ctx := c.Request.Context()
	caller, ok := types.UserIDFromContext(ctx)
	if !ok || caller == "" {
		c.Error(apperrors.NewUnauthorizedError("caller user id missing from context"))
		return
	}
	invID, ok := parseInvitationIDFromPath(c)
	if !ok {
		return
	}

	if err := h.invitationService.Decline(ctx, invID, caller); err != nil {
		switch {
		case errors.Is(err, service.ErrInvitationNotFound):
			c.Error(apperrors.NewNotFoundError("invitation not found"))
		case errors.Is(err, service.ErrInvitationForbidden):
			c.Error(apperrors.NewForbiddenError(err.Error()))
		case errors.Is(err, service.ErrInvitationNotPending):
			c.Error(apperrors.NewConflictError(err.Error()))
		case errors.Is(err, service.ErrInvitationExpired):
			c.Error(apperrors.NewConflictError(err.Error()))
		default:
			logger.Errorf(ctx, "DeclineMyInvitation failed: id=%d user=%s err=%v",
				invID, caller, err)
			c.Error(apperrors.NewInternalServerError("failed to decline invitation").WithDetails(err.Error()))
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
