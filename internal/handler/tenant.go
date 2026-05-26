package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// TenantHandler implements HTTP request handlers for tenant management
// Provides functionality for creating, retrieving, updating, and deleting tenants
// through the REST API endpoints
type TenantHandler struct {
	service       interfaces.TenantService
	userService   interfaces.UserService
	memberService interfaces.TenantMemberService
	kbService     interfaces.KnowledgeBaseService
	config        *config.Config
}

// NewTenantHandler creates a new tenant handler instance with the provided service
// Parameters:
//   - service: An implementation of the TenantService interface for business logic
//   - userService: An implementation of the UserService interface for user operations
//   - memberService: An implementation of TenantMemberService used to bootstrap
//     the creator as Owner of the tenant they just created (self-service create).
//   - config: Application configuration
//
// # Returns a pointer to the newly created TenantHandler
//
// Note on RBAC: cross-tenant gating (CanAccessAllTenants /
// EnableCrossTenantAccess) and per-tenant path matching (URL :id ==
// active tenant) used to live in `authorizeTenantAccess` and the if
// blocks at the top of ListAllTenants / SearchTenants. Both moved to
// `middleware/access.go` (RequireCrossTenantAccess /
// RequirePathTenantMatch) and are wired in `router.go` so the handler
// stays focused on business logic.
func NewTenantHandler(
	service interfaces.TenantService,
	userService interfaces.UserService,
	memberService interfaces.TenantMemberService,
	kbService interfaces.KnowledgeBaseService,
	config *config.Config,
) *TenantHandler {
	return &TenantHandler{
		service:       service,
		userService:   userService,
		memberService: memberService,
		kbService:     kbService,
		config:        config,
	}
}

// createTenantRequest is the JSON body for POST /tenants. Only fields a
// regular authenticated user is allowed to set are accepted; everything
// else (api_key, status, storage_quota, retriever_engines, etc.) is
// generated server-side by TenantService.CreateTenant so a normal user
// can't bypass quotas or self-suspend a workspace at create time.
//
// Cross-tenant superusers historically posted the full Tenant struct to
// this endpoint. We keep that path working by binding into the same
// types.Tenant when CanAccessAllTenants is true (see CreateTenant
// below), but the recommended shape going forward is name+description.
type createTenantRequest struct {
	Name        string `json:"name" binding:"required,min=1,max=128"`
	Description string `json:"description" binding:"max=512"`
}

// updateTenantRequest is the JSON body for PUT /tenants/:id. Only the
// fields an Owner is permitted to mutate via the public API are bound;
// everything else (storage_quota, status, business, api_key, agent /
// retrieval / storage configs, ...) is intentionally NOT writable here
// — those go through dedicated endpoints (POST /:id/api-key,
// PUT /tenants/kv/:key, ...) that have their own validation.
//
// Pointers so we can distinguish "not sent" from "explicit empty
// string"; when nil we leave the existing column untouched.
type updateTenantRequest struct {
	Name        *string `json:"name"        binding:"omitempty,min=1,max=128"`
	Description *string `json:"description" binding:"omitempty,max=512"`
}

// defaultMaxOwnedTenantsPerUser is the cap applied when
// config.Tenant.MaxOwnedPerUser is left at zero. Picked to comfortably
// cover legitimate "personal + a couple of side-projects" use while
// blunting drive-by abuse against POST /tenants (see CreateTenant).
const defaultMaxOwnedTenantsPerUser = 10

