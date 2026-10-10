package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// UserManagementHandler backs Settings → 系统管理 → 用户管理.
//
// The screen has three tabs and all of them are SystemAdmin-gated at the route
// group level (RegisterSystemAdminRoutes applies g.SystemAdmin()), so every
// method here can assume an authenticated platform administrator:
//
//	本地用户  ListUsers / DisableUser / EnableUser / DeleteUser
//	通用OIDC  GetOIDCProvider / UpdateOIDCProvider / TestOIDCProvider
//	LDAP      GetLDAPProvider / UpdateLDAPProvider / TestLDAPProvider
//
// Account creation and password reset are intentionally NOT reimplemented —
// they already exist as POST /system/admin/users/create and
// /system/admin/users/reset-password and the new screen calls those.
type UserManagementHandler struct {
	userSvc         interfaces.UserService
	authProviderSvc interfaces.PlatformAuthProviderService
	tenantSvc       interfaces.TenantService
	// auditSvc is optional; emitAudit no-ops when nil so partially-wired unit
	// tests still compile.
	auditSvc interfaces.AuditLogService
}

// NewUserManagementHandler creates the handler.
func NewUserManagementHandler(
	userSvc interfaces.UserService,
	authProviderSvc interfaces.PlatformAuthProviderService,
	tenantSvc interfaces.TenantService,
	auditSvc interfaces.AuditLogService,
) *UserManagementHandler {
	return &UserManagementHandler{
		userSvc:         userSvc,
		authProviderSvc: authProviderSvc,
		tenantSvc:       tenantSvc,
		auditSvc:        auditSvc,
	}
}

// emitAudit writes a platform-scope (tenant_id=0) audit row for an account or
// provider change. Mirrors SystemHandler.emitAdminAudit so the 审计日志 tab
// shows both families side by side.
func (h *UserManagementHandler) emitAudit(
	ctx context.Context,
	action types.AuditAction,
	targetType string,
	targetID string,
	targetUserID string,
	details map[string]any,
) {
	if h.auditSvc == nil {
		return
	}
	actorID, _ := types.UserIDFromContext(ctx)
	var detailsJSON types.JSON
	if details != nil {
		if b, err := json.Marshal(details); err == nil {
			detailsJSON = types.JSON(b)
		}
	}
	entry := &types.AuditLog{
		TenantID:     0,
		ActorUserID:  actorID,
		ActorRole:    systemAuditActorRole(ctx),
		Action:       action,
		TargetType:   targetType,
		TargetID:     targetID,
		TargetUserID: targetUserID,
		Outcome:      types.AuditOutcomeSuccess,
		Details:      detailsJSON,
	}
	_ = h.auditSvc.Log(ctx, entry)
}

// ---------------------------------------------------------------------------
// 本地用户
// ---------------------------------------------------------------------------

// ListUsers godoc
// @Summary      列出全部用户（用户管理）
// @Description  分页返回平台内所有账号，支持按用户名/邮箱模糊搜索。仅系统管理员可用。
// @Tags         用户管理
// @Produce      json
// @Param        keyword query string false "用户名或邮箱模糊匹配"
// @Param        offset  query int    false "偏移量"
// @Param        limit   query int    false "每页条数（最大 200）"
// @Success      200 {object} types.ListManagedUsersResponse
// @Failure      403 {object} map[string]interface{} "Forbidden"
// @Router       /system/admin/users [get]
func (h *UserManagementHandler) ListUsers(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	offset := 0
	limit := 20
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}
	keyword := strings.TrimSpace(c.Query("keyword"))

	users, total, err := h.userSvc.ListManagedUsers(ctx, keyword, offset, limit)
	if err != nil {
		logger.Errorf(ctx, "Failed to list users: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list users"})
		return
	}
	stats, err := h.userSvc.ManagedUserStats(ctx)
	if err != nil {
		logger.Warnf(ctx, "Failed to compute user stats: %v", err)
		stats = &types.ManagedUserStats{}
	}

	// Resolve workspace names in one batch so the table can show a label
	// instead of a bare numeric tenant id.
	tenantIDs := make([]uint64, 0, len(users))
	seen := map[uint64]bool{}
	for _, u := range users {
		if u.TenantID == 0 || seen[u.TenantID] {
			continue
		}
		seen[u.TenantID] = true
		tenantIDs = append(tenantIDs, u.TenantID)
	}
	tenantNames := map[uint64]string{}
	if len(tenantIDs) > 0 && h.tenantSvc != nil {
		if tenants, terr := h.tenantSvc.GetTenantsByIDs(ctx, tenantIDs); terr == nil {
			for id, t := range tenants {
				if t != nil {
					tenantNames[id] = t.Name
				}
			}
		} else {
			logger.Warnf(ctx, "Failed to resolve tenant names: %v", terr)
		}
	}

	infos := make([]*types.ManagedUserInfo, 0, len(users))
	for _, u := range users {
		infos = append(infos, toManagedUserInfo(u, tenantNames[u.TenantID]))
	}

	c.JSON(http.StatusOK, types.ListManagedUsersResponse{
		Total: total,
		Users: infos,
		Stats: *stats,
	})
}

