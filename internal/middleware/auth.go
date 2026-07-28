package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const (
	defaultExternalUserIDHeader    = "X-External-User-ID"
	defaultExternalUserTokenHeader = "X-External-User-Token"
	maxExternalUserIDLen           = 128
	maxExternalUserTokenTTL        = 24 * time.Hour
)

var (
	errMissingDirectHeader      = errors.New("missing external user id header")
	errInvalidExternalUserID    = errors.New("invalid external user id")
	errInvalidExternalUserToken = errors.New("invalid external user token")
)

// 无需认证的API列表
var noAuthAPI = map[string][]string{
	"/health":                 {"GET"},
	"/api/v1/auth/register":   {"POST"},
	"/api/v1/auth/login":      {"POST"},
	"/api/v1/auth/auto-setup": {"POST"},
	// Share-link surfaces accept a plaintext invite token from anonymous
	// callers (an invitee who hasn't registered yet). They are registered
	// as public routes in RegisterAuthRoutes and rate-limited by IP, so the
	// global Auth middleware must let them through — otherwise opening a
	// share link while logged out 401s and the frontend bounces the user to
	// /login instead of the register page (issue #1617).
	"/api/v1/auth/invitations/lookup": {"POST"},
	"/api/v1/auth/register-by-invite": {"POST"},
	"/api/v1/auth/config":             {"GET"},
	"/api/v1/auth/oidc/config":        {"GET"},
	"/api/v1/auth/oidc/url":           {"GET"},
	"/api/v1/auth/oidc/callback":      {"GET"},
	// MCP OAuth provider redirect: the third-party authorization server
	// redirects the browser here without a WeKnora bearer token. The request
	// is authenticated by the opaque, single-use `state` parameter instead.
	"/api/v1/mcp-oauth/callback": {"GET"},
	"/api/v1/auth/refresh":       {"POST"},
	// IM platforms (Feishu, Slack, etc.) commonly issue a HEAD request
	// before GET to validate Content-Type / Content-Length when rendering
	// image previews — both verbs must be allowed for image links to work.
	"/api/v1/files/presigned": {"GET", "HEAD"},
}

// 检查请求是否在无需认证的API列表中
func isNoAuthAPI(path string, method string) bool {
	for api, methods := range noAuthAPI {
		// 如果以*结尾，按照前缀匹配，否则按照全路径匹配
		if strings.HasSuffix(api, "*") {
			if strings.HasPrefix(path, strings.TrimSuffix(api, "*")) && slices.Contains(methods, method) {
				return true
			}
		} else if path == api && slices.Contains(methods, method) {
			return true
		}
	}
	return false
}

// isTenantOptionalAPI lists authenticated identity-level operations that are
// meaningful before a user belongs to any tenant. Every other authenticated
// route remains tenant-scoped and returns TENANT_REQUIRED when the JWT and
// request headers do not resolve a tenant.
func isTenantOptionalAPI(path, method string) bool {
	switch {
	case path == "/api/v1/auth/me" && (method == http.MethodGet || method == http.MethodPut):
		return true
	case path == "/api/v1/auth/me/preferences" && method == http.MethodPut:
		return true
	case path == "/api/v1/auth/logout" && method == http.MethodPost:
		return true
	case path == "/api/v1/auth/change-password" && method == http.MethodPost:
		return true
	case path == "/api/v1/auth/validate" && method == http.MethodGet:
		return true
	case path == "/api/v1/auth/switch-tenant" && method == http.MethodPost:
		return true
	case path == "/api/v1/tenants" && method == http.MethodPost:
		return true
	case strings.HasPrefix(path, "/api/v1/me/invitations"):
		return true
	default:
		return false
	}
}

func attachTenantlessUserContext(c *gin.Context, user *types.User) {
	applyAuthSession(c, authSession{
		User:        user,
		Principal:   types.Principal{Type: types.PrincipalWebUser, ID: user.ID},
		SystemAdmin: user.IsSystemAdmin,
	})
}

