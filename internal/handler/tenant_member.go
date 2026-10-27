package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

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

// createMemberAccountRequest is the JSON body for
// POST /tenants/:id/members/create. Unlike addMemberRequest, it carries the
// new account's credentials: this endpoint provisions a user and immediately
// joins them to the workspace, so callers no longer have to pre-register.
type createMemberAccountRequest struct {
	Username string           `json:"username" binding:"required,min=2,max=50"`
	Email    string           `json:"email"    binding:"required,email"`
	Password string           `json:"password" binding:"required"`
	Role     types.TenantRole `json:"role"     binding:"required"`
}

// resetMemberPasswordRequest is the JSON body for
// POST /tenants/:id/members/:user_id/reset-password. The caller supplies the
// new password directly (Q3：管理员自填密码，不强制首次登录改密). The value is
// never logged, echoed back, or written into audit details.
type resetMemberPasswordRequest struct {
	NewPassword string `json:"new_password" binding:"required"`
}

// parseTenantIDFromPath reads :id from the gin route and validates it as
// a tenant ID. Returning (0, false) means we already wrote the error to
// the gin context and the caller should `return` immediately.
func parseTenantIDFromPath(c *gin.Context) (uint64, bool) {
	raw := strings.TrimSpace(c.Param("id"))
	if raw == "" {
		c.Error(apperrors.NewValidationError("workspace id is required"))
		return 0, false
	}
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || v == 0 {
		c.Error(apperrors.NewValidationError("workspace id must be a positive integer"))
		return 0, false
	}
	return v, true
}