func toManagedUserInfo(u *types.User, tenantName string) *types.ManagedUserInfo {
	source := u.AuthSource
	if source == "" {
		source = types.AccountAuthSourceLocal
	}
	// Accounts auto-provisioned before auth_source existed can still be
	// recognised from the OIDC preference flag.
	if source == types.AccountAuthSourceLocal &&
		u.Preferences.OidcOnlyLogin != nil && *u.Preferences.OidcOnlyLogin {
		source = types.AccountAuthSourceOIDC
	}
	return &types.ManagedUserInfo{
		ID:                  u.ID,
		Username:            u.Username,
		Email:               u.Email,
		Avatar:              u.Avatar,
		TenantID:            u.TenantID,
		TenantName:          tenantName,
		IsActive:            u.IsActive,
		IsSystemAdmin:       u.IsSystemAdmin,
		CanAccessAllTenants: u.CanAccessAllTenants,
		AuthSource:          source,
		HasLocalPassword:    u.HasUsableLocalPassword(),
		CreatedAt:           u.CreatedAt,
		UpdatedAt:           u.UpdatedAt,
	}
}

// DisableUser godoc
// @Summary      禁用用户
// @Description  禁用账号并撤销其全部会话。系统管理员不能禁用自己。
// @Tags         用户管理
// @Produce      json
// @Param        id path string true "用户 ID"
// @Success      200 {object} types.ManagedUserInfo
// @Router       /system/admin/users/{id}/disable [post]
func (h *UserManagementHandler) DisableUser(c *gin.Context) {
	h.setActive(c, false)
}

// EnableUser godoc
// @Summary      启用用户
// @Description  重新启用已禁用的账号。
// @Tags         用户管理
// @Produce      json
// @Param        id path string true "用户 ID"
// @Success      200 {object} types.ManagedUserInfo
// @Router       /system/admin/users/{id}/enable [post]
func (h *UserManagementHandler) EnableUser(c *gin.Context) {
	h.setActive(c, true)
}

func (h *UserManagementHandler) setActive(c *gin.Context, active bool) {
	ctx := logger.CloneContext(c.Request.Context())

	userID := strings.TrimSpace(c.Param("id"))
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User id is required"})
		return
	}

	callerID, _ := types.UserIDFromContext(ctx)
	if !active && callerID == userID {
		// Disabling yourself would boot the operator out mid-session — the
		// exact foot-gun the SystemAdmin screens exist to prevent.
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot disable your own account"})
		return
	}

	user, err := h.userSvc.SetManagedUserActive(ctx, userID, active)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
			return
		}
		logger.Errorf(ctx, "Failed to change active state for user %s: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update user"})
		return
	}

	action := types.AuditActionSystemUserEnabled
	if !active {
		action = types.AuditActionSystemUserDisabled
	}
	h.emitAudit(ctx, action, "user", user.ID, user.ID, map[string]any{
		"target_email":     user.Email,
		"target_username":  user.Username,
		"sessions_revoked": !active,
	})

	logger.Infof(ctx, "User %s %s by system administrator", user.ID, map[bool]string{true: "enabled", false: "disabled"}[active])
	c.JSON(http.StatusOK, toManagedUserInfo(user, ""))
}