// CreateTenant godoc
// @Summary      创建租户
// @Description  创建新的租户。任意已登录用户均可调用以建立自己的新工作区，
// @Description  调用方会被自动设为该租户的 Owner。跨租户超管仍可像以前一样
// @Description  通过本接口创建任意租户。
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Param        request  body      handler.createTenantRequest  true  "租户信息"
// @Success      201      {object}  map[string]interface{}  "创建的租户"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Router       /tenants [post]
func (h *TenantHandler) CreateTenant(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start creating tenant")

	// Resolve the caller; required so we can bootstrap the Owner
	// membership and so we can branch on cross-tenant superuser status
	// for the legacy full-payload path.
	caller, err := h.userService.GetCurrentUser(ctx)
	if err != nil || caller == nil {
		logger.Error(ctx, "Failed to resolve current user from context", err)
		c.Error(errors.NewUnauthorizedError("authentication required"))
		return
	}

	var tenantData types.Tenant

	if caller.CanAccessAllTenants {
		// Backward-compatible path for cross-tenant superusers: accept
		// the full Tenant payload (status, storage_quota, retriever
		// engines, configs...) so existing tooling keeps working.
		if err := c.ShouldBindJSON(&tenantData); err != nil {
			logger.Error(ctx, "Failed to parse request parameters", err)
			appErr := errors.NewValidationError("Invalid request parameters").WithDetails(err.Error())
			c.Error(appErr)
			return
		}
		// Reset client-supplied primary key so we don't accidentally
		// insert with a chosen ID that collides with a future
		// auto-increment value. Tenant IDs must always be DB-generated.
		tenantData.ID = 0
	} else {
		// Self-service path: a regular user can only set name and
		// description. Everything else is server-generated by
		// TenantService.CreateTenant (api_key, status="active",
		// storage_quota default, retriever engines from RETRIEVE_DRIVER).
		var req createTenantRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			logger.Error(ctx, "Failed to parse request parameters", err)
			appErr := errors.NewValidationError("Invalid request parameters").WithDetails(err.Error())
			c.Error(appErr)
			return
		}

		// Per-user quota: cap how many tenants a regular user can spin
		// up via self-service. Without this any authenticated client
		// can flood `tenants` (and saturate validateStorageBucketUniqueness
		// which scans the whole table). Superusers above are exempt
		// because they're already trusted to manage the catalog.
		if h.memberService != nil {
			memberships, listErr := h.memberService.ListByUser(ctx, caller.ID)
			if listErr != nil {
				logger.Errorf(ctx, "Failed to count owned tenants for user %s: %v", caller.ID, listErr)
				c.Error(errors.NewInternalServerError("Failed to validate tenant quota").WithDetails(listErr.Error()))
				return
			}
			ownedCount := 0
			for _, m := range memberships {
				if m != nil && m.Role == types.TenantRoleOwner {
					ownedCount++
				}
			}
			cap := defaultMaxOwnedTenantsPerUser
			if h.config != nil && h.config.Tenant != nil && h.config.Tenant.MaxOwnedPerUser != 0 {
				cap = h.config.Tenant.MaxOwnedPerUser
			}
			if cap > 0 && ownedCount >= cap {
				logger.Warnf(ctx,
					"User %s reached self-service tenant quota (%d/%d)",
					caller.ID, ownedCount, cap,
				)
				c.Error(errors.NewTooManyRequestsError(
					"reached self-service tenant quota; contact an administrator to raise the limit",
				))
				return
			}
		}

		tenantData = types.Tenant{
			Name:        strings.TrimSpace(req.Name),
			Description: strings.TrimSpace(req.Description),
		}
	}

	logger.Infof(ctx, "Creating tenant, name: %s", secutils.SanitizeForLog(tenantData.Name))

	createdTenant, err := h.service.CreateTenant(ctx, &tenantData)
	if err != nil {
		// Check if this is an application-specific error
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to create tenant: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to create tenant").WithDetails(err.Error()))
		}
		return
	}

	// Bootstrap an Owner membership so the caller immediately has full
	// control over the tenant they just created. We MUST roll the tenant
	// back if this fails: without a membership row the new tenant is
	// unreachable (middleware/auth.go's orphan-recovery only fires for a
	// user's home tenant, never for a freshly-created side workspace),
	// yet still occupies storage_bucket / name uniqueness slots.
	// Idempotent: EnsureOwner is a no-op when the row already exists,
	// so cross-tenant superusers create-and-own through the same path.
	if h.memberService != nil {
		if _, err := h.memberService.EnsureOwner(ctx, caller.ID, createdTenant.ID); err != nil {
			logger.Errorf(ctx,
				"Failed to bootstrap owner membership for user %s tenant %d: %v — rolling back tenant",
				caller.ID, createdTenant.ID, err)
			if delErr := h.service.DeleteTenant(ctx, createdTenant.ID); delErr != nil {
				logger.Errorf(ctx,
					"Rollback DeleteTenant failed for orphan tenant %d: %v",
					createdTenant.ID, delErr,
				)
			}
			c.Error(errors.NewInternalServerError("Failed to finalise tenant ownership").WithDetails(err.Error()))
			return
		}

		// Quota TOCTOU guard. The earlier ownedCount check is racy:
		// N concurrent CreateTenant calls all read ownedCount < cap,
		// all proceed, all insert. Re-count AFTER the Owner membership
		// is committed; if we landed over the cap, roll back this
		// tenant + its membership so the bound holds in steady state.
		// We only do this for non-superusers (the only path that has
		// a cap) — superusers are exempt above.
		if !caller.CanAccessAllTenants {
			memberships, listErr := h.memberService.ListByUser(ctx, caller.ID)
			if listErr != nil {
				logger.Errorf(ctx, "Post-create quota recount failed for user %s tenant %d: %v",
					caller.ID, createdTenant.ID, listErr)
			} else {
				ownedNow := 0
				for _, m := range memberships {
					if m != nil && m.Role == types.TenantRoleOwner {
						ownedNow++
					}
				}
				cap := defaultMaxOwnedTenantsPerUser
				if h.config != nil && h.config.Tenant != nil && h.config.Tenant.MaxOwnedPerUser != 0 {
					cap = h.config.Tenant.MaxOwnedPerUser
				}
				if cap > 0 && ownedNow > cap {
					logger.Warnf(ctx,
						"User %s exceeded tenant quota after concurrent create (%d/%d), rolling back tenant %d",
						caller.ID, ownedNow, cap, createdTenant.ID,
					)
					if rmErr := h.memberService.RemoveMember(ctx, caller.ID, createdTenant.ID); rmErr != nil {
						logger.Errorf(ctx,
							"Rollback RemoveMember failed for user %s tenant %d: %v",
							caller.ID, createdTenant.ID, rmErr,
						)
					}
					if delErr := h.service.DeleteTenant(ctx, createdTenant.ID); delErr != nil {
						logger.Errorf(ctx,
							"Rollback DeleteTenant failed for over-quota tenant %d: %v",
							createdTenant.ID, delErr,
						)
					}
					c.Error(errors.NewTooManyRequestsError(
						"reached self-service tenant quota; contact an administrator to raise the limit",
					))
					return
				}
			}
		}
	}

	logger.Infof(
		ctx,
		"Tenant created successfully, ID: %d, name: %s",
		createdTenant.ID,
		secutils.SanitizeForLog(createdTenant.Name),
	)
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    createdTenant,
	})
}

