package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/handler/dto"
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
	apiKeyService interfaces.TenantAPIKeyService
	userService   interfaces.UserService
	memberService interfaces.TenantMemberService
	kbService     interfaces.KnowledgeBaseService
	config        *config.Config
	// systemSettingSvc resolves runtime tenant policies and limits.
	// Reading goes DB > ENV >
	// in-code default, so a SystemAdmin's UI override applies on the
	// very next CreateTenant call.
	systemSettingSvc interfaces.SystemSettingService
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
	apiKeyService interfaces.TenantAPIKeyService,
	userService interfaces.UserService,
	memberService interfaces.TenantMemberService,
	kbService interfaces.KnowledgeBaseService,
	config *config.Config,
	systemSettingSvc interfaces.SystemSettingService,
) *TenantHandler {
	return &TenantHandler{
		service:          service,
		apiKeyService:    apiKeyService,
		userService:      userService,
		memberService:    memberService,
		kbService:        kbService,
		config:           config,
		systemSettingSvc: systemSettingSvc,
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
// — those go through dedicated endpoints (PUT /tenants/kv/:key, ...)
// that have their own validation.
//
// Pointers so we can distinguish "not sent" from "explicit empty
// string"; when nil we leave the existing column untouched.
type updateTenantRequest struct {
	Name        *string `json:"name"        binding:"omitempty,min=1,max=128"`
	Description *string `json:"description" binding:"omitempty,max=512"`
}

type apiPrincipalConfigRequest struct {
	Mode                  types.APIPrincipalMode `json:"mode"`
	DirectHeaderName      string                 `json:"direct_header_name"`
	SignedTokenHeaderName string                 `json:"signed_token_header_name"`
	RequireDirectHeader   bool                   `json:"require_direct_header"`
	HMACSecret            *string                `json:"hmac_secret"`
}

type apiPrincipalConfigResponse struct {
	Mode                  types.APIPrincipalMode `json:"mode"`
	DirectHeaderName      string                 `json:"direct_header_name"`
	SignedTokenHeaderName string                 `json:"signed_token_header_name"`
	RequireDirectHeader   bool                   `json:"require_direct_header"`
	// HasHMACSecret reports whether a signing secret is configured. The
	// plaintext secret is NEVER returned — clients only learn presence.
	HasHMACSecret bool `json:"has_hmac_secret"`
}

type apiPrincipalTestTokenRequest struct {
	ExternalUserID   string `json:"external_user_id"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
}

type apiPrincipalTestTokenResponse struct {
	Token            string `json:"token"`
	HeaderName       string `json:"header_name"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
	ExpiresAtUnix    int64  `json:"expires_at_unix"`
	ExternalUserID   string `json:"external_user_id"`
}

type tenantAPIKeyCreateRequest struct {
	Name             string   `json:"name"`
	FullAccess       bool     `json:"full_access"`
	KnowledgeBaseIDs []string `json:"knowledge_base_ids"`
	Capabilities     []string `json:"capabilities"`
	ExpiresAt        *int64   `json:"expires_at_unix"`
}

type tenantAPIKeyResponse struct {
	ID               uint64            `json:"id"`
	Name             string            `json:"name"`
	APIKey           string            `json:"api_key"`
	FullAccess       bool              `json:"full_access"`
	KnowledgeBaseIDs types.StringArray `json:"knowledge_base_ids"`
	Capabilities     types.StringArray `json:"capabilities"`
	LastUsedAt       *time.Time        `json:"last_used_at,omitempty"`
	ExpiresAt        *time.Time        `json:"expires_at,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
}

type tenantAPIKeyCreateResponse struct {
	tenantAPIKeyResponse
	Token string `json:"token"`
}

const (
	defaultAPIPrincipalDirectHeader  = "X-External-User-ID"
	defaultAPIPrincipalTokenHeader   = "X-External-User-Token"
	defaultAPIPrincipalTestTokenTTL  = 15 * time.Minute
	maxAPIPrincipalTestTokenTTL      = time.Hour
	maxAPIPrincipalExternalUserIDLen = 128
	// apiPrincipalSecretRedacted is the placeholder an update request may
	// send in place of the HMAC secret to signal "leave the stored secret
	// unchanged". The plaintext secret is never returned by GET, so the
	// client cannot echo the real value back.
	apiPrincipalSecretRedacted = "***"
)

// defaultMaxOwnedTenantsPerUser is the cap applied when
// config.Tenant.MaxOwnedPerUser is left at zero. Picked to comfortably
// cover legitimate "personal + a couple of side-projects" use while
// blunting drive-by abuse against POST /tenants (see CreateTenant).
const defaultMaxOwnedTenantsPerUser = 10

// resolveMaxOwnedTenantsPerUser returns the current cap, walking the
// 3-tier resolver: system_settings DB row > WEKNORA_TENANT_MAX_OWNED_PER_USER
// env > config.Tenant.MaxOwnedPerUser (yaml) > defaultMaxOwnedTenantsPerUser.
// We pre-compute the cfg-derived fallback so the SystemSettingService
// receives a single int64 default — its 3-tier resolver layers DB and
// env on top of that.
func (h *TenantHandler) resolveMaxOwnedTenantsPerUser(ctx context.Context) int {
	fallback := int64(defaultMaxOwnedTenantsPerUser)
	if h.config != nil && h.config.Tenant != nil && h.config.Tenant.MaxOwnedPerUser != 0 {
		fallback = int64(h.config.Tenant.MaxOwnedPerUser)
	}
	return int(h.systemSettingSvc.GetInt(
		ctx,
		"tenant.max_owned_per_user",
		"WEKNORA_TENANT_MAX_OWNED_PER_USER",
		fallback,
	))
}

// CreateTenant godoc
// @Summary      创建空间
// @Description  创建新的空间。任意已登录用户均可调用以建立自己的新工作区，
// @Description  调用方会被自动设为该空间的 Owner。跨空间超管仍可像以前一样
// @Description  通过本接口创建任意空间。
// @Description  当 tenant.auto_create_api_key（或 WEKNORA_TENANT_AUTO_CREATE_API_KEY）
// @Description  开启时，会自动创建一个 full_access API Key，并在响应体的 data.api_key 字段返回其明文 token。
// @Tags         空间管理
// @Accept       json
// @Produce      json
// @Param        request  body      handler.createTenantRequest  true  "空间信息"
// @Success      201      {object}  map[string]interface{}  "创建的空间（可选含 api_key）"
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

	// Deployment-level policy: ordinary users may be restricted to joining
	// existing workspaces by invitation. This check is authoritative; the
	// frontend capability only improves UX and cannot bypass it. Cross-tenant
	// superusers retain the catalog-management create path.
	if !caller.CanAccessAllTenants &&
		!resolveTenantSelfServiceCreationEnabled(ctx, h.config, h.systemSettingSvc) {
		logger.Warnf(ctx, "Self-service tenant creation denied by policy for user %s", caller.ID)
		c.Error(errors.NewTenantCreationDisabledError())
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
		// TenantService.CreateTenant (status="active", storage_quota
		// default, retriever engines from RETRIEVE_DRIVER). API keys are
		// created explicitly through the integration API-key list.
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
				c.Error(errors.NewInternalServerError("Failed to validate workspace quota").WithDetails(listErr.Error()))
				return
			}
			ownedCount := 0
			for _, m := range memberships {
				if m != nil && m.Role == types.TenantRoleOwner {
					ownedCount++
				}
			}
			cap := h.resolveMaxOwnedTenantsPerUser(ctx)
			if cap > 0 && ownedCount >= cap {
				logger.Warnf(ctx,
					"User %s reached self-service tenant quota (%d/%d)",
					caller.ID, ownedCount, cap,
				)
				c.Error(errors.NewTooManyRequestsError(
					"reached self-service workspace quota; contact an administrator to raise the limit",
				))
				return
			}
		}

		tenantData = types.Tenant{
			Name:        strings.TrimSpace(req.Name),
			Description: strings.TrimSpace(req.Description),
		}
	}

	// Apply the system-setting-driven default storage quota when the
	// caller didn't specify one (always true for self-serve; sometimes
	// true for the superuser branch when the JSON omits storage_quota).
	// We resolve at create time on purpose — the on-disk row should
	// carry an explicit value, so changing the setting later doesn't
	// silently shrink/grow established tenants. Negative values are
	// treated as "use default" so a misconfigured setting can't yield
	// a negative quota that the storage-used checks would interpret as
	// "unlimited" (StorageQuota <= 0 disables enforcement in
	// knowledge_create.go).
	if tenantData.StorageQuota <= 0 {
		gb := h.systemSettingSvc.GetInt(
			ctx,
			"tenant.default_storage_quota_gb",
			"WEKNORA_TENANT_DEFAULT_STORAGE_QUOTA_GB",
			10,
		)
		if gb <= 0 {
			gb = 10
		}
		tenantData.StorageQuota = gb * 1024 * 1024 * 1024
	}

	logger.Infof(ctx, "Creating tenant, name: %s", secutils.SanitizeForLog(tenantData.Name))

	createdTenant, err := h.service.CreateTenant(ctx, &tenantData)
	if err != nil {
		// Check if this is an application-specific error
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to create workspace: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to create workspace").WithDetails(err.Error()))
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
			c.Error(errors.NewInternalServerError("Failed to finalise workspace ownership").WithDetails(err.Error()))
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
				cap := h.resolveMaxOwnedTenantsPerUser(ctx)
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
						"reached self-service workspace quota; contact an administrator to raise the limit",
					))
					return
				}
			}
		}
	}

	// When a tenantless user creates their first workspace, make it their
	// default login tenant. Roll the just-created resources back if this
	// finalisation fails so the user is not left with an unreachable tenant.
	if caller.TenantID == 0 {
		caller.TenantID = createdTenant.ID
		if err := h.userService.UpdateUser(ctx, caller); err != nil {
			logger.Errorf(ctx, "Failed to set first tenant %d as default for user %s: %v",
				createdTenant.ID, caller.ID, err)
			if h.memberService != nil {
				_ = h.memberService.RemoveMember(ctx, caller.ID, createdTenant.ID)
			}
			_ = h.service.DeleteTenant(ctx, createdTenant.ID)
			c.Error(errors.NewInternalServerError("Failed to finalise default workspace").WithDetails(err.Error()))
			return
		}
	}

	logger.Infof(
		ctx,
		"Tenant created successfully, ID: %d, name: %s",
		createdTenant.ID,
		secutils.SanitizeForLog(createdTenant.Name),
	)

	// data carries the created tenant. When the legacy auto-create-key
	// behaviour is enabled we embed the plaintext token as data.api_key so
	// the response shape mirrors the pre-break-change behaviour integrations
	// relied on.
	var data any = createdTenant

	// Optional legacy compatibility: mint a full-access API key on tenant
	// creation and return its plaintext token, gated by the
	// tenant.auto_create_api_key setting (env WEKNORA_TENANT_AUTO_CREATE_API_KEY).
	// Default off — modern deployments create keys explicitly via
	// tenant_api_keys. Failing to create the convenience key must NOT fail
	// the whole tenant creation (the tenant is fully usable without a key);
	// we log a warning and return the tenant as usual.
	if h.autoCreateTenantAPIKey(ctx) && h.apiKeyService != nil {
		result, keyErr := h.apiKeyService.CreateAPIKey(ctx, interfaces.TenantAPIKeyCreateRequest{
			TenantID:   createdTenant.ID,
			Name:       "default",
			FullAccess: true,
		})
		if keyErr != nil {
			logger.Errorf(ctx,
				"Auto-create default API key failed for tenant %d: %v — returning tenant without key",
				createdTenant.ID, keyErr)
		} else if merged, mErr := tenantWithAPIKey(createdTenant, result.Token); mErr != nil {
			// Round-trip failure is unexpected; degrade gracefully by
			// returning the tenant without embedding the key rather than
			// failing the whole request.
			logger.Errorf(ctx, "Failed to embed api_key into tenant response for tenant %d: %v",
				createdTenant.ID, mErr)
		} else {
			data = merged
		}
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    data,
	})
}

// tenantWithAPIKey returns the tenant serialized as a map with an extra
// api_key field, so the create response can embed the plaintext token inside
// data (mirroring the pre-break-change shape) without adding a persisted
// api_key column back onto types.Tenant.
func tenantWithAPIKey(tenant *types.Tenant, token string) (map[string]any, error) {
	raw, err := json.Marshal(tenant)
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	m["api_key"] = token
	return m, nil
}

// autoCreateTenantAPIKey resolves whether tenant creation should also mint a
// full-access API key (legacy compatibility). 3-tier resolver:
// system_settings DB row > WEKNORA_TENANT_AUTO_CREATE_API_KEY env > false.
func (h *TenantHandler) autoCreateTenantAPIKey(ctx context.Context) bool {
	if h.systemSettingSvc == nil {
		return false
	}
	return h.systemSettingSvc.GetBool(
		ctx,
		"tenant.auto_create_api_key",
		"WEKNORA_TENANT_AUTO_CREATE_API_KEY",
		false,
	)
}

// GetTenant godoc
// @Summary      获取空间详情
// @Description  根据ID获取空间详情
// @Tags         空间管理
// @Accept       json
// @Produce      json
// @Param        id   path      int  true  "空间ID"
// @Success      200  {object}  map[string]interface{}  "空间详情"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      404  {object}  errors.AppError         "空间不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /tenants/{id} [get]
func (h *TenantHandler) GetTenant(c *gin.Context) {
	ctx := c.Request.Context()

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		logger.Errorf(ctx, "Invalid workspace ID: %s", secutils.SanitizeForLog(c.Param("id")))
		c.Error(errors.NewBadRequestError("Invalid workspace ID"))
		return
	}

	tenant, err := h.service.GetTenantByID(ctx, id)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to retrieve workspace: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to retrieve workspace").WithDetails(err.Error()))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    dto.NewTenantResponse(ctx, tenant),
	})
}

// UpdateTenant godoc
// @Summary      更新空间
// @Description  更新空间信息
// @Tags         空间管理
// @Accept       json
// @Produce      json
// @Param        id       path      int           true  "空间ID"
// @Param        request  body      types.Tenant  true  "空间信息"
// @Success      200      {object}  map[string]interface{}  "更新后的空间"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Router       /tenants/{id} [put]
func (h *TenantHandler) UpdateTenant(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start updating tenant")

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		logger.Errorf(ctx, "Invalid workspace ID: %s", secutils.SanitizeForLog(c.Param("id")))
		c.Error(errors.NewBadRequestError("Invalid workspace ID"))
		return
	}

	// Strict whitelist: only Name / Description are mutable through the
	// public PUT. Storage quota, status, business, configs, api_key and
	// every other privileged column live behind dedicated endpoints
	// (PUT /tenants/kv/:key, ...). Without this, an
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
			c.Error(errors.NewInternalServerError("Failed to load workspace").WithDetails(err.Error()))
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
			logger.Error(ctx, "Failed to update workspace: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to update workspace").WithDetails(err.Error()))
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
		"data":    dto.NewTenantResponse(ctx, updatedTenant),
	})
}

func (h *TenantHandler) ListAPIKeys(c *gin.Context) {
	ctx := c.Request.Context()
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.Error(errors.NewBadRequestError("Invalid workspace ID"))
		return
	}
	keys, err := h.apiKeyService.ListAPIKeys(ctx, id)
	if err != nil {
		c.Error(errors.NewInternalServerError("Failed to list API keys").WithDetails(err.Error()))
		return
	}
	resp := make([]tenantAPIKeyResponse, 0, len(keys))
	for _, key := range keys {
		resp = append(resp, tenantAPIKeyForResponse(key))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": resp})
}

func (h *TenantHandler) CreateAPIKey(c *gin.Context) {
	ctx := c.Request.Context()
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.Error(errors.NewBadRequestError("Invalid workspace ID"))
		return
	}
	var req tenantAPIKeyCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	if err := validateTenantAPIKeyRequest(ctx, h.kbService, id, req); err != nil {
		c.Error(err)
		return
	}
	var expiresAt *time.Time
	if req.ExpiresAt != nil {
		t := time.Unix(*req.ExpiresAt, 0)
		if !t.After(time.Now()) {
			c.Error(errors.NewValidationError("expires_at_unix must be in the future"))
			return
		}
		expiresAt = &t
	}
	result, err := h.apiKeyService.CreateAPIKey(ctx, interfaces.TenantAPIKeyCreateRequest{
		TenantID:         id,
		Name:             req.Name,
		FullAccess:       req.FullAccess,
		KnowledgeBaseIDs: req.KnowledgeBaseIDs,
		Capabilities:     req.Capabilities,
		ExpiresAt:        expiresAt,
	})
	if err != nil {
		c.Error(errors.NewInternalServerError("Failed to create API key").WithDetails(err.Error()))
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data": tenantAPIKeyCreateResponse{
			tenantAPIKeyResponse: tenantAPIKeyForResponse(result.APIKey),
			Token:                result.Token,
		},
	})
}

func (h *TenantHandler) DeleteAPIKey(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.Error(errors.NewBadRequestError("Invalid workspace ID"))
		return
	}
	keyID, err := strconv.ParseUint(c.Param("key_id"), 10, 64)
	if err != nil || keyID == 0 {
		c.Error(errors.NewBadRequestError("Invalid API key ID"))
		return
	}
	if err := h.apiKeyService.RevokeAPIKey(ctx, tenantID, keyID); err != nil {
		c.Error(errors.NewNotFoundError("API key not found"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func tenantAPIKeyForResponse(key *types.TenantAPIKey) tenantAPIKeyResponse {
	if key == nil {
		return tenantAPIKeyResponse{}
	}
	return tenantAPIKeyResponse{
		ID:               key.ID,
		Name:             key.Name,
		APIKey:           key.APIKey,
		FullAccess:       key.FullAccess,
		KnowledgeBaseIDs: key.KnowledgeBaseIDs,
		Capabilities:     types.NormalizeAPIKeyCapabilities(key.Capabilities),
		LastUsedAt:       key.LastUsedAt,
		ExpiresAt:        key.ExpiresAt,
		CreatedAt:        key.CreatedAt,
	}
}

func validateTenantAPIKeyRequest(
	ctx context.Context,
	kbService interfaces.KnowledgeBaseService,
	tenantID uint64,
	req tenantAPIKeyCreateRequest,
) *errors.AppError {
	if strings.TrimSpace(req.Name) == "" {
		return errors.NewValidationError("name is required")
	}
	if req.FullAccess {
		return nil
	}
	caps := types.NormalizeAPIKeyCapabilities(types.StringArray(req.Capabilities))
	if len(caps) == 0 {
		return errors.NewValidationError("capabilities are required for scoped API keys")
	}
	for _, cap := range req.Capabilities {
		if strings.TrimSpace(cap) == "" {
			continue
		}
		if types.NormalizeAPIKeyCapability(types.APIKeyCapability(cap)) == "" {
			return errors.NewValidationError("capabilities contains an unknown capability")
		}
	}
	for _, kbID := range req.KnowledgeBaseIDs {
		kbID = strings.TrimSpace(kbID)
		if kbID == "" {
			continue
		}
		kb, err := kbService.GetKnowledgeBaseByID(ctx, kbID)
		if err != nil || kb == nil {
			return errors.NewValidationError("knowledge_base_ids contains an unknown knowledge base")
		}
		if kb.TenantID != tenantID {
			return errors.NewForbiddenError("knowledge_base_ids contains a knowledge base outside this workspace")
		}
	}
	return nil
}

func apiPrincipalConfigForResponse(cfg *types.APIPrincipalConfig) apiPrincipalConfigResponse {
	if cfg == nil {
		cfg = &types.APIPrincipalConfig{}
	}
	mode := cfg.Mode
	if mode == "" {
		mode = types.APIPrincipalModeTenant
	}
	return apiPrincipalConfigResponse{
		Mode:                  mode,
		DirectHeaderName:      defaultAPIPrincipalDirectHeader,
		SignedTokenHeaderName: defaultAPIPrincipalTokenHeader,
		RequireDirectHeader:   cfg.RequireDirectHeader,
		HasHMACSecret:         strings.TrimSpace(cfg.HMACSecret) != "",
	}
}

// GetAPIPrincipalConfig godoc
// @Summary      获取空间 API Key 用户身份配置
// @Description  返回 X-API-Key 请求如何映射为终端 Principal 的配置（Owner）
// @Tags         空间管理
// @Accept       json
// @Produce      json
// @Param        id   path      int  true  "空间ID"
// @Success      200  {object}  map[string]interface{}  "API principal 配置"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      403  {object}  errors.AppError         "权限不足"
// @Security     Bearer
// @Router       /tenants/{id}/api-principal-config [get]
func (h *TenantHandler) GetAPIPrincipalConfig(c *gin.Context) {
	ctx := c.Request.Context()
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.Error(errors.NewBadRequestError("Invalid workspace ID"))
		return
	}
	tenant, err := h.service.GetTenantByID(ctx, id)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
		} else {
			c.Error(errors.NewInternalServerError("Failed to load workspace").WithDetails(err.Error()))
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    apiPrincipalConfigForResponse(tenant.APIPrincipalConfig),
	})
}

// UpdateAPIPrincipalConfig godoc
// @Summary      更新空间 API Key 用户身份配置
// @Description  配置 X-API-Key 请求如何映射为终端 Principal（Owner）
// @Tags         空间管理
// @Accept       json
// @Produce      json
// @Param        id       path      int                           true  "空间ID"
// @Param        request  body      handler.apiPrincipalConfigRequest  true  "API principal 配置"
// @Success      200      {object}  map[string]interface{}        "更新后的配置"
// @Failure      400      {object}  errors.AppError               "请求参数错误"
// @Failure      403      {object}  errors.AppError               "权限不足"
// @Security     Bearer
// @Router       /tenants/{id}/api-principal-config [put]
func (h *TenantHandler) UpdateAPIPrincipalConfig(c *gin.Context) {
	ctx := c.Request.Context()
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.Error(errors.NewBadRequestError("Invalid workspace ID"))
		return
	}
	var req apiPrincipalConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	if req.Mode == "" {
		req.Mode = types.APIPrincipalModeTenant
	}
	switch req.Mode {
	case types.APIPrincipalModeTenant, types.APIPrincipalModeDirect, types.APIPrincipalModeSignedToken:
	default:
		c.Error(errors.NewValidationError("mode must be tenant, direct_header, or signed_token"))
		return
	}

	tenant, err := h.service.GetTenantByID(ctx, id)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
		} else {
			c.Error(errors.NewInternalServerError("Failed to load workspace").WithDetails(err.Error()))
		}
		return
	}

	existingSecret := ""
	if tenant.APIPrincipalConfig != nil {
		existingSecret = tenant.APIPrincipalConfig.HMACSecret
	}
	hmacSecret := existingSecret
	if req.HMACSecret != nil {
		provided := strings.TrimSpace(*req.HMACSecret)
		// GET no longer discloses the plaintext secret, so a client that
		// edits the config re-submits the redaction placeholder to mean
		// "keep the existing secret". Treat it as a no-op instead of
		// overwriting the real secret with "***".
		if provided != apiPrincipalSecretRedacted {
			hmacSecret = provided
		}
	}
	cfg := &types.APIPrincipalConfig{
		Mode:                  req.Mode,
		DirectHeaderName:      defaultAPIPrincipalDirectHeader,
		SignedTokenHeaderName: defaultAPIPrincipalTokenHeader,
		RequireDirectHeader:   req.RequireDirectHeader,
		HMACSecret:            hmacSecret,
	}
	if cfg.Mode == types.APIPrincipalModeSignedToken && strings.TrimSpace(cfg.HMACSecret) == "" {
		c.Error(errors.NewValidationError("hmac_secret is required for signed_token mode"))
		return
	}
	tenant.APIPrincipalConfig = cfg

	updatedTenant, err := h.service.UpdateTenant(ctx, tenant)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
		} else {
			c.Error(errors.NewInternalServerError("Failed to update API principal config").WithDetails(err.Error()))
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    apiPrincipalConfigForResponse(updatedTenant.APIPrincipalConfig),
	})
}

// CreateAPIPrincipalTestToken godoc
// @Summary      生成 API Playground 测试 JWT
// @Description  使用空间已保存的 HMAC 密钥签发短期外部用户 JWT（Owner）
// @Tags         空间管理
// @Accept       json
// @Produce      json
// @Param        id       path      int                                  true  "空间ID"
// @Param        request  body      handler.apiPrincipalTestTokenRequest true  "测试 Token 参数"
// @Success      200      {object}  map[string]interface{}               "短期 JWT"
// @Failure      400      {object}  errors.AppError                      "请求参数错误"
// @Failure      403      {object}  errors.AppError                      "权限不足"
// @Security     Bearer
// @Router       /tenants/{id}/api-principal-test-token [post]
func (h *TenantHandler) CreateAPIPrincipalTestToken(c *gin.Context) {
	ctx := c.Request.Context()
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.Error(errors.NewBadRequestError("Invalid workspace ID"))
		return
	}

	var req apiPrincipalTestTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}

	externalUserID := strings.TrimSpace(req.ExternalUserID)
	if err := validateAPIPrincipalExternalUserID(externalUserID); err != nil {
		c.Error(errors.NewValidationError("external_user_id is invalid").WithDetails(err.Error()))
		return
	}

	tenant, err := h.service.GetTenantByID(ctx, id)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
		} else {
			c.Error(errors.NewInternalServerError("Failed to load workspace").WithDetails(err.Error()))
		}
		return
	}

	cfg := tenant.APIPrincipalConfig
	if cfg == nil || cfg.Mode != types.APIPrincipalModeSignedToken {
		c.Error(errors.NewValidationError("signed_token mode is required"))
		return
	}
	secret := strings.TrimSpace(cfg.HMACSecret)
	if secret == "" {
		c.Error(errors.NewValidationError("hmac_secret is required for signed_token mode"))
		return
	}

	ttl := defaultAPIPrincipalTestTokenTTL
	if req.ExpiresInSeconds > 0 {
		ttl = time.Duration(req.ExpiresInSeconds) * time.Second
	}
	if ttl <= 0 || ttl > maxAPIPrincipalTestTokenTTL {
		c.Error(errors.NewValidationError("expires_in_seconds must be between 1 and 3600"))
		return
	}

	now := time.Now()
	expiresAt := now.Add(ttl)
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":       externalUserID,
		"tenant_id": strconv.FormatUint(id, 10),
		"aud":       "weknora",
		"iat":       now.Unix(),
		"exp":       expiresAt.Unix(),
	}).SignedString([]byte(secret))
	if err != nil {
		c.Error(errors.NewInternalServerError("Failed to create API principal test token").WithDetails(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": apiPrincipalTestTokenResponse{
			Token:            token,
			HeaderName:       defaultAPIPrincipalTokenHeader,
			ExpiresInSeconds: int(ttl.Seconds()),
			ExpiresAtUnix:    expiresAt.Unix(),
			ExternalUserID:   externalUserID,
		},
	})
}

func validateAPIPrincipalExternalUserID(id string) error {
	if id == "" {
		return errors.NewValidationError("external_user_id is required")
	}
	if len(id) > maxAPIPrincipalExternalUserIDLen {
		return errors.NewValidationError("external_user_id is too long")
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return errors.NewValidationError("external_user_id contains invalid characters")
		}
	}
	return nil
}

// DeleteTenant godoc
// @Summary      删除空间
// @Description  删除指定的空间
// @Tags         空间管理
// @Accept       json
// @Produce      json
// @Param        id   path      int  true  "空间ID"
// @Success      200  {object}  map[string]interface{}  "删除成功"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Router       /tenants/{id} [delete]
func (h *TenantHandler) DeleteTenant(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start deleting tenant")

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		logger.Errorf(ctx, "Invalid workspace ID: %s", secutils.SanitizeForLog(c.Param("id")))
		c.Error(errors.NewBadRequestError("Invalid workspace ID"))
		return
	}

	logger.Infof(ctx, "Deleting tenant, ID: %d", id)

	if err := h.service.DeleteTenant(ctx, id); err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to delete workspace: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to delete workspace").WithDetails(err.Error()))
		}
		return
	}

	logger.Infof(ctx, "Workspace deleted successfully, ID: %d", id)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Workspace deleted successfully",
	})
}

// ListTenants godoc
// @Summary      获取空间列表
// @Description  获取当前用户可访问的空间列表
// @Tags         空间管理
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "空间列表"
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
			"items": []*dto.TenantResponse{dto.NewTenantResponse(ctx, tenant)},
		},
	})
}

// ListAllTenants godoc
// @Summary      获取所有空间列表
// @Description  获取系统中所有空间（需要跨空间访问权限）
// @Tags         空间管理
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "所有空间列表"
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
			logger.Error(ctx, "Failed to retrieve all workspaces list: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to retrieve all workspaces list").WithDetails(err.Error()))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items": dto.NewTenantResponsesCrossTenant(tenants),
		},
	})
}

// SearchTenants godoc
// @Summary      搜索空间
// @Description  分页搜索空间（需要跨空间访问权限）
// @Tags         空间管理
// @Accept       json
// @Produce      json
// @Param        keyword    query     string  false  "搜索关键词"
// @Param        tenant_id  query     int     false  "空间ID筛选"
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
			logger.Error(ctx, "Failed to search workspaces: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to search workspaces").WithDetails(err.Error()))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items":     dto.NewTenantResponsesCrossTenant(tenants),
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// GetTenantKV godoc
// @Summary      获取空间KV配置
// @Description  获取空间级别的KV配置（支持web-search-config、prompt-templates、parser-engine-config、storage-engine-config、chat-history-config、retrieval-config）
// @Tags         空间管理
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
	case "web-search-config", "parser-engine-config", "storage-engine-config":
		if !dto.CanViewIntegrationSecrets(ctx) {
			c.Error(errors.NewForbiddenError("integration configuration requires admin access"))
			return
		}
	}

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
// @Summary      更新空间KV配置
// @Description  更新空间级别的KV配置（支持web-search-config、parser-engine-config、storage-engine-config、chat-history-config、retrieval-config）
// @Tags         空间管理
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
	case "web-search-config", "parser-engine-config", "storage-engine-config":
		if !dto.CanViewIntegrationSecrets(ctx) {
			c.Error(errors.NewForbiddenError("integration configuration requires admin access"))
			return
		}
	}

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

	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Workspace is empty")
		c.Error(errors.NewBadRequestError("Workspace is empty"))
		return
	}

	cfg = *types.MergeWebSearchConfigForUpdate(&cfg, tenant.WebSearchConfig)

	// Validate configuration
	if cfg.MaxResults < 1 || cfg.MaxResults > 50 {
		c.Error(errors.NewBadRequestError("max_results must be between 1 and 50"))
		return
	}

	tenant.WebSearchConfig = &cfg
	updatedTenant, err := h.service.UpdateTenant(ctx, tenant)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to update workspace: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to update workspace web search config").WithDetails(err.Error()))
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    types.WebSearchConfigForResponse(updatedTenant.WebSearchConfig, true),
		"message": "Web search configuration updated successfully",
	})
}

// GetTenantWebSearchConfig godoc
// @Summary      获取空间网络搜索配置
// @Description  获取空间的网络搜索配置
// @Tags         空间管理
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
		logger.Error(ctx, "Workspace is empty")
		c.Error(errors.NewBadRequestError("Workspace is empty"))
		return
	}

	logger.Infof(ctx, "Tenant web search config retrieved successfully, Tenant ID: %d", tenant.ID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    types.WebSearchConfigForResponse(tenant.WebSearchConfig, true),
	})
}

// GetTenantParserEngineConfig returns the tenant's parser engine config (MinerU endpoint, API key, etc.).
func (h *TenantHandler) GetTenantParserEngineConfig(c *gin.Context) {
	ctx := c.Request.Context()
	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Workspace is empty")
		c.Error(errors.NewBadRequestError("Workspace is empty"))
		return
	}
	data := types.ParserEngineConfigForResponse(tenant.ParserEngineConfig, true)
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
		logger.Error(ctx, "Workspace is empty")
		c.Error(errors.NewBadRequestError("Workspace is empty"))
		return
	}
	merged := types.MergeParserEngineConfigForUpdate(&cfg, tenant.ParserEngineConfig)
	if err := validateParserEngineOutboundURLs(merged); err != nil {
		c.Error(errors.NewValidationError(err.Error()))
		return
	}
	tenant.ParserEngineConfig = merged
	updatedTenant, err := h.service.UpdateTenant(ctx, tenant)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to update workspace parser engine config").WithDetails(err.Error()))
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    types.ParserEngineConfigForResponse(updatedTenant.ParserEngineConfig, true),
		"message": "解析引擎配置已更新",
	})
}

// GetTenantStorageEngineConfig returns the tenant's storage engine config (Local, MinIO, COS parameters).
func (h *TenantHandler) GetTenantStorageEngineConfig(c *gin.Context) {
	ctx := c.Request.Context()
	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Workspace is empty")
		c.Error(errors.NewBadRequestError("Workspace is empty"))
		return
	}
	data := types.StorageEngineConfigForResponse(tenant.StorageEngineConfig, true)
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
		logger.Error(ctx, "Workspace is empty")
		c.Error(errors.NewBadRequestError("Workspace is empty"))
		return
	}
	merged := types.MergeStorageEngineConfigForUpdate(&cfg, tenant.StorageEngineConfig)
	tenant.StorageEngineConfig = merged
	updatedTenant, err := h.service.UpdateTenant(ctx, tenant)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to update workspace storage engine config").WithDetails(err.Error()))
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    types.StorageEngineConfigForResponse(updatedTenant.StorageEngineConfig, true),
		"message": "存储引擎配置已更新",
	})
}

// GetPromptTemplates godoc
// @Summary      获取提示词模板
// @Description  获取系统配置的提示词模板列表
// @Tags         空间管理
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
		logger.Error(ctx, "Workspace is empty")
		c.Error(errors.NewBadRequestError("Workspace is empty"))
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
		logger.Error(ctx, "Workspace is empty")
		c.Error(errors.NewBadRequestError("Workspace is empty"))
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
		logger.Error(ctx, "Workspace is empty")
		c.Error(errors.NewBadRequestError("Workspace is empty"))
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
		logger.Error(ctx, "Workspace is empty")
		c.Error(errors.NewBadRequestError("Workspace is empty"))
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

func validateParserEngineOutboundURLs(cfg *types.ParserEngineConfig) error {
	if cfg == nil {
		return nil
	}
	if endpoint := strings.TrimSpace(cfg.MinerUEndpoint); endpoint != "" {
		if err := secutils.ValidateURLForSSRF(endpoint); err != nil {
			return fmt.Errorf("mineru_endpoint failed SSRF validation: %v", err)
		}
	}
	if vlmURL := strings.TrimSpace(cfg.MinerUVLMServerURL); vlmURL != "" {
		if err := secutils.ValidateURLForSSRF(vlmURL); err != nil {
			return fmt.Errorf("mineru_vlm_server_url failed SSRF validation: %v", err)
		}
	}
	return nil
}
