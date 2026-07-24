package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type downloadKnowledgeLookup struct {
	knowledge *types.Knowledge
}

func (s *downloadKnowledgeLookup) GetKnowledgeByIDOnly(_ context.Context, id string) (*types.Knowledge, error) {
	if s.knowledge != nil && s.knowledge.ID == id {
		return s.knowledge, nil
	}
	return nil, apprepo.ErrKnowledgeNotFound
}

type downloadKBShareStub struct {
	interfaces.KBShareService
	permission types.OrgMemberRole
	source     uint64
}

func (s *downloadKBShareStub) CheckTenantKBPermission(
	_ context.Context,
	_ string,
	_ uint64,
	_ types.TenantRole,
) (types.OrgMemberRole, bool, error) {
	return s.permission, true, nil
}

func (s *downloadKBShareStub) GetKBSourceTenant(_ context.Context, _ string) (uint64, error) {
	return s.source, nil
}

func newKnowledgeDownloadRouteTestEngine(
	t *testing.T,
	role types.TenantRole,
	knowledge *types.Knowledge,
	kb *types.KnowledgeBase,
	share interfaces.KBShareService,
) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	enabled := true
	guards := &rbacGuards{
		cfg:              &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}},
		knowledgeService: &downloadKnowledgeLookup{knowledge: knowledge},
		kbService:        &stubWikiKBLookup{kbs: map[string]*types.KnowledgeBase{kb.ID: kb}},
		kbShareService:   share,
	}

	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(1))
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, role)
		c.Request = c.Request.WithContext(ctx)
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Next()
	})
	RegisterKnowledgeRoutes(r.Group("/api/v1"), &handler.KnowledgeHandler{}, guards)
	return r
}

func TestKnowledgeDownloadRejectsTenantViewer(t *testing.T) {
	engine := newKnowledgeDownloadRouteTestEngine(
		t,
		types.TenantRoleViewer,
		&types.Knowledge{ID: "knowledge-own", KnowledgeBaseID: "kb-own", TenantID: 1},
		&types.KnowledgeBase{ID: "kb-own", TenantID: 1},
		nil,
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/knowledge/knowledge-own/download", nil)
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())
}

func TestKnowledgeDownloadRejectsReadOnlySharedKB(t *testing.T) {
	engine := newKnowledgeDownloadRouteTestEngine(
		t,
		types.TenantRoleContributor,
		&types.Knowledge{ID: "knowledge-shared", KnowledgeBaseID: "kb-shared", TenantID: 2},
		&types.KnowledgeBase{ID: "kb-shared", TenantID: 2},
		&downloadKBShareStub{permission: types.OrgRoleViewer, source: 2},
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/knowledge/knowledge-shared/download", nil)
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())
}