// Auth 认证中间件。按顺序尝试三条通道：
//
//  1. 白名单（isNoAuthAPI）/ OPTIONS 预检 —— 直接放行；
//  2. Bearer JWT —— 成功则走 authenticateJWTUser 完成空间/角色解析；
//     校验失败不立即拒绝，继续尝试 X-API-Key（保持既有兼容行为：
//     携带过期 JWT 但同时带有效 API key 的客户端仍可通过）；
//  3. X-API-Key —— authenticateAPIKeyRequest。
//
// 三条通道都未命中时返回 401；若调用方提交过 Bearer token，错误消息
// 明确指出 token 无效而不是笼统的 "missing authentication"，方便客户端
// 区分「没登录」和「登录态过期」。
func Auth(
	tenantService interfaces.TenantService,
	userService interfaces.UserService,
	memberService interfaces.TenantMemberService,
	apiKeyService interfaces.TenantAPIKeyService,
	cfg *config.Config,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		// ignore OPTIONS request
		if c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}

		// 检查请求是否在无需认证的API列表中
		if isNoAuthAPI(c.Request.URL.Path, c.Request.Method) {
			c.Next()
			return
		}

		// 尝试JWT Token认证
		bearerPresented := false
		if token, ok := bearerToken(c); ok {
			bearerPresented = true
			user, jwtTenantID, err := userService.ValidateToken(c.Request.Context(), token)
			if err == nil && user != nil {
				if authenticateJWTUser(c, tenantService, memberService, cfg, user, jwtTenantID) {
					c.Next()
				}
				return
			}
			logger.Warnf(c.Request.Context(), "[auth] bearer token rejected: %v", err)
		}

		// 尝试X-API-Key认证（兼容模式）
		if apiKey := c.GetHeader("X-API-Key"); apiKey != "" {
			if apiKeyService == nil {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: API key service is not configured"})
				c.Abort()
				return
			}
			if authenticateAPIKeyRequest(c, tenantService, userService, apiKeyService, apiKey) {
				c.Next()
			}
			return
		}

		// 没有任何通道认证成功
		if bearerPresented {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: invalid or expired token"})
		} else {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: missing authentication"})
		}
		c.Abort()
	}
}

// bearerToken extracts the Bearer token from the Authorization header.
func bearerToken(c *gin.Context) (string, bool) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
		return "", false
	}
	return strings.TrimPrefix(authHeader, "Bearer "), true
}