// GetTenant godoc
// @Summary      获取租户详情
// @Description  根据ID获取租户详情
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Param        id   path      int  true  "租户ID"
// @Success      200  {object}  map[string]interface{}  "租户详情"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      404  {object}  errors.AppError         "租户不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /tenants/{id} [get]
func (h *TenantHandler) GetTenant(c *gin.Context) {
	ctx := c.Request.Context()

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		logger.Errorf(ctx, "Invalid tenant ID: %s", secutils.SanitizeForLog(c.Param("id")))
		c.Error(errors.NewBadRequestError("Invalid tenant ID"))
		return
	}

	tenant, err := h.service.GetTenantByID(ctx, id)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to retrieve tenant: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to retrieve tenant").WithDetails(err.Error()))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    tenant,
	})
}

// UpdateTenant godoc
// @Summary      更新租户
// @Description  更新租户信息
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Param        id       path      int           true  "租户ID"
// @Param        request  body      types.Tenant  true  "租户信息"
// @Success      200      {object}  map[string]interface{}  "更新后的租户"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Router       /tenants/{id} [put]
func (h *TenantHandler) UpdateTenant(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start updating tenant")

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		logger.Errorf(ctx, "Invalid tenant ID: %s", secutils.SanitizeForLog(c.Param("id")))
		c.Error(errors.NewBadRequestError("Invalid tenant ID"))
		return
	}

	// Strict whitelist: only Name / Description are mutable through the
	// public PUT. Storage quota, status, business, configs, api_key and
	// every other privileged column live behind dedicated endpoints
	// (POST /:id/api-key, PUT /tenants/kv/:key, ...). Without this, an
	// Owner — including any user who just self-served a tenant — could
	// flip status / bump storage_quota by simply crafting an extended
	// JSON body. Pointers distinguish "field omitted" from "explicit
	// empty string" so we can leave untouched columns alone.
	var req updateTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}

	// Load the persisted tenant so any column the request omits keeps
	// its current value through the GORM `Updates(struct)` zero-skip
	// behaviour (we always pass back the full struct).
	existing, err := h.service.GetTenantByID(ctx, id)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to load tenant").WithDetails(err.Error()))
		}
		return
	}

	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" {
			c.Error(errors.NewValidationError("name cannot be blank"))
			return
		}
		existing.Name = trimmed
	}
	if req.Description != nil {
		existing.Description = strings.TrimSpace(*req.Description)
	}

	logger.Infof(ctx, "Updating tenant, ID: %d, Name: %s", id, secutils.SanitizeForLog(existing.Name))

	updatedTenant, err := h.service.UpdateTenant(ctx, existing)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to update tenant: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to update tenant").WithDetails(err.Error()))
		}
		return
	}

	logger.Infof(
		ctx,
		"Tenant updated successfully, ID: %d, Name: %s",
		updatedTenant.ID,
		secutils.SanitizeForLog(updatedTenant.Name),
	)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    updatedTenant,
	})
}

