package middleware

import (
	stderrors "errors"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

var errTenantAPIKeyScopeForbidden = stderrors.New("workspace API key scope forbidden")

// APIKeyRoutePolicy declares how an X-API-Key caller may use a single route.
//
// Design: API-key authorization is a SEPARATE authority from the JWT
// role/ownership guards. Ownership ("creator OR Admin+") is a human concept
// and never applies to a machine principal; instead every API-key-accessible
// route declares one policy here, and the APIKeyGate is the single place that
// enforces it. Routes that declare no policy are denied for API keys by
// default (fail-closed), which removes the old "remember to add APIKeyDeny"
// footgun.
type APIKeyRoutePolicy struct {
	// PlatformOnly rejects workspace-bound keys even when they are full-access.
	// It is used for control-plane routes under /system/admin and cross-workspace
	// tenant lifecycle APIs.
	PlatformOnly bool
	// RequireFullAccess admits only full-access tenant API keys unless one of
	// the listed capabilities also matches. Routes with neither full-access
	// requirement nor capabilities are open to any valid API key.
	RequireFullAccess bool

	// Capabilities is an any-of allow-list for scoped API keys. A capability
	// never widens which knowledge bases a key may touch; KB scoping is
	// enforced downstream by KBAccess guards and handler scope checks.
	Capabilities []types.APIKeyCapability
}

// WithCapability returns a copy of the policy that additionally admits keys
// carrying capability c. Multiple calls accumulate (any-of semantics);
// duplicates are ignored.
func (p APIKeyRoutePolicy) WithCapability(c types.APIKeyCapability) APIKeyRoutePolicy {
	for _, existing := range p.Capabilities {
		if existing == c {
			return p
		}
	}
	// Copy the slice so mutating the returned policy never aliases the
	// receiver's backing array.
	next := make([]types.APIKeyCapability, len(p.Capabilities), len(p.Capabilities)+1)
	copy(next, p.Capabilities)
	p.Capabilities = append(next, c)
	return p
}

// APIKeyRouteAuthorizer is the registry of per-route API-key policies. It is
// populated at router-construction time (single-threaded) and only read at
// request time, so it needs no locking.
type APIKeyRouteAuthorizer struct {
	// policies is keyed by HTTP method, then by the gin full-path
	// template (e.g. "/api/v1/knowledge-bases/:id/knowledge/file").
	policies map[string]map[string]APIKeyRoutePolicy
}

// NewAPIKeyRouteAuthorizer returns an empty authorizer.
func NewAPIKeyRouteAuthorizer() *APIKeyRouteAuthorizer {
	return &APIKeyRouteAuthorizer{policies: map[string]map[string]APIKeyRoutePolicy{}}
}

// Register records the API-key policy for (method, fullPath). fullPath MUST be
// the same string gin reports via c.FullPath() for the route, otherwise the
// gate lookup will miss and the route will be denied for API keys. Router-side
// helpers build fullPath from the group's BasePath so the two stay in sync.
func (a *APIKeyRouteAuthorizer) Register(method, fullPath string, policy APIKeyRoutePolicy) {
	method = strings.ToUpper(strings.TrimSpace(method))
	fullPath = normalizeRoutePath(fullPath)
	if a.policies[method] == nil {
		a.policies[method] = map[string]APIKeyRoutePolicy{}
	}
	a.policies[method][fullPath] = policy
}

// Lookup returns the policy for (method, fullPath) if one was declared.
func (a *APIKeyRouteAuthorizer) Lookup(method, fullPath string) (APIKeyRoutePolicy, bool) {
	byPath, ok := a.policies[strings.ToUpper(method)]
	if !ok {
		return APIKeyRoutePolicy{}, false
	}
	policy, ok := byPath[normalizeRoutePath(fullPath)]
	return policy, ok
}

// RegisteredRoutes returns every (method, fullPath) pair the authorizer knows
// about. Used by the router startup self-check to detect stale path templates.
func (a *APIKeyRouteAuthorizer) RegisteredRoutes() map[string][]string {
	out := make(map[string][]string, len(a.policies))
	for method, byPath := range a.policies {
		paths := make([]string, 0, len(byPath))
		for path := range byPath {
			paths = append(paths, path)
		}
		out[method] = paths
	}
	return out
}

// Middleware returns the gate middleware. It runs as the first handler on the
// authenticated API group, after routing (so c.FullPath() is populated). JWT
// principals pass straight through; API-key principals are authorized purely
// from the declared policy table.
func (a *APIKeyRouteAuthorizer) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		scope, ok := types.TenantAPIKeyScopeFromContext(c.Request.Context())
		if !ok {
			c.Next()
			return
		}
		if err := a.authorize(scope, c.Request.Method, c.FullPath()); err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "Forbidden: API key scope does not allow this operation",
			})
			return
		}
		c.Next()
	}
}