// authenticateJWTUser finishes authentication for a validated JWT user:
// it resolves the target tenant (X-Tenant-ID switch / JWT claim / first
// active membership), resolves the caller's role inside that tenant, and
// attaches the session context. Returns true when the request may proceed;
// on false the response has already been written and the request aborted.
func authenticateJWTUser(
	c *gin.Context,
	tenantService interfaces.TenantService,
	memberService interfaces.TenantMemberService,
	cfg *config.Config,
	user *types.User,
	jwtTenantID uint64,
) bool {
	ctx := c.Request.Context()

	targetTenantID, tenant, crossTenantSwitch, ok := resolveTargetTenant(c, tenantService, memberService, cfg, user, jwtTenantID)
	if !ok {
		return false
	}

	if targetTenantID == 0 {
		// 无可用空间：身份级路由（/auth/me 等）放行为 tenantless 会话，
		// 其余路由返回 TENANT_REQUIRED 让前端引导用户创建/加入空间。
		if isTenantOptionalAPI(c.Request.URL.Path, c.Request.Method) {
			attachTenantlessUserContext(c, user)
			return true
		}
		c.JSON(http.StatusConflict, gin.H{
			"error": "Workspace required",
			"code":  "TENANT_REQUIRED",
		})
		c.Abort()
		return false
	}

	// 获取空间信息（X-Tenant-ID 切换路径已在 resolveTargetTenant 内取到，
	// 避免二次查库）。
	if tenant == nil {
		var err error
		tenant, err = tenantService.GetTenantByID(ctx, targetTenantID)
		if err != nil || tenant == nil {
			logger.Warnf(ctx, "[auth] tenant lookup failed: tenant=%d user=%s err=%v", targetTenantID, user.ID, err)
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Unauthorized: invalid workspace",
			})
			c.Abort()
			return false
		}
	}

	// 解析当前空间内的角色 (issue #1303)
	role, ok := resolveTenantRole(ctx, memberService, user, targetTenantID, crossTenantSwitch, cfg)
	if !ok {
		// 强制 RBAC 时，缺少 active membership 即拒绝；fail-open 路径已在
		// resolveTenantRole 内部处理。
		logger.Warnf(ctx, "User %s has no active membership in tenant %d", user.ID, targetTenantID)
		c.JSON(http.StatusForbidden, gin.H{
			"error": "Forbidden: not a member of the target workspace",
		})
		c.Abort()
		return false
	}

	logger.Infof(ctx,
		"[auth] resolved role=%s for user=%s in tenant=%d (jwt_tenant=%d, header=%q, cross_switch=%v)",
		role, user.ID, targetTenantID, jwtTenantID, c.GetHeader("X-Tenant-ID"), crossTenantSwitch)
	applyAuthSession(c, authSession{
		User:        user,
		Principal:   types.Principal{Type: types.PrincipalWebUser, ID: user.ID},
		TenantID:    targetTenantID,
		Tenant:      tenant,
		Role:        role,
		SystemAdmin: user.IsSystemAdmin,
	})
	return true
}