// ResetAPIKey godoc
// @Summary      重置租户 API Key
// @Description  为指定租户生成一个新的 API Key，旧 Key 立即失效
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Param        id   path      int  true  "租户ID"
// @Success      200  {object}  map[string]interface{}  "新生成的 API Key"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      403  {object}  errors.AppError         "权限不足"
// @Security     Bearer
// @Router       /tenants/{id}/api-key [post]
func (h *TenantHandler) ResetAPIKey(c *gin.Context) {
	ctx := c.Request.Context()

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		logger.Errorf(ctx, "Invalid tenant ID: %s", secutils.SanitizeForLog(c.Param("id")))
		c.Error(errors.NewBadRequestError("Invalid tenant ID"))
		return
	}

	logger.Infof(ctx, "Resetting API key for tenant, ID: %d", id)
	apiKey, err := h.service.UpdateAPIKey(ctx, id)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to reset API key: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to reset API key").WithDetails(err.Error()))
		}
		return
	}

	logger.Infof(ctx, "API key reset successfully, tenant ID: %d", id)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"api_key": apiKey,
		},
	})
}

// DeleteTenant godoc
// @Summary      删除租户
// @Description  删除指定的租户
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Param        id   path      int  true  "租户ID"
// @Success      200  {object}  map[string]interface{}  "删除成功"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Router       /tenants/{id} [delete]
func (h *TenantHandler) DeleteTenant(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start deleting tenant")

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		logger.Errorf(ctx, "Invalid tenant ID: %s", secutils.SanitizeForLog(c.Param("id")))
		c.Error(errors.NewBadRequestError("Invalid tenant ID"))
		return
	}

	logger.Infof(ctx, "Deleting tenant, ID: %d", id)

	if err := h.service.DeleteTenant(ctx, id); err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to delete tenant: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to delete tenant").WithDetails(err.Error()))
		}
		return
	}

	logger.Infof(ctx, "Tenant deleted successfully, ID: %d", id)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Tenant deleted successfully",
	})
}

