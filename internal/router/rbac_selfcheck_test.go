package router

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestAssertAPIKeyPoliciesMatchRoutes_TrailingSlash guards the class of bug
// where a route registered with a "/" rel (gin path ".../x/") was flagged as
// missing against the normalized policy key (".../x"), panicking at startup.
func TestAssertAPIKeyPoliciesMatchRoutes_TrailingSlash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v1 := engine.Group("/api/v1")

	g := &rbacGuards{}
	noop := func(c *gin.Context) {}

	// Register through the apiKey helpers using both "" and "/" rel plus a
	// nested path, mirroring real route declarations.
	empty := g.apiKeyGroup(v1.Group("/knowledge-bases"), apiKeyViewer())
	empty.GET("", noop)

	slash := g.apiKeyGroup(v1.Group("/evaluation"), apiKeyOwner())
	slash.POST("/", noop)
	slash.GET("/", noop)

	g.apiKeyRoute(v1, http.MethodGet, "/tenants", apiKeyOwner(), noop)

	// Must not panic: every declared policy resolves to a real route despite
	// the trailing-slash difference on /evaluation.
	g.assertAPIKeyPoliciesMatchRoutes(engine)
}

// TestAssertAPIKeyPoliciesMatchRoutes_Missing verifies the self-check still
// panics when a policy points at a genuinely non-existent route.
func TestAssertAPIKeyPoliciesMatchRoutes_Missing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()

	g := &rbacGuards{}
	// Declare a policy for a path that is never registered on the engine.
	g.ensureAPIKeyAuthorizer().Register(http.MethodPost, "/api/v1/does-not-exist", apiKeyOwner())

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for policy on non-existent route")
		}
	}()
	g.assertAPIKeyPoliciesMatchRoutes(engine)
}