// resolveTargetTenant decides which tenant this request operates in.
//
// Priority:
//  1. X-Tenant-ID header — must parse to a positive integer, the user must
//     be allowed to access it (home tenant / cross-tenant superuser / active
//     membership, see IsTenantAccessible) and the tenant must exist. The
//     fetched tenant is returned so the caller doesn't refetch it.
//  2. JWT tenant claim (falling back to user.TenantID when the claim is 0).
//  3. First active membership — lets a tenantless session become usable as
//     soon as an invitation is accepted (see resolveFirstMembershipTarget).
//
// Returns ok=false when the response has already been written (malformed
// header, inaccessible or missing target tenant). targetTenantID == 0 with
// ok=true means "authenticated but no usable workspace" — the caller decides
// between tenantless routes and TENANT_REQUIRED.
func resolveTargetTenant(
	c *gin.Context,
	tenantService interfaces.TenantService,
	memberService interfaces.TenantMemberService,
	cfg *config.Config,
	user *types.User,
	jwtTenantID uint64,
) (targetTenantID uint64, tenant *types.Tenant, crossTenantSwitch bool, ok bool) {
	ctx := c.Request.Context()

	// 默认 target = JWT 里的 tenant_id（来自登录或 /auth/switch-tenant），
	// 兼容 ValidateToken 的 fallback：claim 缺失时 jwtTenantID == user.TenantID。
	targetTenantID = jwtTenantID
	if targetTenantID == 0 {
		targetTenantID = user.TenantID
	}

	if tenantHeader := c.GetHeader("X-Tenant-ID"); tenantHeader != "" {
		// 解析目标空间ID。畸形 / 零值必须显式拒绝：静默忽略会让坏掉的
		// 前端/SDK 悄悄写错空间，反而看不到问题。与 RequirePathTenantMatch
		// 中对 :id 的校验保持一致（非空、可解析、>0）。
		parsedTenantID, err := strconv.ParseUint(tenantHeader, 10, 64)
		if err != nil || parsedTenantID == 0 {
			logger.Warnf(ctx, "Invalid X-Tenant-ID header from user=%s: %q (err=%v)", user.ID, tenantHeader, err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid X-Tenant-ID header"})
			c.Abort()
			return 0, nil, false, false
		}
		// 检查用户是否有权限访问目标空间：自家空间、跨空间超管、或
		// 有 active membership 行——三选一，由 IsTenantAccessible 统一判定。
		if !IsTenantAccessible(ctx, user, parsedTenantID, memberService, cfg) {
			logger.Warnf(ctx, "User %s attempted to access tenant %d without permission", user.ID, parsedTenantID)
			c.JSON(http.StatusForbidden, gin.H{
				"error": "Forbidden: insufficient permissions to access target workspace",
			})
			c.Abort()
			return 0, nil, false, false
		}
		// 验证目标空间是否存在
		targetTenant, err := tenantService.GetTenantByID(ctx, parsedTenantID)
		if err != nil || targetTenant == nil {
			logger.Warnf(ctx, "Error getting target tenant by ID: %v, tenantID: %d", err, parsedTenantID)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid target workspace ID"})
			c.Abort()
			return 0, nil, false, false
		}
		logger.Infof(ctx, "User %s switching to tenant %d", user.ID, parsedTenantID)
		return parsedTenantID, targetTenant, parsedTenantID != user.TenantID, true
	}

	if targetTenantID == 0 {
		targetTenantID = resolveFirstMembershipTarget(ctx, user, memberService, tenantService)
	}
	return targetTenantID, nil, targetTenantID != user.TenantID, true
}

// resolveFirstMembershipTarget lets a tenantless session immediately become
// usable once an active membership exists (for example after accepting its
// first invitation or being added directly by an administrator). The user
// service persists the same earliest-membership choice on the next token
// issuance; middleware keeps the current JWT usable until then.
func resolveFirstMembershipTarget(
	ctx context.Context,
	user *types.User,
	memberService interfaces.TenantMemberService,
	tenantService interfaces.TenantService,
) uint64 {
	if user == nil || memberService == nil || tenantService == nil {
		return 0
	}
	members, err := memberService.ListByUser(ctx, user.ID)
	if err != nil {
		logger.Warnf(ctx, "Failed to list memberships for tenantless user %s: %v", user.ID, err)
		return 0
	}
	for _, member := range members {
		if member == nil || member.TenantID == 0 || member.Status != types.TenantMemberStatusActive {
			continue
		}
		tenant, err := tenantService.GetTenantByID(ctx, member.TenantID)
		if err == nil && tenant != nil {
			return member.TenantID
		}
	}
	return 0
}

func authenticateAPIKeyRequest(
	c *gin.Context,
	tenantService interfaces.TenantService,
	userService interfaces.UserService,
	apiKeyService interfaces.TenantAPIKeyService,
	apiKey string,
) bool {
	ctx := c.Request.Context()
	// AuthenticateAPIKey resolves the key by SHA-256 hash (see startup
	// BackfillMissingKeyHashes for migration 000065 placeholder rows).
	key, err := apiKeyService.AuthenticateAPIKey(ctx, apiKey)
	if err != nil || key == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: invalid API key"})
		c.Abort()
		return false
	}

	if key.IsPlatform() {
		tenantHeader := strings.TrimSpace(c.GetHeader("X-Tenant-ID"))
		if tenantHeader == "" {
			if !isPlatformTenantOptionalAPI(c.Request.URL.Path, c.Request.Method) {
				c.JSON(http.StatusConflict, gin.H{
					"error": "Workspace required: platform API keys must send X-Tenant-ID",
					"code":  "TENANT_REQUIRED",
				})
				c.Abort()
				return false
			}
			attachPlatformAPIKeyAuthContext(c, key)
		} else {
			targetTenantID, parseErr := strconv.ParseUint(tenantHeader, 10, 64)
			if parseErr != nil || targetTenantID == 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid X-Tenant-ID header"})
				c.Abort()
				return false
			}
			attachAPIKeyAuthContext(c, tenantService, userService, targetTenantID, key)
		}
	} else {
		tenantID := key.TenantIDValue()
		if tenantID == 0 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: invalid API key scope"})
			c.Abort()
			return false
		}
		if tenantHeader := strings.TrimSpace(c.GetHeader("X-Tenant-ID")); tenantHeader != "" {
			requestedTenantID, parseErr := strconv.ParseUint(tenantHeader, 10, 64)
			if parseErr != nil || requestedTenantID == 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid X-Tenant-ID header"})
				c.Abort()
				return false
			}
			if requestedTenantID != tenantID {
				c.JSON(http.StatusForbidden, gin.H{
					"error": "Forbidden: workspace API key cannot switch workspaces",
				})
				c.Abort()
				return false
			}
		}
		attachAPIKeyAuthContext(c, tenantService, userService, tenantID, key)
	}
	if c.IsAborted() {
		return false
	}
	// Per-route API-key authorization (full access + capabilities + KB scope)
	// is enforced by middleware.APIKeyRouteAuthorizer on the /api/v1 group.
	// Key-management and any other undeclared route is denied there.
	return true
}