// ListTenants godoc
// @Summary      获取租户列表
// @Description  获取当前用户可访问的租户列表
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "租户列表"
// @Failure      500  {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Router       /tenants [get]
func (h *TenantHandler) ListTenants(c *gin.Context) {
	ctx := c.Request.Context()

	tenant, ok := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	if !ok || tenant == nil {
		c.Error(errors.NewUnauthorizedError("Authentication required"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items": []*types.Tenant{tenant},
		},
	})
}

// ListAllTenants godoc
// @Summary      获取所有租户列表
// @Description  获取系统中所有租户（需要跨租户访问权限）
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "所有租户列表"
// @Failure      403  {object}  errors.AppError         "权限不足"
// @Security     Bearer
// @Router       /tenants/all [get]
func (h *TenantHandler) ListAllTenants(c *gin.Context) {
	ctx := c.Request.Context()

	// Cross-tenant gating (CanAccessAllTenants + EnableCrossTenantAccess)
	// is enforced at the route layer via middleware.RequireCrossTenantAccess
	// (router.go). The handler stays focused on listing.
	tenants, err := h.service.ListAllTenants(ctx)
	if err != nil {
		// Check if this is an application-specific error
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to retrieve all tenants list: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to retrieve all tenants list").WithDetails(err.Error()))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items": tenants,
		},
	})
}

