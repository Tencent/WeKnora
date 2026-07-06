package middleware

import (
	stderrors "errors"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

var errTenantAPIKeyScopeForbidden = stderrors.New("tenant api key scope forbidden")

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
	// MinRole is the minimum tenant role an API key needs for this route.
	//
	// Data-plane routes (knowledge bases and their content) use Viewer /
	// Contributor / Admin; for those, safe reads (GET/HEAD/OPTIONS) only
	// require Viewer while writes require the declared role. Tenant-level
	// infrastructure routes (models, vector stores, MCP, data sources,
	// channels, tenant KV, ...) use Owner, which is required for EVERY
	// method — reading tenant configuration is itself a privileged action.
	//
	// KB scoping is NOT enforced here: a key's knowledge_base_ids allow-list
	// is a pure data filter applied by the KBAccess guards and the handler
	// scope checks on data-plane routes. Tenant-level routes are simply
	// gated behind the Owner role instead of being reachable by a narrow key.
	MinRole types.TenantRole
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
	role := scope.Role
	if role == "" {
		role = types.TenantRoleViewer
	}
	required := policy.MinRole
	if required == "" {
		required = types.TenantRoleViewer
	}
	// Data-plane safe reads only need a Viewer floor. Tenant-level (Owner)
	// routes require the full role even for reads, so they are never lowered.
	if isSafeHTTPMethod(method) && required != types.TenantRoleOwner {
		required = types.TenantRoleViewer
	}
	if !role.HasPermission(required) {
		return errTenantAPIKeyScopeForbidden
	}
	return nil
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

func isSafeHTTPMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
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