func isPlatformTenantOptionalAPI(path, method string) bool {
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	// 精确匹配 admin 控制面前缀（"/api/v1/system/admin" 本身或其子路径）。
	// 裸 HasPrefix 会误放行诸如 "/api/v1/system/admin-foo" 的同前缀路径。
	if path == "/api/v1/system/admin" || strings.HasPrefix(path, "/api/v1/system/admin/") {
		return true
	}
	if method == http.MethodGet && (path == "/api/v1/tenants/all" || path == "/api/v1/tenants/search") {
		return true
	}
	return method == http.MethodPost && path == "/api/v1/tenants"
}

func attachPlatformAPIKeyAuthContext(c *gin.Context, key *types.TenantAPIKey) {
	principal, user := platformAPIKeyIdentity(key)
	applyAuthSession(c, authSession{
		User:      user,
		Principal: principal,
		// This role context exists only for legacy guard compatibility after
		// RequireRole short-circuits API-key principals; the key's real
		// authority is its platform capabilities enforced by the APIKeyGate.
		Role: types.TenantRoleViewer,
		APIKeyScope: &types.TenantAPIKeyScope{
			KeyID:        key.ID,
			ScopeType:    types.APIKeyScopePlatform,
			FullAccess:   false,
			Capabilities: key.Capabilities,
		},
	})
}

func platformAPIKeyIdentity(key *types.TenantAPIKey) (types.Principal, *types.User) {
	keyID := uint64(0)
	if key != nil {
		keyID = key.ID
	}
	principal := types.Principal{Type: types.PrincipalAPIPlatform, ID: strconv.FormatUint(keyID, 10)}
	userID := principal.StorageID()
	return principal, &types.User{
		ID:       userID,
		Username: userID,
		Email:    fmt.Sprintf("platform-api-key-%d@api-key.local", keyID),
		IsActive: true,
	}
}