// ListMembers godoc
// @Summary      列出空间成员
// @Description  分页返回当前空间内 active 成员（含每位成员的角色、邮箱、头像）；支持 q 按邮箱/用户名筛选
// @Tags         空间成员
// @Produce      json
// @Param        id         path   string  true   "空间 ID"
// @Param        q          query  string  false  "按邮箱/用户名模糊筛选"
// @Param        page       query  int     false  "页码（从 1 起）"  default(1)
// @Param        page_size  query  int     false  "每页数量（最大 100）"  default(20)
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /tenants/{id}/members [get]
func (h *TenantMemberHandler) ListMembers(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := parseTenantIDFromPath(c)
	if !ok {
		return
	}

	q := strings.TrimSpace(c.Query("q"))
	page, pageSize, ok := parseListPagination(c)
	if !ok {
		return
	}

	members, total, err := h.memberService.ListMembersPage(ctx, tenantID, q, page, pageSize)
	if err != nil {
		logger.Errorf(ctx, "ListMembersPage failed: tenant=%d err=%v", tenantID, err)
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
			"members":   resp,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// AddMember godoc
// @Summary      直接添加空间成员（直加路径）
// @Description
//
//	Owner 通过 email 直接把用户作为 active 成员添加进当前空间。
//
//	这是【直加路径】，被加入的用户没有任何确认机会就出现在空间里——
//	保留它是为了三类不需要走邀请确认的场景：
//	  1. 自动化脚本 / 平台运维 / 数据迁移；
//	  2. 跨空间超管 (CanAccessAllTenants) 的批量编排；
//	  3. 对接外部 IdP 时由身份源单向同步成员。
//
//	所有由 UI 触发的「邀请伙伴加入」交互应改走
//	POST /tenants/:id/invitations，那条路径会先创建 pending 行，让被邀请
//	人在 /me/invitations 主动接受后再写 tenant_members 行（PR #1303 后续）。
//	这条路径与 invitations 路径共存而不互相替代。
//
// @Tags         空间成员
// @Accept       json
// @Produce      json
// @Param        id        path  string                 true  "空间 ID"
// @Param        request   body  addMemberRequest       true  "邀请请求"
// @Success      201  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /tenants/{id}/members [post]
func (h *TenantMemberHandler) AddMember(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := parseTenantIDFromPath(c)
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

	// Add the member and write the 201 / mapped-error response through the
	// shared helper (also used by the invitation auto-accept path).
	addMemberAndRespond(c, ctx, h.memberService, user, tenantID, req.Role, invitedBy)
}

// CreateMemberAccount godoc
// @Summary      创建成员账号并加入空间
// @Description  工作区 Owner（系统管理员按 Owner 处理）可创建 admin/contributor/viewer，
// @Description  Admin 只能创建 viewer；任何人都不能创建 owner。一次请求完成建号与入组。
// @Tags         空间成员
// @Accept       json
// @Produce      json
// @Param        id       path  string                      true  "空间 ID"
// @Param        request  body  createMemberAccountRequest  true  "建号请求"
// @Success      201  {object}  map[string]interface{}  "成员已创建（响应不含密码）"
// @Failure      400  {object}  map[string]interface{}  "参数或密码不合规 / 试图创建 owner"
// @Failure      403  {object}  map[string]interface{}  "角色越权"
// @Failure      409  {object}  map[string]interface{}  "邮箱或用户名已注册"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     Bearer
// @Router       /tenants/{id}/members/create [post]
func (h *TenantMemberHandler) CreateMemberAccount(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := parseTenantIDFromPath(c)
	if !ok {
		return
	}

	var req createMemberAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("invalid request body").WithDetails(err.Error()))
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.TrimSpace(req.Email)
	// 密码刻意不做 trim：与 Register / AdminCreateUser 一致，按原字节校验并哈希。
	if n := utf8.RuneCountInString(req.Username); n < 2 || n > 50 {
		c.Error(apperrors.NewValidationError("username must be 2-50 characters"))
		return
	}
	if !req.Role.IsValid() {
		c.Error(apperrors.NewValidationError("role must be one of owner/admin/contributor/viewer"))
		return
	}
	// 任何人都不能创建 owner；owner 转让/新增走另立需求（A2）。
	if req.Role == types.TenantRoleOwner {
		c.Error(apperrors.NewValidationError("cannot create an owner account"))
		return
	}

	// 系统管理员按 owner 处理（D4）；其余调用者取当前活动空间里的角色。
	actorRole := types.CallerFromContext(ctx).Role
	if types.IsSystemAdminFromContext(ctx) {
		actorRole = types.TenantRoleOwner
	}
	if !service.CanCreateRole(actorRole, req.Role) {
		logger.Warnf(ctx, "CreateMemberAccount denied: actor_role=%s target_role=%s tenant=%d",
			actorRole, req.Role, tenantID)
		c.Error(apperrors.NewForbiddenError("your role cannot create a member with this role"))
		return
	}

	// tenantless 模式只建用户、不建个人空间；随后由 AddMember 加入当前空间。
	password := req.Password
	user, _, err := h.userService.AdminCreateUser(ctx, &types.AdminCreateUserRequest{
		Username: req.Username,
		Email:    req.Email,
		Password: &password,
	}, types.TenantProvisioningTenantless)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrUserEmailExists):
			// AC-5：已注册邮箱直接冲突，不覆盖现有账号、也不把老账号拉进空间。
			c.Error(apperrors.NewConflictError(
				"this email is already registered; invite or add the existing user instead"))
		case errors.Is(err, service.ErrUserUsernameExists):
			c.Error(apperrors.NewConflictError("this username is already taken"))
		case errors.Is(err, service.ErrUserIdentityConflict):
			// 邮箱和用户名只撞了一个时，AdminCreateUser 返回的是笼统的
			// identity conflict。这里按邮箱优先（Register 也先查邮箱）补一句
			// 具体提示：前端靠消息里的 email/username 关键字选文案，笼统
			// 消息会被误判成「用户名已占用」（AC-5 要求提示邮箱已注册）。
			if existing, lookupErr := h.userService.GetUserByEmail(ctx, req.Email); lookupErr == nil && existing != nil {
				c.Error(apperrors.NewConflictError(
					"this email is already registered; invite or add the existing user instead"))
			} else {
				c.Error(apperrors.NewConflictError("this username is already taken"))
			}
		case service.IsPasswordPolicyError(err):
			c.Error(apperrors.NewValidationError("password does not meet the password policy"))
		default:
			// 不记录密码；邮箱做脱敏后再进日志。
			logger.Errorf(ctx, "AdminCreateUser failed: email=%s err=%v",
				secutils.SanitizeForLog(req.Email), err)
			c.Error(apperrors.NewInternalServerError("failed to create user account"))
		}
		return
	}

	// invited_by 只记真实人类调用者；API key 的合成用户留空，与 AddMember 一致。
	caller, _ := types.UserIDFromContext(ctx)
	var invitedBy *string
	if caller != "" && !types.IsSyntheticUserID(caller) {
		invitedBy = &caller
	}

	member, err := h.memberService.AddMember(ctx, user.ID, tenantID, req.Role, invitedBy)
	if err != nil {
		// D5：入组失败时尽力软删刚建的用户，避免留下没有任何空间的孤儿账号。
		// 回滚本身失败只告警，对外仍返回入组错误。
		if rollbackErr := h.userService.DeleteUser(ctx, user.ID); rollbackErr != nil {
			logger.Warnf(ctx, "rollback of newly created user failed: user=%s tenant=%d err=%v",
				user.ID, tenantID, rollbackErr)
		}
		writeAddMemberError(c, ctx, user, tenantID, err)
		return
	}

	// AddMember 内部已经发过 rbac.member_added 审计，这里不重复。
	writeAddMemberSuccess(c, user, member)
}