// SearchTenants godoc
// @Summary      搜索租户
// @Description  分页搜索租户（需要跨租户访问权限）
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Param        keyword    query     string  false  "搜索关键词"
// @Param        tenant_id  query     int     false  "租户ID筛选"
// @Param        page       query     int     false  "页码"  default(1)
// @Param        page_size  query     int     false  "每页数量"  default(20)
// @Success      200        {object}  map[string]interface{}  "搜索结果"
// @Failure      403        {object}  errors.AppError         "权限不足"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /tenants/search [get]
func (h *TenantHandler) SearchTenants(c *gin.Context) {
	ctx := c.Request.Context()

	// Cross-tenant gating is enforced at the route layer via
	// middleware.RequireCrossTenantAccess (router.go); the handler only
	// parses query params and delegates to the service.

	// Parse query parameters
	keyword := c.Query("keyword")
	tenantIDStr := c.Query("tenant_id")
	pageStr := c.DefaultQuery("page", "1")
	pageSizeStr := c.DefaultQuery("page_size", "20")

	var tenantID uint64
	if tenantIDStr != "" {
		parsedID, err := strconv.ParseUint(tenantIDStr, 10, 64)
		if err == nil {
			tenantID = parsedID
		}
	}

	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}

	pageSize, err := strconv.Atoi(pageSizeStr)
	if err != nil || pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100 // Limit max page size
	}

	tenants, total, err := h.service.SearchTenants(ctx, keyword, tenantID, page, pageSize)
	if err != nil {
		// Check if this is an application-specific error
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to search tenants: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to search tenants").WithDetails(err.Error()))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items":     tenants,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// GetTenantKV godoc
// @Summary      获取租户KV配置
// @Description  获取租户级别的KV配置（支持web-search-config、prompt-templates、parser-engine-config、storage-engine-config、chat-history-config、retrieval-config）
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Param        key  path      string  true  "配置键名"
// @Success      200  {object}  map[string]interface{}  "配置值"
// @Failure      400  {object}  errors.AppError         "不支持的键"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /tenants/kv/{key} [get]
func (h *TenantHandler) GetTenantKV(c *gin.Context) {
	ctx := c.Request.Context()
	key := secutils.SanitizeForLog(c.Param("key"))

	switch key {
	case "web-search-config":
		h.GetTenantWebSearchConfig(c)
		return
	case "prompt-templates":
		h.GetPromptTemplates(c)
		return
	case "parser-engine-config":
		h.GetTenantParserEngineConfig(c)
		return
	case "storage-engine-config":
		h.GetTenantStorageEngineConfig(c)
		return
	case "chat-history-config":
		h.GetTenantChatHistoryConfig(c)
		return
	case "retrieval-config":
		h.GetTenantRetrievalConfig(c)
		return
	default:
		logger.Info(ctx, "KV key not supported", "key", key)
		c.Error(errors.NewBadRequestError("unsupported key"))
		return
	}
}

// UpdateTenantKV godoc
// @Summary      更新租户KV配置
// @Description  更新租户级别的KV配置（支持web-search-config、parser-engine-config、storage-engine-config、chat-history-config、retrieval-config）
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Param        key      path      string  true  "配置键名"
// @Param        request  body      object  true  "配置值"
// @Success      200      {object}  map[string]interface{}  "更新成功"
// @Failure      400      {object}  errors.AppError         "不支持的键"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /tenants/kv/{key} [put]
func (h *TenantHandler) UpdateTenantKV(c *gin.Context) {
	ctx := c.Request.Context()
	key := secutils.SanitizeForLog(c.Param("key"))

	switch key {
	case "web-search-config":
		h.updateTenantWebSearchConfigInternal(c)
		return
	case "parser-engine-config":
		h.updateTenantParserEngineConfigInternal(c)
		return
	case "storage-engine-config":
		h.updateTenantStorageEngineConfigInternal(c)
		return
	case "chat-history-config":
		h.updateTenantChatHistoryConfigInternal(c)
		return
	case "retrieval-config":
		h.updateTenantRetrievalConfigInternal(c)
		return
	default:
		logger.Info(ctx, "KV key not supported", "key", key)
		c.Error(errors.NewBadRequestError("unsupported key"))
		return
	}
}

// updateTenantWebSearchConfigInternal updates tenant's web search config
func (h *TenantHandler) updateTenantWebSearchConfigInternal(c *gin.Context) {
	ctx := c.Request.Context()

	// Bind directly into the strong typed struct
	var cfg types.WebSearchConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}

	cfg = *types.EffectiveWebSearchConfig(&cfg)

	// Validate configuration
	if cfg.MaxResults < 1 || cfg.MaxResults > 50 {
		c.Error(errors.NewBadRequestError("max_results must be between 1 and 50"))
		return
	}

	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}

	tenant.WebSearchConfig = &cfg
	updatedTenant, err := h.service.UpdateTenant(ctx, tenant)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to update tenant: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to update tenant web search config").WithDetails(err.Error()))
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    types.EffectiveWebSearchConfig(updatedTenant.WebSearchConfig),
		"message": "Web search configuration updated successfully",
	})
}

// GetTenantWebSearchConfig godoc
// @Summary      获取租户网络搜索配置
// @Description  获取租户的网络搜索配置
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "网络搜索配置"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /tenants/kv/web-search-config [get]
func (h *TenantHandler) GetTenantWebSearchConfig(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start getting tenant web search config")
	// Get tenant
	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}

	logger.Infof(ctx, "Tenant web search config retrieved successfully, Tenant ID: %d", tenant.ID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    types.EffectiveWebSearchConfig(tenant.WebSearchConfig),
	})
}

// GetTenantParserEngineConfig returns the tenant's parser engine config (MinerU endpoint, API key, etc.).
func (h *TenantHandler) GetTenantParserEngineConfig(c *gin.Context) {
	ctx := c.Request.Context()
	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}
	data := tenant.ParserEngineConfig
	if data == nil {
		data = &types.ParserEngineConfig{}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    data,
	})
}

// updateTenantParserEngineConfigInternal updates the tenant's parser engine config.
func (h *TenantHandler) updateTenantParserEngineConfigInternal(c *gin.Context) {
	ctx := c.Request.Context()
	var cfg types.ParserEngineConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}
	tenant.ParserEngineConfig = &cfg
	updatedTenant, err := h.service.UpdateTenant(ctx, tenant)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to update tenant parser engine config").WithDetails(err.Error()))
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    updatedTenant.ParserEngineConfig,
		"message": "解析引擎配置已更新",
	})
}

