package router

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

type downloadKnowledgeServiceStub struct {
	interfaces.KnowledgeService
	knowledge *types.Knowledge
}

func (s *downloadKnowledgeServiceStub) GetKnowledgeByIDOnly(_ context.Context, id string) (*types.Knowledge, error) {
	if s.knowledge != nil && s.knowledge.ID == id {
		return s.knowledge, nil
	}
	return nil, apprepo.ErrKnowledgeNotFound
}

func (s *downloadKnowledgeServiceStub) GetKnowledgeFile(context.Context, string) (io.ReadCloser, string, error) {
	return io.NopCloser(strings.NewReader("file-body")), "doc.pdf", nil
}

type downloadKBServiceStub struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *downloadKBServiceStub) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	if s.kb != nil && s.kb.ID == id {
		return s.kb, nil
	}
	return nil, apprepo.ErrKnowledgeBaseNotFound
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

type downloadCloseNotifyRecorder struct {
	*httptest.ResponseRecorder
}

func (r *downloadCloseNotifyRecorder) CloseNotify() <-chan bool {
	ch := make(chan bool, 1)
	return ch
}

func newKnowledgeDownloadRouteTestEngine(
	t *testing.T,
	role types.TenantRole,
	userID string,
	knowledge *types.Knowledge,
	kb *types.KnowledgeBase,
	share interfaces.KBShareService,
) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	enabled := true
	cfg := &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}
	guards := &rbacGuards{
		cfg:              cfg,
		knowledgeService: &downloadKnowledgeLookup{knowledge: knowledge},
		kbService:        &stubWikiKBLookup{kbs: map[string]*types.KnowledgeBase{kb.ID: kb}},
		kbShareService:   share,
	}

	h := handler.NewKnowledgeHandler(
		cfg,
		&downloadKnowledgeServiceStub{knowledge: knowledge},
		&downloadKBServiceStub{kb: kb},
		share,
		nil, nil, nil,
	)

	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(1))
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, role)
		if userID != "" {
			ctx = context.WithValue(ctx, types.UserIDContextKey, userID)
		}
		c.Request = c.Request.WithContext(ctx)
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Next()
	})
	RegisterKnowledgeRoutes(r.Group("/api/v1"), h, guards)
	return r
}

func serveKnowledgeFile(t *testing.T, engine *gin.Engine, url string) *downloadCloseNotifyRecorder {
	t.Helper()
	rec := &downloadCloseNotifyRecorder{ResponseRecorder: httptest.NewRecorder()}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	engine.ServeHTTP(rec, req)
	return rec
}

func TestKnowledgeDownloadRejectsTenantViewer(t *testing.T) {
	engine := newKnowledgeDownloadRouteTestEngine(
		t,
		types.TenantRoleViewer,
		"me",
		&types.Knowledge{ID: "knowledge-own", KnowledgeBaseID: "kb-own", TenantID: 1},
		&types.KnowledgeBase{ID: "kb-own", TenantID: 1, CreatorID: "other"},
		nil,
	)

	rec := serveKnowledgeFile(t, engine, "/api/v1/knowledge/knowledge-own/download")
	require.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())
}

func TestKnowledgeDownloadRejectsReadOnlySharedKB(t *testing.T) {
	engine := newKnowledgeDownloadRouteTestEngine(
		t,
		types.TenantRoleContributor,
		"me",
		&types.Knowledge{ID: "knowledge-shared", KnowledgeBaseID: "kb-shared", TenantID: 2},
		&types.KnowledgeBase{ID: "kb-shared", TenantID: 2, CreatorID: "other"},
		&downloadKBShareStub{permission: types.OrgRoleViewer, source: 2},
	)

	rec := serveKnowledgeFile(t, engine, "/api/v1/knowledge/knowledge-shared/download")
	require.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())
}

func TestKnowledgeDownloadRejectsNonCreatorContributor(t *testing.T) {
	engine := newKnowledgeDownloadRouteTestEngine(
		t,
		types.TenantRoleContributor,
		"me",
		&types.Knowledge{ID: "knowledge-own", KnowledgeBaseID: "kb-own", TenantID: 1},
		&types.KnowledgeBase{ID: "kb-own", TenantID: 1, CreatorID: "other"},
		nil,
	)

	rec := serveKnowledgeFile(t, engine, "/api/v1/knowledge/knowledge-own/download")
	require.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())
}