func attachAPIKeyAuthContext(
	c *gin.Context,
	tenantService interfaces.TenantService,
	userService interfaces.UserService,
	tenantID uint64,
	key *types.TenantAPIKey,
) {
	t, err := tenantService.GetTenantByID(c.Request.Context(), tenantID)
	if err != nil {
		logger.Warnf(c.Request.Context(), "[auth] API key tenant lookup failed: tenant=%d err=%v", tenantID, err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: invalid API key"})
		c.Abort()
		return
	}

	var user *types.User
	var principal types.Principal
	if key != nil && key.IsPlatform() {
		// A platform key keeps one stable machine identity while selecting the
		// target workspace through X-Tenant-ID. Tenant API-principal modes and
		// tenant-owned synthetic users must not rewrite that identity.
		principal, user = platformAPIKeyIdentity(key)
		user.TenantID = tenantID
	} else {
		user, err = userService.GetUserByTenantID(c.Request.Context(), tenantID)
		if err != nil || user == nil {
			user = &types.User{
				ID:       fmt.Sprintf("system-%d", tenantID),
				Username: fmt.Sprintf("system-%d", tenantID),
				Email:    fmt.Sprintf("system-%d@api-key.local", tenantID),
				TenantID: tenantID,
				IsActive: true,
			}
			logger.Infof(c.Request.Context(),
				"No user found for tenant %d via API key, using synthetic system user %s", tenantID, user.ID)
		}

		var principalErr error
		principal, principalErr = resolveAPIPrincipal(c.Request.Context(), t, c.Request.Header)
		if principalErr != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": apiPrincipalAuthErrorMessage(principalErr)})
			c.Abort()
			return
		}
	}

	// This role context exists only for legacy guard compatibility after
	// RequireRole short-circuits API-key principals. The API key's real
	// authority is FullAccess + Capabilities + KnowledgeBaseIDs.
	apiKeyTenantRoleContext := types.TenantRoleViewer
	fullAccess := key != nil && key.FullAccess && !key.IsPlatform()
	if fullAccess {
		apiKeyTenantRoleContext = types.TenantRoleOwner
	}
	session := authSession{
		User:      user,
		Principal: principal,
		TenantID:  tenantID,
		Tenant:    t,
		Role:      apiKeyTenantRoleContext,
	}
	if key != nil {
		session.APIKeyScope = &types.TenantAPIKeyScope{
			KeyID:            key.ID,
			ScopeType:        key.ScopeType,
			FullAccess:       fullAccess,
			KnowledgeBaseIDs: key.KnowledgeBaseIDs,
			Capabilities:     key.Capabilities,
		}
	}
	applyAuthSession(c, session)
}

func resolveAPIPrincipal(ctx context.Context, tenant *types.Tenant, header http.Header) (types.Principal, error) {
	tenantID := uint64(0)
	if tenant != nil {
		tenantID = tenant.ID
	}
	fallback := types.Principal{
		Type: types.PrincipalAPITenant,
		ID:   strconv.FormatUint(tenantID, 10),
	}
	if tenant == nil || tenantID == 0 {
		return fallback, nil
	}
	cfg := tenant.APIPrincipalConfig
	if cfg == nil || cfg.Mode == "" || cfg.Mode == types.APIPrincipalModeTenant {
		return fallback, nil
	}
	switch cfg.Mode {
	case types.APIPrincipalModeDirect:
		externalUserID := strings.TrimSpace(header.Get(defaultExternalUserIDHeader))
		if externalUserID == "" {
			if cfg.RequireDirectHeader {
				return types.Principal{}, errMissingDirectHeader
			}
			return fallback, nil
		}
		if err := validateExternalUserID(externalUserID); err != nil {
			return types.Principal{}, fmt.Errorf("%w: %v", errInvalidExternalUserID, err)
		}
		return types.Principal{
			Type: types.PrincipalAPIExternalUser,
			ID:   strconv.FormatUint(tenantID, 10) + ":" + externalUserID,
		}, nil
	case types.APIPrincipalModeSignedToken:
		externalUserID, err := verifyExternalUserJWT(header.Get(defaultExternalUserTokenHeader), tenantID, cfg.HMACSecret)
		if err != nil || externalUserID == "" {
			logger.Warnf(ctx, "invalid external user token for tenant=%d: %v", tenantID, err)
			return types.Principal{}, fmt.Errorf("%w: %w", errInvalidExternalUserToken, err)
		}
		if err := validateExternalUserID(externalUserID); err != nil {
			return types.Principal{}, fmt.Errorf("%w: %v", errInvalidExternalUserID, err)
		}
		return types.Principal{
			Type: types.PrincipalAPIExternalUser,
			ID:   strconv.FormatUint(tenantID, 10) + ":" + externalUserID,
		}, nil
	default:
		return fallback, nil
	}
}