// GetTenantStorageEngineConfig returns the tenant's storage engine config (Local, MinIO, COS parameters).
func (h *TenantHandler) GetTenantStorageEngineConfig(c *gin.Context) {
	ctx := c.Request.Context()
	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}
	data := tenant.StorageEngineConfig
	if data == nil {
		data = &types.StorageEngineConfig{}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    data,
	})
}

// updateTenantStorageEngineConfigInternal updates the tenant's storage engine config.
func (h *TenantHandler) updateTenantStorageEngineConfigInternal(c *gin.Context) {
	ctx := c.Request.Context()
	var cfg types.StorageEngineConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	provider := strings.ToLower(strings.TrimSpace(cfg.DefaultProvider))
	if provider == "" {
		provider = firstAllowedStorageProvider()
	}
	if provider == "" {
		c.Error(errors.NewBadRequestError("No storage provider is allowed by STORAGE_ALLOW_LIST"))
		return
	}
	if !isStorageProviderAllowed(provider) {
		c.Error(errors.NewBadRequestError("Storage provider is not allowed by STORAGE_ALLOW_LIST"))
		return
	}
	cfg.DefaultProvider = provider
	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}
	tenant.StorageEngineConfig = &cfg
	updatedTenant, err := h.service.UpdateTenant(ctx, tenant)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to update tenant storage engine config").WithDetails(err.Error()))
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    updatedTenant.StorageEngineConfig,
		"message": "存储引擎配置已更新",
	})
}

// GetPromptTemplates godoc
// @Summary      获取提示词模板
// @Description  获取系统配置的提示词模板列表
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "提示词模板配置"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /tenants/kv/prompt-templates [get]
func (h *TenantHandler) GetPromptTemplates(c *gin.Context) {
	// Return prompt templates from config.yaml
	templates := h.config.PromptTemplates
	if templates == nil {
		templates = &config.PromptTemplatesConfig{}
	}

	// Determine user language from context (set by Language middleware)
	lang, _ := types.LanguageFromContext(c.Request.Context())

	// Build a localized copy so the original config is never mutated
	localized := &config.PromptTemplatesConfig{
		SystemPrompt:         config.LocalizeTemplates(templates.SystemPrompt, lang),
		ContextTemplate:      config.LocalizeTemplates(templates.ContextTemplate, lang),
		Rewrite:              config.LocalizeTemplates(templates.Rewrite, lang),
		Fallback:             config.LocalizeTemplates(templates.Fallback, lang),
		GenerateSessionTitle: templates.GenerateSessionTitle,
		GenerateSummary:      templates.GenerateSummary,
		KeywordsExtraction:   templates.KeywordsExtraction,
		AgentSystemPrompt:    config.LocalizeTemplates(templates.AgentSystemPrompt, lang),
		IntentPrompts:        config.LocalizeTemplates(templates.IntentPrompts, lang),
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    localized,
	})
}

// GetTenantChatHistoryConfig returns the tenant's chat history KB configuration.
func (h *TenantHandler) GetTenantChatHistoryConfig(c *gin.Context) {
	ctx := c.Request.Context()
	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}
	data := tenant.ChatHistoryConfig
	if data == nil {
		data = &types.ChatHistoryConfig{}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    data,
	})
}