func writeAddMemberError(
	c *gin.Context,
	ctx context.Context,
	user *types.User,
	tenantID uint64,
	err error,
) {
	switch {
	case errors.Is(err, service.ErrInvalidTenantRole):
		c.Error(apperrors.NewValidationError(err.Error()))
	case errors.Is(err, service.ErrAPIKeyCannotAssignOwner):
		c.Error(apperrors.NewForbiddenError(err.Error()))
	case errors.Is(err, service.ErrMembershipAlreadyExists):
		// 409 reads better than 400 here: the request was syntactically
		// fine, the conflict is semantic ("already a member").
		c.Error(apperrors.NewConflictError(err.Error()))
	default:
		logger.Errorf(ctx, "AddMember failed: user=%s tenant=%d err=%v",
			user.ID, tenantID, err)
		c.Error(apperrors.NewInternalServerError("failed to add member").WithDetails(err.Error()))
	}
}

func writeAddMemberSuccess(c *gin.Context, user *types.User, member *types.TenantMember) {
	// Project the freshly added row through the same response shape the
	// list endpoint uses, so the UI can swap "Add Member" UX into the
	// table without an extra round-trip.
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data": types.TenantMemberResponse{
			UserID:    member.UserID,
			Email:     user.Email,
			Username:  user.Username,
			Avatar:    user.Avatar,
			Role:      member.Role,
			Status:    member.Status,
			InvitedBy: member.InvitedBy,
			JoinedAt:  member.JoinedAt,
		},
	})
}

// addMemberAndRespond calls TenantMemberService.AddMember and writes the
// HTTP response: 201 with a TenantMemberResponse on success, or the service
// sentinel mapped to its HTTP status (400 / 403 / 409 / 500) on error. It
// always writes exactly one response, so the caller MUST return right after.
// Shared by TenantMemberHandler.AddMember and the auto-accept branch of
// TenantInvitationHandler.CreateInvitation so the mapping never drifts.
func addMemberAndRespond(
	c *gin.Context,
	ctx context.Context,
	memberService interfaces.TenantMemberService,
	user *types.User,
	tenantID uint64,
	role types.TenantRole,
	invitedBy *string,
) {
	member, err := memberService.AddMember(ctx, user.ID, tenantID, role, invitedBy)
	if err != nil {
		writeAddMemberError(c, ctx, user, tenantID, err)
		return
	}
	writeAddMemberSuccess(c, user, member)
}