func TestKnowledgeDownloadRejectsLegacyTenantOwnedKB(t *testing.T) {
	engine := newKnowledgeDownloadRouteTestEngine(
		t,
		types.TenantRoleContributor,
		"me",
		&types.Knowledge{ID: "knowledge-own", KnowledgeBaseID: "kb-own", TenantID: 1},
		&types.KnowledgeBase{ID: "kb-own", TenantID: 1, CreatorID: ""},
		nil,
	)

	rec := serveKnowledgeFile(t, engine, "/api/v1/knowledge/knowledge-own/download")
	require.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())
}

func TestKnowledgeDownloadAllowsCreator(t *testing.T) {
	engine := newKnowledgeDownloadRouteTestEngine(
		t,
		types.TenantRoleContributor,
		"me",
		&types.Knowledge{ID: "knowledge-own", KnowledgeBaseID: "kb-own", TenantID: 1},
		&types.KnowledgeBase{ID: "kb-own", TenantID: 1, CreatorID: "me"},
		nil,
	)

	rec := serveKnowledgeFile(t, engine, "/api/v1/knowledge/knowledge-own/download")
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
}

func TestKnowledgeDownloadAllowsAdmin(t *testing.T) {
	engine := newKnowledgeDownloadRouteTestEngine(
		t,
		types.TenantRoleAdmin,
		"me",
		&types.Knowledge{ID: "knowledge-own", KnowledgeBaseID: "kb-own", TenantID: 1},
		&types.KnowledgeBase{ID: "kb-own", TenantID: 1, CreatorID: "other"},
		nil,
	)

	rec := serveKnowledgeFile(t, engine, "/api/v1/knowledge/knowledge-own/download")
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
}

func TestKnowledgeDownloadAllowsSharedEditor(t *testing.T) {
	engine := newKnowledgeDownloadRouteTestEngine(
		t,
		types.TenantRoleContributor,
		"me",
		&types.Knowledge{ID: "knowledge-shared", KnowledgeBaseID: "kb-shared", TenantID: 2},
		&types.KnowledgeBase{ID: "kb-shared", TenantID: 2, CreatorID: "other"},
		&downloadKBShareStub{permission: types.OrgRoleEditor, source: 2},
	)

	rec := serveKnowledgeFile(t, engine, "/api/v1/knowledge/knowledge-shared/download")
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
}

func TestKnowledgePreviewRejectsNonCreatorContributor(t *testing.T) {
	engine := newKnowledgeDownloadRouteTestEngine(
		t,
		types.TenantRoleContributor,
		"me",
		&types.Knowledge{ID: "knowledge-own", KnowledgeBaseID: "kb-own", TenantID: 1},
		&types.KnowledgeBase{ID: "kb-own", TenantID: 1, CreatorID: "other"},
		nil,
	)

	rec := serveKnowledgeFile(t, engine, "/api/v1/knowledge/knowledge-own/preview")
	require.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())
}

func TestKnowledgePreviewRejectsReadOnlySharedKB(t *testing.T) {
	engine := newKnowledgeDownloadRouteTestEngine(
		t,
		types.TenantRoleContributor,
		"me",
		&types.Knowledge{ID: "knowledge-shared", KnowledgeBaseID: "kb-shared", TenantID: 2},
		&types.KnowledgeBase{ID: "kb-shared", TenantID: 2, CreatorID: "other"},
		&downloadKBShareStub{permission: types.OrgRoleViewer, source: 2},
	)

	rec := serveKnowledgeFile(t, engine, "/api/v1/knowledge/knowledge-shared/preview")
	require.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())
}

func TestKnowledgePreviewAllowsCreator(t *testing.T) {
	engine := newKnowledgeDownloadRouteTestEngine(
		t,
		types.TenantRoleContributor,
		"me",
		&types.Knowledge{ID: "knowledge-own", KnowledgeBaseID: "kb-own", TenantID: 1},
		&types.KnowledgeBase{ID: "kb-own", TenantID: 1, CreatorID: "me"},
		nil,
	)

	rec := serveKnowledgeFile(t, engine, "/api/v1/knowledge/knowledge-own/preview")
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
}