func verifyExternalUserJWT(tokenString string, tenantID uint64, secret string) (string, error) {
	tokenString = strings.TrimSpace(tokenString)
	secret = strings.TrimSpace(secret)
	if tokenString == "" {
		return "", errors.New("missing external user token")
	}
	if secret == "" {
		return "", errors.New("external user token secret is not configured")
	}
	claims := jwt.MapClaims{}
	parser := jwt.NewParser(
		jwt.WithAudience("weknora"),
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
	)
	token, err := parser.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return "", err
	}
	if token == nil || !token.Valid {
		return "", errors.New("invalid external user token")
	}
	exp, err := claims.GetExpirationTime()
	if err != nil || exp == nil {
		return "", errors.New("missing expiration")
	}
	if time.Until(exp.Time) > maxExternalUserTokenTTL {
		return "", fmt.Errorf("token lifetime exceeds %s", maxExternalUserTokenTTL)
	}
	if nbf, nbfErr := claims.GetNotBefore(); nbfErr == nil && nbf != nil && time.Now().Before(nbf.Time) {
		return "", errors.New("token not yet valid")
	}
	if got := principalTenantIDFromClaims(claims); got != tenantID {
		return "", fmt.Errorf("workspace mismatch: got %d want %d", got, tenantID)
	}
	sub, _ := claims["sub"].(string)
	sub = strings.TrimSpace(sub)
	if sub == "" {
		return "", errors.New("missing subject")
	}
	return sub, nil
}

func validateExternalUserID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("empty external user id")
	}
	if len(id) > maxExternalUserIDLen {
		return fmt.Errorf("external user id too long (max %d)", maxExternalUserIDLen)
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return errors.New("external user id contains invalid characters")
		}
	}
	return nil
}

func apiPrincipalAuthErrorMessage(err error) string {
	switch {
	case errors.Is(err, errMissingDirectHeader):
		return "Unauthorized: missing external user id header"
	case errors.Is(err, errInvalidExternalUserID):
		return "Unauthorized: invalid external user id"
	case errors.Is(err, errInvalidExternalUserToken):
		return "Unauthorized: invalid external user token"
	default:
		return "Unauthorized: invalid external user token"
	}
}

func principalTenantIDFromClaims(claims jwt.MapClaims) uint64 {
	v, ok := claims["tenant_id"]
	if !ok {
		return 0
	}
	switch t := v.(type) {
	case float64:
		if t <= 0 {
			return 0
		}
		return uint64(t)
	case int64:
		if t <= 0 {
			return 0
		}
		return uint64(t)
	case uint64:
		return t
	case json.Number:
		n, err := strconv.ParseUint(t.String(), 10, 64)
		if err != nil {
			return 0
		}
		return n
	case string:
		n, err := strconv.ParseUint(strings.TrimSpace(t), 10, 64)
		if err != nil {
			return 0
		}
		return n
	default:
		return 0
	}
}