// authorize applies the declared policy to an API-key scope. Absent policy =>
// default deny.
func (a *APIKeyRouteAuthorizer) authorize(scope types.TenantAPIKeyScope, method, fullPath string) error {
	policy, ok := a.Lookup(method, fullPath)
	if !ok {
		return errTenantAPIKeyScopeForbidden
	}
	if policy.PlatformOnly && !scope.IsPlatform() {
		return errTenantAPIKeyScopeForbidden
	}
	if policy.PlatformOnly && len(policy.Capabilities) == 0 {
		return errTenantAPIKeyScopeForbidden
	}
	if scope.FullAccess {
		return nil
	}
	for _, cap := range policy.Capabilities {
		if cap != "" && scope.HasCapability(cap) {
			return nil
		}
	}
	if !policy.RequireFullAccess && len(policy.Capabilities) == 0 {
		return nil
	}
	return errTenantAPIKeyScopeForbidden
}

// DenyAPIKeyPrincipal returns a middleware that rejects any X-API-Key
// principal outright. Use it on routes registered directly on the engine
// (outside the /api/v1 group) where the APIKeyRouteAuthorizer.Middleware
// gate does NOT run — the JWT role guards (RequireRole / RequireSystemAdmin
// / RequireOwnershipOrRole) short-circuit API-key principals on the
// assumption the gate already authorized them, so an ungated route would
// otherwise let any valid key through. JWT sessions pass straight through.
func DenyAPIKeyPrincipal() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := types.TenantAPIKeyScopeFromContext(c.Request.Context()); ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "Forbidden: API keys cannot access this endpoint",
			})
			return
		}
		c.Next()
	}
}

// AllowFileServeAPIKey guards the tenant-scoped file-proxy routes (/files and
// the KB-scoped image proxy) for X-API-Key callers. Those routes serve an
// arbitrary storage path that only carries a tenant segment — there is no KB
// id in the path a KB-restricted key's allow-list could be checked against —
// so a KB-restricted key is denied outright (it must download KB content via
// the KB-scoped routes such as /knowledge/:id/download, which DO enforce the
// allow-list). A key passes when it is full-access, or when it is NOT
// KB-restricted and carries the retrieve capability: that is exactly the class
// of key that can already read any of the tenant's KB content, so exposing the
// tenant-bounded raw file path (the handler still enforces
// ValidateStoragePathTenant / the KB owner tenant) grants it nothing new. JWT
// sessions carry no API-key scope and pass straight through.
func AllowFileServeAPIKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		scope, ok := types.TenantAPIKeyScopeFromContext(c.Request.Context())
		if !ok {
			c.Next()
			return
		}
		if scope.FullAccess ||
			(!scope.IsKnowledgeBaseRestricted() &&
				scope.HasCapability(types.APIKeyCapabilityRetrieve)) {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "Forbidden: API key scope does not allow this operation",
		})
	}
}

// normalizeRoutePath collapses duplicate slashes and trims a trailing slash so
// helper-built paths (BasePath()+rel) match gin's c.FullPath() exactly.
func normalizeRoutePath(p string) string {
	if p == "" {
		return ""
	}
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	if len(p) > 1 {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}