// ResetMemberPassword godoc
// @Summary      重置成员密码
// @Description  工作区 Owner（系统管理员按 Owner 处理）可重置 admin/contributor/viewer 的密码，
// @Description  Admin 只能重置 contributor/viewer；不能重置与自己同级或更高级的成员，也不能重置本人。
// @Description  成功后旧密码立即失效，该用户所有已签发 token 被吊销。
// @Tags         空间成员
// @Accept       json
// @Produce      json
// @Param        id        path  string                     true  "空间 ID"
// @Param        user_id   path  string                     true  "用户 ID"
// @Param        request   body  resetMemberPasswordRequest  true  "新密码"
// @Success      200  {object}  map[string]interface{}  "已重置（响应不含密码）"
// @Failure      400  {object}  map[string]interface{}  "参数或密码不合规 / 重置本人"
// @Failure      403  {object}  map[string]interface{}  "角色越权"
// @Failure      404  {object}  map[string]interface{}  "目标不是本空间成员"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     Bearer
// @Router       /tenants/{id}/members/{user_id}/reset-password [post]
func (h *TenantMemberHandler) ResetMemberPassword(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := parseTenantIDFromPath(c)
	if !ok {
		return
	}
	targetUserID := strings.TrimSpace(c.Param("user_id"))
	if targetUserID == "" {
		c.Error(apperrors.NewValidationError("user_id is required"))
		return
	}

	var req resetMemberPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("invalid request body").WithDetails(err.Error()))
		return
	}
	// 密码刻意不做 trim：与 Register / AdminResetPassword 一致，按原字节校验并哈希。

	// 本人走「修改密码」接口（需要旧密码），管理端重置不允许作用于自己。
	callerID, _ := types.UserIDFromContext(ctx)
	if callerID != "" && callerID == targetUserID {
		c.Error(apperrors.NewValidationError(
			"cannot reset your own password; use the change-password endpoint"))
		return
	}

	// 先确认目标确实是当前空间的 active 成员，才能拿到目标角色做矩阵判定。
	member, err := h.memberService.GetMembership(ctx, targetUserID, tenantID)
	if err != nil {
		logger.Errorf(ctx, "GetMembership failed: user=%s tenant=%d err=%v",
			targetUserID, tenantID, err)
		c.Error(apperrors.NewInternalServerError("failed to look up membership").WithDetails(err.Error()))
		return
	}
	if member == nil {
		c.Error(apperrors.NewNotFoundError("membership not found"))
		return
	}

	// 系统管理员按 owner 处理（D4）；其余调用者取当前活动空间里的角色。
	actorRole := types.CallerFromContext(ctx).Role
	if types.IsSystemAdminFromContext(ctx) {
		actorRole = types.TenantRoleOwner
	}
	if !service.CanResetRole(actorRole, member.Role) {
		logger.Warnf(ctx, "ResetMemberPassword denied: actor_role=%s target_role=%s tenant=%d",
			actorRole, member.Role, tenantID)
		c.Error(apperrors.NewForbiddenError("your role cannot reset this member's password"))
		return
	}

	// AdminResetPassword 内部已做密码策略校验，并吊销该用户全部 token。
	// 失败分支不记录密码，只记目标 ID 与错误。
	if err := h.userService.AdminResetPassword(ctx, targetUserID, req.NewPassword); err != nil {
		if service.IsPasswordPolicyError(err) {
			c.Error(apperrors.NewValidationError("password does not meet the password policy"))
			return
		}
		logger.Errorf(ctx, "AdminResetPassword failed: user=%s tenant=%d err=%v",
			targetUserID, tenantID, err)
		c.Error(apperrors.NewInternalServerError("failed to reset member password"))
		return
	}

	// 审计通过 member service 现有 emitAudit 通道发出；details 不含密码。
	// 用窄接口做可选断言，避免给 TenantMemberService 接口新增方法而破坏
	// 既有实现/测试替身。
	if auditor, ok := h.memberService.(interfaces.MemberPasswordResetAuditor); ok {
		auditor.AuditMemberPasswordReset(ctx, tenantID, targetUserID, member.Role)
	} else {
		logger.Warnf(ctx,
			"member service does not implement MemberPasswordResetAuditor; audit row skipped: tenant=%d user=%s",
			tenantID, targetUserID)
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// UpdateMemberRole godoc
// @Summary      修改空间成员角色
// @Description  Owner 修改某位成员在当前空间内的角色；不能将最后一位 Owner 降级
// @Tags         空间成员
// @Accept       json
// @Produce      json
// @Param        id       path  string                  true  "空间 ID"
// @Param        user_id  path  string                  true  "用户 ID"
// @Param        request  body  updateMemberRoleRequest true  "目标角色"
// @Success      200  {object}  handler.SuccessResponse
// @Security     Bearer
// @Router       /tenants/{id}/members/{user_id} [put]
func (h *TenantMemberHandler) UpdateMemberRole(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := parseTenantIDFromPath(c)
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
		case errors.Is(err, service.ErrAPIKeyCannotAssignOwner):
			c.Error(apperrors.NewForbiddenError(err.Error()))
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
// @Summary      移除空间成员
// @Description  Owner 将某位成员从当前空间中移除（软删除 tenant_members 行）；不能移除最后一位 Owner
// @Tags         空间成员
// @Produce      json
// @Param        id       path  string  true  "空间 ID"
// @Param        user_id  path  string  true  "用户 ID"
// @Success      200  {object}  handler.SuccessResponse
// @Security     Bearer
// @Router       /tenants/{id}/members/{user_id} [delete]
func (h *TenantMemberHandler) RemoveMember(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := parseTenantIDFromPath(c)
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
// @Summary      退出当前空间
// @Description  调用方主动退出当前空间。等价于以自己的 user_id 调 RemoveMember，
//
//	但不需要 Owner 权限——非 Owner 也可以自助离开。最后一位 Owner 仍然不能离开
//	（需先把其他成员提升为 Owner），由服务层 ErrLastOwner 拦截。
//
// @Tags         空间成员
// @Produce      json
// @Param        id  path  string  true  "空间 ID"
// @Success      200  {object}  handler.SuccessResponse
// @Security     Bearer
// @Router       /tenants/{id}/leave [post]
func (h *TenantMemberHandler) LeaveTenant(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := parseTenantIDFromPath(c)
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
			c.Error(apperrors.NewNotFoundError("you are not a member of this workspace"))
		case errors.Is(err, service.ErrLastOwner):
			c.Error(apperrors.NewConflictError(err.Error()))
		default:
			logger.Errorf(ctx, "LeaveTenant failed: user=%s tenant=%d err=%v",
				caller, tenantID, err)
			c.Error(apperrors.NewInternalServerError("failed to leave workspace").WithDetails(err.Error()))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}