// DeleteUser godoc
// @Summary      删除用户（软删除）
// @Description  墓碑化账号：软删除并撤销全部会话，同时释放邮箱/用户名以便后续重新开通。
// @Description  不能删除自己，也不能删除最后一个系统管理员。
// @Tags         用户管理
// @Produce      json
// @Param        id path string true "用户 ID"
// @Success      200 {object} map[string]interface{}
// @Router       /system/admin/users/{id} [delete]
func (h *UserManagementHandler) DeleteUser(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	userID := strings.TrimSpace(c.Param("id"))
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User id is required"})
		return
	}

	callerID, _ := types.UserIDFromContext(ctx)
	if callerID == userID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot delete your own account"})
		return
	}

	target, err := h.userSvc.GetUserByID(ctx, userID)
	if err != nil || target == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}
	// Deleting the last administrator would lock the platform out of system
	// administration entirely — the same invariant RevokeSystemAdmin enforces.
	if target.IsSystemAdmin {
		admins, _, lerr := h.userSvc.ListSystemAdmins(ctx, 0, 2)
		if lerr != nil {
			logger.Errorf(ctx, "Failed to count system admins before delete: %v", lerr)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify administrator count"})
			return
		}
		if len(admins) <= 1 {
			c.JSON(http.StatusBadRequest,
				gin.H{"error": "Cannot delete the last remaining system administrator"})
			return
		}
	}

	deleted, err := h.userSvc.SoftDeleteManagedUser(ctx, userID)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
			return
		}
		logger.Errorf(ctx, "Failed to delete user %s: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete user"})
		return
	}

	h.emitAudit(ctx, types.AuditActionSystemUserDeleted, "user", userID, userID, map[string]any{
		"target_email":     target.Email,
		"target_username":  target.Username,
		"sessions_revoked": true,
		"soft_delete":      true,
	})

	logger.Infof(ctx, "User %s deleted (tombstoned) by system administrator", userID)
	c.JSON(http.StatusOK, gin.H{
		"message": "User deleted",
		"user":    deleted.ToUserInfo(),
	})
}

// ---------------------------------------------------------------------------
// 通用OIDC
// ---------------------------------------------------------------------------

// GetOIDCProvider godoc
// @Summary      获取通用 OIDC 配置
// @Description  返回当前生效的 OIDC 配置（client_secret 只回传是否已配置）。
// @Tags         用户管理
// @Produce      json
// @Success      200 {object} types.OIDCProviderView
// @Router       /system/admin/auth/oidc [get]
func (h *UserManagementHandler) GetOIDCProvider(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())
	view, err := h.authProviderSvc.GetOIDCProvider(ctx)
	if err != nil {
		logger.Errorf(ctx, "Failed to load OIDC provider: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load OIDC configuration"})
		return
	}
	h.attachUpdatedByName(ctx, view.UpdatedBy, func(name string) { view.UpdatedByName = name })
	c.JSON(http.StatusOK, view)
}

// UpdateOIDCProvider godoc
// @Summary      保存通用 OIDC 配置
// @Description  保存 OIDC 配置。client_secret 为只写字段：留空表示保留原密钥。
// @Tags         用户管理
// @Accept       json
// @Produce      json
// @Param        request body types.OIDCProviderUpdateRequest true "OIDC 配置"
// @Success      200 {object} types.OIDCProviderView
// @Router       /system/admin/auth/oidc [put]
func (h *UserManagementHandler) UpdateOIDCProvider(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	var req types.OIDCProviderUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid OIDC configuration: " + err.Error()})
		return
	}

	view, err := h.authProviderSvc.UpdateOIDCProvider(ctx, &req)
	if err != nil {
		logger.Errorf(ctx, "Failed to update OIDC provider: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.emitAudit(ctx, types.AuditActionSystemAuthProviderUpdated, "auth_provider", "oidc", "", map[string]any{
		"provider":       "oidc",
		"enabled":        view.Enabled,
		"secret_changed": req.ClearSecret || (req.ClientSecret != nil && *req.ClientSecret != ""),
		"client_id":      secutils.SanitizeForLog(view.ClientID),
		"issuer_url":     view.IssuerURL,
	})
	h.attachUpdatedByName(ctx, view.UpdatedBy, func(name string) { view.UpdatedByName = name })
	c.JSON(http.StatusOK, view)
}

