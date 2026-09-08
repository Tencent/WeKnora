package semantic

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/semantic/cubeclient"
)

// Module wiring. Everything the rest of WeKnora needs to touch is here:
// router.go calls Register (+2 lines), agent_service.go consumes Default().

var (
	mu     sync.RWMutex
	engine *Engine
)

// Default returns the module engine, or nil when the module is disabled.
func Default() *Engine {
	mu.RLock()
	defer mu.RUnlock()
	return engine
}

// Enabled reports whether the module was wired at startup.
func Enabled() bool { return Default() != nil }

// LoadConfig reads the module settings from the environment.
func LoadConfig() Config {
	return Config{
		Enabled:         envBool("CUBE_ENABLE", false),
		APIURL:          strings.TrimRight(os.Getenv("CUBE_API_URL"), "/"),
		APISecret:       os.Getenv("CUBEJS_API_SECRET"),
		ModelDir:        os.Getenv("CUBE_MODEL_DIR"),
		DatasourcesFile: os.Getenv("CUBE_DATASOURCES_FILE"),
		MaxPreviewRows:  envInt("CUBE_MAX_PREVIEW_ROWS", 200),
		PublishTimeout:  time.Duration(envInt("CUBE_PUBLISH_TIMEOUT", 30)) * time.Second,
		HTTPTimeout:     time.Duration(envInt("CUBE_HTTP_TIMEOUT", 120)) * time.Second,
		AuditLimit:      envInt("CUBE_AUDIT_LIMIT", 100),
	}
}

func envInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func envBool(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// Register wires the module and mounts its routes under the given group
// (which must be the authenticated /api/v1 group). When CUBE_ENABLE is not
// set the function is a no-op and the rest of WeKnora behaves exactly as
// upstream. viewer/contributor/admin are the platform RBAC guards.
func Register(v1 *gin.RouterGroup, db *gorm.DB, viewer, contributor, admin gin.HandlerFunc) error {
	cfg := LoadConfig()
	if !cfg.Enabled {
		return nil
	}
	if cfg.APIURL == "" || cfg.APISecret == "" {
		return fmt.Errorf("CUBE_ENABLE=true but CUBE_API_URL / CUBEJS_API_SECRET are not set")
	}
	e, err := NewEngine(cfg, db)
	if err != nil {
		return fmt.Errorf("semantic module init failed: %w", err)
	}
	h := NewHandler(e)
	registerRoutes(v1.Group("/semantic"), h, viewer, contributor, admin)

	mu.Lock()
	engine = e
	mu.Unlock()
	return nil
}

// registerRoutes mounts the API. Role floors:
//   - connections & data groups: tenant infrastructure → read Viewer+, write Admin+
//   - models: the modeling workspace → read Viewer+, draft Contributor+, publish Admin+
func registerRoutes(g *gin.RouterGroup, h *Handler, viewer, contributor, admin gin.HandlerFunc) {
	g.GET("/info", viewer, h.Info)

	// connections
	g.POST("/connections", admin, h.CreateConnection)
	g.POST("/connections/test", admin, h.TestConnectionRaw)
	g.GET("/connections", viewer, h.ListConnections)
	g.GET("/connections/:id", viewer, h.GetConnection)
	g.PUT("/connections/:id", admin, h.UpdateConnection)
	g.DELETE("/connections/:id", admin, h.DeleteConnection)
	g.POST("/connections/:id/test", admin, h.TestConnectionByID)
	g.POST("/connections/:id/test-draft", admin, h.TestConnectionDraftByID)
	g.GET("/connections/:id/tables", viewer, h.ListTables)
	g.GET("/connections/:id/columns", viewer, h.ListColumns)
	g.GET("/connections/:id/draft", contributor, h.DraftCube)

	// models
	g.POST("/models", contributor, h.CreateModel)
	g.GET("/models", viewer, h.ListModels)
	g.GET("/models/:id", viewer, h.GetModel)
	g.PUT("/models/:id", contributor, h.UpdateModel)
	g.DELETE("/models/:id", admin, h.DeleteModel)
	g.POST("/models/:id/publish", admin, h.PublishModel)
	g.POST("/models/:id/unpublish", admin, h.UnpublishModel)
	g.GET("/models/:id/versions", viewer, h.ListVersions)
	g.POST("/models/:id/rollback", admin, h.RollbackModel)
	g.POST("/models/:id/preview", contributor, h.PreviewModel)

	// data groups
	g.POST("/groups", admin, h.CreateGroup)
	g.GET("/groups", viewer, h.ListGroups)
	g.PUT("/groups/:id", admin, h.UpdateGroup)
	g.DELETE("/groups/:id", admin, h.DeleteGroup)
	g.PUT("/groups/:id/members", admin, h.SetGroupMembers)
	g.GET("/groups/:id/members", viewer, h.ListGroupMembers)
	g.GET("/groups/:id/usage", viewer, h.GroupUsage)

	// audit
	g.GET("/audit", admin, h.ListAudits)

	// meta is consumed by both the studio and (indirectly) agent tools
	g.GET("/meta", viewer, h.Meta)
}

// CubeMetaForUser is the entry point used by the agent tools: it resolves
// the caller's data groups and returns the permission-filtered model meta.
func CubeMetaForUser(ctx context.Context, tenantID uint64, userID string) (*cubeclient.MetaResponse, error) {
	e := Default()
	if e == nil || e.client == nil {
		return nil, fmt.Errorf("semantic modeling module is not enabled")
	}
	uctx, err := e.WithUserContext(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	meta, err := e.client.Meta(uctx)
	if err != nil {
		return nil, err
	}
	e.filterMetaForUser(uctx, tenantID, userID, meta)
	return meta, nil
}

// CubeQueryForUser runs a Cube query under the caller's identity (agent tools).
func CubeQueryForUser(
	ctx context.Context,
	tenantID uint64,
	userID string,
	q *PreviewQuery,
) (*cubeclient.LoadResponse, error) {
	e := Default()
	if e == nil || e.client == nil {
		return nil, fmt.Errorf("semantic modeling module is not enabled")
	}
	return e.QueryForUser(ctx, tenantID, userID, q)
}

// CubeSQLForUser dry-runs a Cube query under the caller's identity (agent tools).
func CubeSQLForUser(
	ctx context.Context,
	tenantID uint64,
	userID string,
	q *PreviewQuery,
) (*cubeclient.SQLResponse, error) {
	e := Default()
	if e == nil || e.client == nil {
		return nil, fmt.Errorf("semantic modeling module is not enabled")
	}
	return e.SQLForUser(ctx, tenantID, userID, q)
}
