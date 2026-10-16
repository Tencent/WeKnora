package router

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/handler/session"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// registerOperationAreas registers the real route tables the catalog points
// into.
func registerOperationAreas(v1 *gin.RouterGroup, g *rbacGuards) {
	RegisterKnowledgeBaseRoutes(v1, &handler.KnowledgeBaseHandler{}, g)
	RegisterKnowledgeBaseActivityRoutes(v1, &handler.AuditLogHandler{}, g)
	RegisterKnowledgeRoutes(v1, &handler.KnowledgeHandler{}, g)
	RegisterWikiPageRoutes(v1, &handler.WikiPageHandler{}, g)
	RegisterChunkRoutes(v1, &handler.ChunkHandler{}, g)
	RegisterKnowledgeTagRoutes(v1, &handler.TagHandler{}, g)
	RegisterOrganizationRoutes(v1, &handler.OrganizationHandler{}, g)
	RegisterFAQRoutes(v1, &handler.FAQHandler{}, g)
	RegisterCustomAgentRoutes(v1, &handler.CustomAgentHandler{}, g)
	RegisterSkillRoutes(v1, &handler.SkillHandler{}, g)
	RegisterModelRoutes(v1, &handler.ModelHandler{}, &handler.ModelCredentialsHandler{}, g)
	RegisterMCPServiceRoutes(v1, &handler.MCPServiceHandler{}, &handler.MCPCredentialsHandler{},
		&handler.MCPOAuthHandler{}, g)
	RegisterSandboxConfigRoutes(v1, &handler.SandboxConfigHandler{}, &handler.SandboxSkillHandler{}, g)
	RegisterSessionRoutes(v1, &session.Handler{}, nil, g)
	RegisterMessageRoutes(v1, &handler.MessageHandler{}, g)
}

// TestBuiltinOperationsMatchRegisteredRoutes requires every operation to
// resolve, so a renamed or removed route fails here rather than at startup.
func TestBuiltinOperationsMatchRegisteredRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	g := newRBACGuards(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	registerOperationAreas(engine.Group("/api/v1"), g)

	if missing := missingOperationRoutes(engine, operations.Builtin()); len(missing) > 0 {
		t.Fatalf("operations without a route: %v", missing)
	}
}

// TestBuiltinOperationRolesMatchRouteGuards calls every operation's route
// with RBAC enforced, as each workspace role, and requires the lowest role
// the guards let through to be the operation's declared Role. The caller
// created every resource, so ownership guards decide by role the way they do
// on the caller's own knowledge base, including a creator since demoted to
// viewer. Anything after the guards (KB access, the handler itself) may
// fail, which still counts as passing the role.
func TestBuiltinOperationRolesMatchRouteGuards(t *testing.T) {
	gin.SetMode(gin.TestMode)
	enforce := true
	cfg := &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enforce}}
	const userID = "probe-user"
	owner := func(*gin.Context) (string, error) { return userID, nil }
	g := &rbacGuards{
		cfg:                    cfg,
		kbCreator:              owner,
		agentCreator:           owner,
		kbCreatorFromKbIDParam: owner,
		knowledgeKBCreator:     owner,
		chunkKBCreator:         owner,
		chunkKBCreatorFromID:   owner,
		wikiKBCreator:          owner,
	}

	var role types.TenantRole
	engine := gin.New()
	engine.Use(gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, _ any) {
		c.AbortWithStatus(http.StatusInternalServerError)
	}))
	engine.Use(func(c *gin.Context) {
		ctx := types.WithCaller(c.Request.Context(), types.Caller{TenantID: 1, UserID: userID, Role: role})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	registerOperationAreas(engine.Group("/api/v1"), g)

	ladder := []types.TenantRole{
		types.TenantRoleViewer, types.TenantRoleContributor, types.TenantRoleAdmin, types.TenantRoleOwner,
	}
	for _, op := range operations.Builtin().All() {
		var lowest types.TenantRole
		for _, role = range ladder {
			if !deniedByRole(engine, op) {
				lowest = role
				break
			}
		}
		if lowest != op.Role {
			t.Errorf("%s (%s %s): guards let %q through first, catalog declares %q; "+
				`set "role" in its x-agent annotation`, op.ID, op.Method, op.Route, lowest, op.Role)
		}
	}
}

// deniedByRole reports whether the route guards turn the caller away.
func deniedByRole(engine *gin.Engine, op operations.Operation) bool {
	segments := strings.Split(op.Route, "/")
	for i, seg := range segments {
		if strings.HasPrefix(seg, ":") || strings.HasPrefix(seg, "*") {
			segments[i] = "probe-" + seg[1:]
		}
	}
	var body io.Reader
	if op.Method != http.MethodGet {
		body = strings.NewReader("{}")
	}
	req := httptest.NewRequest(op.Method, strings.Join(segments, "/"), body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		return false
	}
	text := rec.Body.String()
	return strings.Contains(text, "insufficient workspace role") || strings.Contains(text, "must own the resource")
}

func TestAssertOperationRoutesExistPanicsOnDrift(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/api/v1/knowledge/:id", func(*gin.Context) {})

	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic for operations whose routes are not registered")
		}
	}()
	assertOperationRoutesExist(engine, operations.Builtin())
}