// resolveTenantRole determines the caller's TenantRole inside targetTenantID.
//
// Order of resolution:
//  1. Active TenantMember row → return that role.
//  2. Cross-tenant superuser switch (X-Tenant-ID with CanAccessAllTenants=true)
//     → grant Admin in the target tenant. Org admins are intentionally not
//     promoted to Owner; tenant deletion / API-key rotation should always
//     stay with a real Owner inside the target tenant. Cross-tenant access
//     is also never allowed to trigger the orphan-tenant auto-promotion
//     below — a superuser only visits, never claims ownership.
//  3. No membership but the tenant currently has zero active members AND
//     the caller is authenticating into their own home tenant (i.e.
//     targetTenantID == user.TenantID and this is not a cross-tenant
//     switch). This is the API-key-only orphan-tenant self-heal path:
//     the registrant becomes Owner of the tenant their own user record
//     points to. Any other path (cross-tenant switch, JWT minted for a
//     foreign tenant, etc.) is intentionally excluded to avoid silent
//     ownership grabs.
//  4. Otherwise → return ok=false. Caller decides:
//     - When EnableRBAC=true (or cfg unavailable): treat as 403.
//     - When EnableRBAC=false: fail open with Admin so existing deployments
//     don't break in the rollout window where memberships might lag user
//     records.
//
// The boolean second return value reports whether enforcement should reject
// the request. It is true whenever a usable role was found OR fail-open
// applies; false only when we want callers to abort with 403.
func resolveTenantRole(
	ctx context.Context,
	memberService interfaces.TenantMemberService,
	user *types.User,
	targetTenantID uint64,
	crossTenantSwitch bool,
	cfg *config.Config,
) (types.TenantRole, bool) {
	// 1. 正常成员关系
	member, err := memberService.GetMembership(ctx, user.ID, targetTenantID)
	if err == nil && member != nil && member.Status == types.TenantMemberStatusActive {
		logger.Infof(ctx,
			"[auth] resolveTenantRole step1 hit: user=%s tenant=%d row_role=%s row_status=%s",
			user.ID, targetTenantID, member.Role, member.Status)
		return member.Role, true
	}
	if err != nil {
		logger.Warnf(ctx, "tenant_members lookup failed user=%s tenant=%d: %v",
			user.ID, targetTenantID, err)
		// Fall through; treat lookup errors the same as "no membership
		// found" so a transient DB hiccup doesn't lock everyone out.
	} else {
		var statusInfo string
		if member == nil {
			statusInfo = "no_row"
		} else {
			statusInfo = "row_exists status=" + string(member.Status) + " role=" + string(member.Role)
		}
		logger.Warnf(ctx,
			"[auth] resolveTenantRole step1 miss: user=%s tenant=%d (%s)",
			user.ID, targetTenantID, statusInfo)
	}

	// 2. 跨空间超管直通：CanAccessAllTenants 用户切到别的空间时不强制要求 membership。
	//    注意：这里只授予临时 Admin 角色，不写入 tenant_members，避免"看一眼别人空间"
	//    意外升级为持久化所有权。
	if crossTenantSwitch && user.CanAccessAllTenants {
		logger.Infof(ctx,
			"[auth] resolveTenantRole step2 (cross-tenant superuser) -> Admin: user=%s tenant=%d",
			user.ID, targetTenantID)
		return types.TenantRoleAdmin, true
	}

	// 3. 孤儿空间自愈：仅当用户登录的是自己的 home tenant、且该空间尚无任何活跃成员时
	//    允许自动晋升为 Owner。跨空间 switch / JWT 指向他人空间的场景一律不进入此分支，
	//    防止越权获得他人空间的 Owner 权限。
	isHomeTenant := !crossTenantSwitch && targetTenantID == user.TenantID
	if isHomeTenant {
		hasAny, anyErr := memberService.HasAnyMembers(ctx, targetTenantID)
		if anyErr == nil && !hasAny {
			if _, e := memberService.AddMember(
				ctx, user.ID, targetTenantID, types.TenantRoleOwner, nil,
			); e == nil {
				logger.Infof(ctx,
					"[audit] Auto-promoted user %s to Owner of orphan tenant %d (home_tenant=true)",
					user.ID, targetTenantID,
				)
				return types.TenantRoleOwner, true
			} else {
				logger.Warnf(ctx, "Failed to auto-promote user %s in tenant %d: %v",
					user.ID, targetTenantID, e)
			}
		}
	}

	// 4. 兜底：根据 EnableRBAC 决定 fail-closed 还是 fail-open
	if cfg != nil && cfg.Tenant.IsRBACEnforced() {
		logger.Warnf(ctx,
			"[auth] resolveTenantRole step4 fail-closed (EnableRBAC=true): user=%s tenant=%d",
			user.ID, targetTenantID)
		return "", false
	}
	logger.Warnf(ctx,
		"[auth] resolveTenantRole step4 fail-open (EnableRBAC=false) -> Admin: user=%s tenant=%d",
		user.ID, targetTenantID)
	// fail-open 期间保持现有行为（每个登录用户在自己空间里都是"管理员"）。
	return types.TenantRoleAdmin, true
}