// TestOIDCProvider godoc
// @Summary      测试通用 OIDC 连接
// @Description  按当前配置加载发现文档并回显解析出的端点，不修改任何状态。
// @Tags         用户管理
// @Produce      json
// @Success      200 {object} types.OIDCTestResponse
// @Router       /system/admin/auth/oidc/test [post]
func (h *UserManagementHandler) TestOIDCProvider(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())
	resp := h.authProviderSvc.TestOIDCProvider(ctx)
	c.JSON(http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// LDAP
// ---------------------------------------------------------------------------

// GetLDAPProvider godoc
// @Summary      获取 LDAP 配置
// @Description  返回当前 LDAP 配置（bind 密码只回传是否已配置）。
// @Tags         用户管理
// @Produce      json
// @Success      200 {object} types.LDAPProviderView
// @Router       /system/admin/auth/ldap [get]
func (h *UserManagementHandler) GetLDAPProvider(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())
	view, err := h.authProviderSvc.GetLDAPProvider(ctx)
	if err != nil {
		logger.Errorf(ctx, "Failed to load LDAP provider: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load LDAP configuration"})
		return
	}
	h.attachUpdatedByName(ctx, view.UpdatedBy, func(name string) { view.UpdatedByName = name })
	c.JSON(http.StatusOK, view)
}

// UpdateLDAPProvider godoc
// @Summary      保存 LDAP 配置
// @Description  保存 LDAP 配置。bind_password 为只写字段：留空表示保留原密码。
// @Tags         用户管理
// @Accept       json
// @Produce      json
// @Param        request body types.LDAPProviderUpdateRequest true "LDAP 配置"
// @Success      200 {object} types.LDAPProviderView
// @Router       /system/admin/auth/ldap [put]
func (h *UserManagementHandler) UpdateLDAPProvider(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	var req types.LDAPProviderUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid LDAP configuration: " + err.Error()})
		return
	}

	view, err := h.authProviderSvc.UpdateLDAPProvider(ctx, &req)
	if err != nil {
		logger.Errorf(ctx, "Failed to update LDAP provider: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.emitAudit(ctx, types.AuditActionSystemAuthProviderUpdated, "auth_provider", "ldap", "", map[string]any{
		"provider":       "ldap",
		"enabled":        view.Enabled,
		"secret_changed": req.ClearBindPassword || (req.BindPassword != nil && *req.BindPassword != ""),
		"host":           view.Host,
		"base_dn":        view.BaseDN,
	})
	h.attachUpdatedByName(ctx, view.UpdatedBy, func(name string) { view.UpdatedByName = name })
	c.JSON(http.StatusOK, view)
}

// TestLDAPProvider godoc
// @Summary      测试 LDAP 连接
// @Description  依次验证服务账号绑定、用户检索与用户绑定，并回显检测到的 subject 属性。
// @Tags         用户管理
// @Accept       json
// @Produce      json
// @Param        request body types.LDAPTestRequest true "测试参数（均可为空）"
// @Success      200 {object} types.LDAPTestResponse
// @Router       /system/admin/auth/ldap/test [post]
func (h *UserManagementHandler) TestLDAPProvider(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	// The body is entirely optional: a bare POST probes connectivity only.
	var req types.LDAPTestRequest
	_ = c.ShouldBindJSON(&req)

	resp := h.authProviderSvc.TestLDAPProvider(ctx, &req)
	// Never echo the password back, even indirectly.
	if resp.Attributes != nil {
		delete(resp.Attributes, "userPassword")
	}
	c.JSON(http.StatusOK, resp)
}

// attachUpdatedByName resolves the stored actor UUID into a display label.
func (h *UserManagementHandler) attachUpdatedByName(
	ctx context.Context, userID string, set func(string),
) {
	if strings.TrimSpace(userID) == "" || h.userSvc == nil {
		return
	}
	user, err := h.userSvc.GetUserByID(ctx, userID)
	if err != nil || user == nil {
		return
	}
	if user.Username != "" {
		set(user.Username)
		return
	}
	set(user.Email)
}
