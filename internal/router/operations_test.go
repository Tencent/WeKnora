package router

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/gin-gonic/gin"
)

// TestBuiltinOperationsMatchRegisteredRoutes registers the real route tables
// the catalog points into and requires every operation to resolve, so a
// renamed or removed route fails here rather than at startup.
func TestBuiltinOperationsMatchRegisteredRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	g := newRBACGuards(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	RegisterKnowledgeBaseRoutes(v1, &handler.KnowledgeBaseHandler{}, g)
	RegisterKnowledgeBaseActivityRoutes(v1, &handler.AuditLogHandler{}, g)
	RegisterKnowledgeRoutes(v1, &handler.KnowledgeHandler{}, g)
	RegisterWikiPageRoutes(v1, &handler.WikiPageHandler{}, g)
	RegisterChunkRoutes(v1, &handler.ChunkHandler{}, g)
	RegisterKnowledgeTagRoutes(v1, &handler.TagHandler{}, g)
	RegisterOrganizationRoutes(v1, &handler.OrganizationHandler{}, g)

	if missing := missingOperationRoutes(engine, operations.Builtin()); len(missing) > 0 {
		t.Fatalf("operations without a route: %v", missing)
	}
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