// updateTenantChatHistoryConfigInternal updates the tenant's chat history KB configuration.
// When enabled with an embedding model and no KB exists yet, it auto-creates a hidden KB.
func (h *TenantHandler) updateTenantChatHistoryConfigInternal(c *gin.Context) {
	ctx := c.Request.Context()

	// The frontend sends: enabled, embedding_model_id
	// knowledge_base_id is managed internally.
	var req types.ChatHistoryConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}

	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}

	existing := tenant.ChatHistoryConfig

	// Build the new config, preserving the internally-managed knowledge_base_id
	cfg := &types.ChatHistoryConfig{
		Enabled:          req.Enabled,
		EmbeddingModelID: req.EmbeddingModelID,
		KnowledgeBaseID:  "", // will be set below
	}

	// Carry over existing KB ID if the embedding model hasn't changed
	if existing != nil && existing.KnowledgeBaseID != "" {
		if existing.EmbeddingModelID == req.EmbeddingModelID {
			cfg.KnowledgeBaseID = existing.KnowledgeBaseID
		} else {
			// Embedding model changed — the old KB is incompatible.
			// We'll create a new one below. The old KB remains but is orphaned (can be cleaned up later).
			logger.Infof(ctx, "Embedding model changed from %s to %s, will create new chat history KB", existing.EmbeddingModelID, req.EmbeddingModelID)
		}
	}

	// Auto-create hidden KB if enabled + model set + no KB yet
	if cfg.Enabled && cfg.EmbeddingModelID != "" && cfg.KnowledgeBaseID == "" {
		kb := &types.KnowledgeBase{
			Name:             "__chat_history__",
			Type:             types.KnowledgeBaseTypeDocument,
			IsTemporary:      true,
			Description:      "Auto-managed knowledge base for chat history message indexing",
			EmbeddingModelID: cfg.EmbeddingModelID,
		}
		createdKB, err := h.kbService.CreateKnowledgeBase(ctx, kb)
		if err != nil {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to create chat history knowledge base").WithDetails(err.Error()))
			return
		}
		cfg.KnowledgeBaseID = createdKB.ID
		logger.Infof(ctx, "Auto-created chat history KB: id=%s, embedding_model=%s", createdKB.ID, cfg.EmbeddingModelID)
	}

	tenant.ChatHistoryConfig = cfg
	updatedTenant, err := h.service.UpdateTenant(ctx, tenant)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to update chat history config").WithDetails(err.Error()))
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    updatedTenant.ChatHistoryConfig,
		"message": "Chat history configuration updated successfully",
	})
}

// GetTenantRetrievalConfig returns the tenant's global retrieval configuration.
func (h *TenantHandler) GetTenantRetrievalConfig(c *gin.Context) {
	ctx := c.Request.Context()
	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}
	data := tenant.RetrievalConfig
	if data == nil {
		data = &types.RetrievalConfig{}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    data,
	})
}

// updateTenantRetrievalConfigInternal updates the tenant's global retrieval configuration.
func (h *TenantHandler) updateTenantRetrievalConfigInternal(c *gin.Context) {
	ctx := c.Request.Context()

	var cfg types.RetrievalConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}

	// Validate thresholds
	if cfg.VectorThreshold < 0 || cfg.VectorThreshold > 1 {
		c.Error(errors.NewBadRequestError("vector_threshold must be between 0 and 1"))
		return
	}
	if cfg.KeywordThreshold < 0 || cfg.KeywordThreshold > 1 {
		c.Error(errors.NewBadRequestError("keyword_threshold must be between 0 and 1"))
		return
	}
	if cfg.RerankThreshold < -10 || cfg.RerankThreshold > 10 {
		c.Error(errors.NewBadRequestError("rerank_threshold must be between -10 and 10"))
		return
	}
	if cfg.EmbeddingTopK < 0 || cfg.EmbeddingTopK > 200 {
		c.Error(errors.NewBadRequestError("embedding_top_k must be between 0 and 200"))
		return
	}
	if cfg.RerankTopK < 0 || cfg.RerankTopK > 200 {
		c.Error(errors.NewBadRequestError("rerank_top_k must be between 0 and 200"))
		return
	}

	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}

	tenant.RetrievalConfig = &cfg
	updatedTenant, err := h.service.UpdateTenant(ctx, tenant)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to update retrieval config").WithDetails(err.Error()))
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    updatedTenant.RetrievalConfig,
		"message": "Retrieval configuration updated successfully",
	})
}
