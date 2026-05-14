package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// TenantMemberHandler exposes /tenants/:id/members CRUD. The route layer
// enforces RBAC (Viewer for list, Owner for any mutation) — see
// router.RegisterTenantRoutes — so we don't re-check role here.
//
// Tenant scoping: the auth middleware resolves the caller's role against
// the *active* tenant (JWT / X-Tenant-ID switch / API-key). The URL :id
// is independent and MUST be cross-checked: a user who is Owner of
// tenant A could otherwise POST /tenants/B/members and have the role
// gate happily accept their tenant-A role for an operation that targets
// tenant B. That cross-check now lives in
// middleware.RequirePathTenantMatch (mounted at the /tenants/:id route
// group); by the time a request reaches one of the methods below, :id
// is guaranteed to either match the active tenant or carry a
// cross-tenant superuser bypass.
type TenantMemberHandler struct {
	memberService interfaces.TenantMemberService
	userService   interfaces.UserService
}

// NewTenantMemberHandler wires the dependencies. PR 1 already provides
// both services through the dig container; we just consume them. The
// previous *config.Config argument was removed once
// middleware.RequirePathTenantMatch took over the cross-tenant
// superuser carve-out.
func NewTenantMemberHandler(
	memberService interfaces.TenantMemberService,
	userService interfaces.UserService,
) *TenantMemberHandler {
	return &TenantMemberHandler{
		memberService: memberService,
		userService:   userService,
	}
}

// addMemberRequest is the JSON body for POST /tenants/:id/members.
// Email is the user-facing invite identifier; the handler resolves it to a
// User via UserService.GetUserByEmail. PR 3 does not implement
// email-based invitations for users that don't exist yet — the invitee
// must already have an account. Sending an email invite is tracked as a
// PR 4 candidate.
type addMemberRequest struct {
	Email string           `json:"email" binding:"required,email"`
	Role  types.TenantRole `json:"role" binding:"required"`
}

// updateMemberRoleRequest is the JSON body for PUT /tenants/:id/members/:user_id.
type updateMemberRoleRequest struct {
	Role types.TenantRole `json:"role" binding:"required"`
}

// parseTenantIDFromPath reads :id from the gin route and validates it as
// a tenant ID. Returning (0, false) means we already wrote the error to
// the gin context and the caller should `return` immediately.
func parseTenantIDFromPath(c *gin.Context) (uint64, bool) {
	raw := strings.TrimSpace(c.Param("id"))
	if raw == "" {
		c.Error(apperrors.NewValidationError("tenant id is required"))
		return 0, false
	}
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || v == 0 {
		c.Error(apperrors.NewValidationError("tenant id must be a positive integer"))
		return 0, false
	}
	return v, true
}

// resolveTenantIDFromPath parses :id from the URL. The cross-check
// "URL :id must match the active tenant context (with a cross-tenant
// superuser carve-out)" used to live here but moved to
// middleware.RequirePathTenantMatch, mounted on the /tenants/:id
// route group. By the time a handler method calls this, the URL has
// already been authorised against the caller's tenant context.
//
// Returns the parsed tenant ID and true; returns (0, false) after
// having written the appropriate error to the gin context, in which
// case the handler must `return` immediately.
func (h *TenantMemberHandler) resolveTenantIDFromPath(c *gin.Context) (uint64, bool) {
	return parseTenantIDFromPath(c)
}

// ListMembers godoc
// @Summary      列出租户成员
// @Description  返回当前租户内全部 active 成员（含每位成员的角色、邮箱、头像）
// @Tags         租户成员
// @Produce      json
// @Param        id  path  string  true  "租户 ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /tenants/{id}/members [get]
func (h *TenantMemberHandler) ListMembers(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := h.resolveTenantIDFromPath(c)
	if !ok {
		return
	}

	members, err := h.memberService.ListByTenant(ctx, tenantID)
	if err != nil {
		logger.Errorf(ctx, "ListByTenant failed: tenant=%d err=%v", tenantID, err)
		c.Error(apperrors.NewInternalServerError("failed to list members").WithDetails(err.Error()))
		return
	}

	// Hydrate user-facing fields in one batched query. Before this we
	// did N+1 GetUserByID calls; tenants with hundreds of members
	// pressed the user repo hard for no good reason. Failure is
	// best-effort — a transient batch error degrades to "no email /
	// username on this page" rather than dropping rows, so dangling
	// memberships can still be cleaned up by the Owner.
	ids := make([]string, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.UserID)
	}
	usersByID := map[string]*types.User{}
	if u, err := h.userService.GetUsersByIDs(ctx, ids); err == nil {
		usersByID = u
	} else {
		logger.Warnf(ctx, "ListMembers batch user lookup failed: tenant=%d err=%v", tenantID, err)
	}

	resp := make([]types.TenantMemberResponse, 0, len(members))
	for _, m := range members {
		row := types.TenantMemberResponse{
			UserID:    m.UserID,
			Role:      m.Role,
			Status:    m.Status,
			InvitedBy: m.InvitedBy,
			JoinedAt:  m.JoinedAt,
		}
		if u, ok := usersByID[m.UserID]; ok && u != nil {
			row.Email = u.Email
			row.Username = u.Username
			row.Avatar = u.Avatar
		}
		resp = append(resp, row)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"members": resp,
			"total":   len(resp),
		},
	})
}

// AddMember godoc
// @Summary      添加租户成员
// @Description  Owner 通过 email 邀请已经存在的用户加入当前租户
// @Tags         租户成员
// @Accept       json
// @Produce      json
// @Param        id        path  string                 true  "租户 ID"
// @Param        request   body  addMemberRequest       true  "邀请请求"
// @Success      201  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /tenants/{id}/members [post]
func (h *TenantMemberHandler) AddMember(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := h.resolveTenantIDFromPath(c)
	if !ok {
		return
	}

	var req addMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("invalid request body").WithDetails(err.Error()))
		return
	}
	// Defence in depth — service also re-validates, but rejecting early
	// gives the client a better error message than the generic service
	// sentinel-mapped 400.
	if !req.Role.IsValid() {
		c.Error(apperrors.NewValidationError("role must be one of owner/admin/contributor/viewer"))
		return
	}

	user, err := h.userService.GetUserByEmail(ctx, strings.TrimSpace(req.Email))
	if err != nil {
		// ErrUserNotFound is the deliberate "not registered yet" signal;
		// mapping it to 404 lets the UI render "ask them to sign up first"
		// instead of a generic failure.
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

	// Attribute the invite to a human caller only. The X-API-Key auth
	// path attaches a synthetic "system-<tenantID>" user (see
	// types.IsSyntheticUserID); recording that as invited_by would
	// permanently break join-with-users views and any future "who
	// invited whom" UX. Leaving invited_by NULL is the correct fallback
	// — matches the same treatment KB.CreatorID gets in PR 2.
	caller, _ := types.UserIDFromContext(ctx)
	var invitedBy *string
	if caller != "" && !types.IsSyntheticUserID(caller) {
		invitedBy = &caller
	}

	member, err := h.memberService.AddMember(ctx, user.ID, tenantID, req.Role, invitedBy)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidTenantRole):
			c.Error(apperrors.NewValidationError(err.Error()))
		case errors.Is(err, service.ErrMembershipAlreadyExists):
			// 409 reads better than 400 here: the request was syntactically
			// fine, the conflict is semantic ("already a member").
			c.Error(apperrors.NewConflictError(err.Error()))
		default:
			logger.Errorf(ctx, "AddMember failed: user=%s tenant=%d err=%v",
				user.ID, tenantID, err)
			c.Error(apperrors.NewInternalServerError("failed to add member").WithDetails(err.Error()))
		}
		return
	}

	// Project the freshly added row through the same response shape the
	// list endpoint uses, so the UI can swap "Add Member" UX into the
	// table without an extra round-trip.
	resp := types.TenantMemberResponse{
		UserID:    member.UserID,
		Email:     user.Email,
		Username:  user.Username,
		Avatar:    user.Avatar,
		Role:      member.Role,
		Status:    member.Status,
		InvitedBy: member.InvitedBy,
		JoinedAt:  member.JoinedAt,
	}
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    resp,
	})
}

// UpdateMemberRole godoc
// @Summary      修改租户成员角色
// @Description  Owner 修改某位成员在当前租户内的角色；不能将最后一位 Owner 降级
// @Tags         租户成员
// @Accept       json
// @Produce      json
// @Param        id       path  string                  true  "租户 ID"
// @Param        user_id  path  string                  true  "用户 ID"
// @Param        request  body  updateMemberRoleRequest true  "目标角色"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /tenants/{id}/members/{user_id} [put]
func (h *TenantMemberHandler) UpdateMemberRole(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := h.resolveTenantIDFromPath(c)
	if !ok {
		return
	}
	userID := strings.TrimSpace(c.Param("user_id"))
	if userID == "" {
		c.Error(apperrors.NewValidationError("user_id is required"))
		return
	}

	var req updateMemberRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("invalid request body").WithDetails(err.Error()))
		return
	}
	if !req.Role.IsValid() {
		c.Error(apperrors.NewValidationError("role must be one of owner/admin/contributor/viewer"))
		return
	}

	if err := h.memberService.UpdateRole(ctx, userID, tenantID, req.Role); err != nil {
		switch {
		case errors.Is(err, service.ErrMembershipNotFound):
			c.Error(apperrors.NewNotFoundError("membership not found"))
		case errors.Is(err, service.ErrLastOwner):
			c.Error(apperrors.NewConflictError(err.Error()))
		case errors.Is(err, service.ErrInvalidTenantRole):
			c.Error(apperrors.NewValidationError(err.Error()))
		default:
			logger.Errorf(ctx, "UpdateRole failed: user=%s tenant=%d err=%v",
				userID, tenantID, err)
			c.Error(apperrors.NewInternalServerError("failed to update member role").WithDetails(err.Error()))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// RemoveMember godoc
// @Summary      移除租户成员
// @Description  Owner 将某位成员从当前租户中移除（软删除 tenant_members 行）；不能移除最后一位 Owner
// @Tags         租户成员
// @Produce      json
// @Param        id       path  string  true  "租户 ID"
// @Param        user_id  path  string  true  "用户 ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /tenants/{id}/members/{user_id} [delete]
func (h *TenantMemberHandler) RemoveMember(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := h.resolveTenantIDFromPath(c)
	if !ok {
		return
	}
	userID := strings.TrimSpace(c.Param("user_id"))
	if userID == "" {
		c.Error(apperrors.NewValidationError("user_id is required"))
		return
	}

	if err := h.memberService.RemoveMember(ctx, userID, tenantID); err != nil {
		switch {
		case errors.Is(err, service.ErrMembershipNotFound):
			c.Error(apperrors.NewNotFoundError("membership not found"))
		case errors.Is(err, service.ErrLastOwner):
			c.Error(apperrors.NewConflictError(err.Error()))
		default:
			logger.Errorf(ctx, "RemoveMember failed: user=%s tenant=%d err=%v",
				userID, tenantID, err)
			c.Error(apperrors.NewInternalServerError("failed to remove member").WithDetails(err.Error()))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// LeaveTenant godoc
// @Summary      退出当前租户
// @Description  调用方主动退出当前租户。等价于以自己的 user_id 调 RemoveMember，
//
//	但不需要 Owner 权限——非 Owner 也可以自助离开。最后一位 Owner 仍然不能离开
//	（需先把其他成员提升为 Owner），由服务层 ErrLastOwner 拦截。
//
// @Tags         租户成员
// @Produce      json
// @Param        id  path  string  true  "租户 ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /tenants/{id}/leave [post]
func (h *TenantMemberHandler) LeaveTenant(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := h.resolveTenantIDFromPath(c)
	if !ok {
		return
	}
	caller, ok := types.UserIDFromContext(ctx)
	if !ok || caller == "" {
		c.Error(apperrors.NewUnauthorizedError("caller user id missing from context"))
		return
	}

	if err := h.memberService.RemoveMember(ctx, caller, tenantID); err != nil {
		switch {
		case errors.Is(err, service.ErrMembershipNotFound):
			c.Error(apperrors.NewNotFoundError("you are not a member of this tenant"))
		case errors.Is(err, service.ErrLastOwner):
			c.Error(apperrors.NewConflictError(err.Error()))
		default:
			logger.Errorf(ctx, "LeaveTenant failed: user=%s tenant=%d err=%v",
				caller, tenantID, err)
			c.Error(apperrors.NewInternalServerError("failed to leave tenant").WithDetails(err.Error()))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}
