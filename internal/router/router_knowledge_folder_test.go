package router

import (
	"context"
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

type folderRouteService struct {
	interfaces.KnowledgeFolderService
	treeCalls              int
	getCalls               int
	movedKnowledgeID       string
	movedKnowledgeFolderID string
}

func (s *folderRouteService) CreateFolder(
	_ context.Context, kbID string, req *types.CreateFolderRequest,
) (*types.KnowledgeFolder, error) {
	return &types.KnowledgeFolder{ID: "created", KnowledgeBaseID: kbID, ParentID: req.ParentID, Name: req.Name}, nil
}
func (s *folderRouteService) GetFolder(
	_ context.Context, kbID, folderID string,
) (*types.KnowledgeFolder, error) {
	s.getCalls++
	return &types.KnowledgeFolder{ID: folderID, KnowledgeBaseID: kbID}, nil
}
func (*folderRouteService) ListByParent(context.Context, string, string) ([]*types.KnowledgeFolder, error) {
	return []*types.KnowledgeFolder{}, nil
}
func (s *folderRouteService) GetTree(context.Context, string) ([]*types.KnowledgeFolder, error) {
	s.treeCalls++
	return []*types.KnowledgeFolder{}, nil
}
func (*folderRouteService) UpdateFolder(
	_ context.Context, kbID, folderID string, _ *types.UpdateFolderRequest,
) (*types.KnowledgeFolder, error) {
	return &types.KnowledgeFolder{ID: folderID, KnowledgeBaseID: kbID}, nil
}
func (*folderRouteService) DeleteFolder(context.Context, string, string, bool) error { return nil }
func (*folderRouteService) MoveFolder(
	_ context.Context, kbID, folderID string, _ *types.MoveFolderRequest,
) (*types.KnowledgeFolder, error) {
	return &types.KnowledgeFolder{ID: folderID, KnowledgeBaseID: kbID}, nil
}
func (s *folderRouteService) MoveKnowledgeToFolder(_ context.Context, knowledgeID, folderID string) error {
	s.movedKnowledgeID = knowledgeID
	s.movedKnowledgeFolderID = folderID
	return nil
}

func (*folderRouteService) GetBreadcrumb(
	_ context.Context, kbID, folderID string,
) ([]*types.KnowledgeFolder, error) {
	return []*types.KnowledgeFolder{{ID: folderID, KnowledgeBaseID: kbID}}, nil
}

type folderKBShareService struct {
	interfaces.KBShareService
	permission     types.OrgMemberRole
	sourceTenantID uint64
	checks         int
}

func (s *folderKBShareService) CheckTenantKBPermission(
	_ context.Context, _ string, _ uint64, _ types.TenantRole,
) (types.OrgMemberRole, bool, error) {
	s.checks++
	return s.permission, true, nil
}
func (s *folderKBShareService) GetKBSourceTenant(context.Context, string) (uint64, error) {
	return s.sourceTenantID, nil
}

type folderKBLookup struct {
	kbs map[string]*types.KnowledgeBase
}

func (s folderKBLookup) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	if kb := s.kbs[id]; kb != nil {
		return kb, nil
	}
	return nil, apprepo.ErrKnowledgeBaseNotFound
}

type folderKnowledgeLookup struct{ knowledges map[string]*types.Knowledge }

func (s folderKnowledgeLookup) GetKnowledgeByIDOnly(_ context.Context, id string) (*types.Knowledge, error) {
	if knowledge := s.knowledges[id]; knowledge != nil {
		return knowledge, nil
	}
	return nil, apprepo.ErrKnowledgeNotFound
}

type folderRoutePrincipal struct {
	role   types.TenantRole
	userID string
	tenant uint64
	scope  *types.TenantAPIKeyScope
}

func newKnowledgeFolderRouteEngine(
	t *testing.T,
	principal folderRoutePrincipal,
	kbs map[string]*types.KnowledgeBase,
	knowledges map[string]*types.Knowledge,
	share interfaces.KBShareService,
	svc interfaces.KnowledgeFolderService,
) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	enabled := true
	kbLookup := folderKBLookup{kbs: kbs}
	knowledgeLookup := folderKnowledgeLookup{knowledges: knowledges}
	g := &rbacGuards{
		cfg:              &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}},
		kbService:        kbLookup,
		knowledgeService: knowledgeLookup,
		kbShareService:   share,
	}
	g.kbCreator = func(c *gin.Context) (string, error) {
		kb, err := kbLookup.GetKnowledgeBaseByID(c.Request.Context(), c.Param("id"))
		if err != nil {
			return "", middleware.ErrResourceNotFound
		}
		return kb.CreatorID, nil
	}
	g.knowledgeKBCreator = func(c *gin.Context) (string, error) {
		knowledge, err := knowledgeLookup.GetKnowledgeByIDOnly(c.Request.Context(), c.Param("id"))
		if err != nil {
			return "", middleware.ErrResourceNotFound
		}
		kb, err := kbLookup.GetKnowledgeBaseByID(c.Request.Context(), knowledge.KnowledgeBaseID)
		if err != nil {
			return "", middleware.ErrResourceNotFound
		}
		return kb.CreatorID, nil
	}

	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, principal.tenant)
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, principal.role)
		ctx = context.WithValue(ctx, types.UserIDContextKey, principal.userID)
		if principal.scope != nil {
			ctx = types.WithTenantAPIKeyScope(ctx, *principal.scope)
		}
		c.Request = c.Request.WithContext(ctx)
		c.Set(types.TenantIDContextKey.String(), principal.tenant)
		c.Set(types.UserIDContextKey.String(), principal.userID)
		c.Next()
	})
	v1 := r.Group("/api/v1")
	v1.Use(g.ensureAPIKeyAuthorizer().Middleware())
	h, err := handler.NewKnowledgeFolderHandler(svc)
	require.NoError(t, err)
	folders := g.apiKeyGroup(v1.Group("/knowledge-bases/:id/folders"), apiKeyIngest(apiKeyFullAccess()))
	read := folders.With(apiKeyRetrieve(apiKeyFullAccess()))
	folders.POST("", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), h.CreateFolder)
	read.GET("", g.Viewer(), g.KBAccessRead("id"), h.ListFolders)
	read.GET("/tree", g.Viewer(), g.KBAccessRead("id"), h.GetTree)
	read.GET("/:folder_id/breadcrumb", g.Viewer(), g.KBAccessRead("id"), h.GetBreadcrumb)
	folders.POST("/:folder_id/move", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), h.MoveFolder)
	read.GET("/:folder_id", g.Viewer(), g.KBAccessRead("id"), h.GetFolder)
	folders.PUT("/:folder_id", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), h.UpdateFolder)
	folders.DELETE("/:folder_id", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), h.DeleteFolder)
	knowledge := g.apiKeyGroup(v1.Group("/knowledges"), apiKeyIngest(apiKeyFullAccess()))
	knowledge.PUT("/:id/folder", g.OwnedKnowledgeKBOrAdmin(), g.KBAccessWriteFromKnowledgeIDParam("id"), h.MoveKnowledgeToFolder)
	v1.GET("/undeclared", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	return r
}

func serveFolderRoute(t *testing.T, r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	r.ServeHTTP(rec, req)
	return rec
}

func TestKnowledgeFolderStaticRoutesHitTheirHandlers(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 1, CreatorID: "owner"}
	svc := &folderRouteService{}
	r := newKnowledgeFolderRouteEngine(t,
		folderRoutePrincipal{role: types.TenantRoleViewer, userID: "viewer", tenant: 1},
		map[string]*types.KnowledgeBase{kb.ID: kb}, nil, nil, svc)

	rec := serveFolderRoute(t, r, http.MethodGet, "/api/v1/knowledge-bases/kb-1/folders/tree", "")
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Equal(t, 1, svc.treeCalls)
	require.Zero(t, svc.getCalls, "/tree must not be captured as :folder_id")
}

func TestKnowledgeFolderRouteRBACMatrix(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 1, CreatorID: "owner"}
	reads := []struct{ name, method, path string }{
		{"list", http.MethodGet, "/api/v1/knowledge-bases/kb-1/folders"},
		{"get", http.MethodGet, "/api/v1/knowledge-bases/kb-1/folders/f-1"},
		{"tree", http.MethodGet, "/api/v1/knowledge-bases/kb-1/folders/tree"},
		{"breadcrumb", http.MethodGet, "/api/v1/knowledge-bases/kb-1/folders/f-1/breadcrumb"},
	}
	writes := []struct{ name, method, path, body string }{
		{"create", http.MethodPost, "/api/v1/knowledge-bases/kb-1/folders", `{"name":"x"}`},
		{"update", http.MethodPut, "/api/v1/knowledge-bases/kb-1/folders/f-1", `{"name":"x"}`},
		{"delete", http.MethodDelete, "/api/v1/knowledge-bases/kb-1/folders/f-1", ""},
		{"move", http.MethodPost, "/api/v1/knowledge-bases/kb-1/folders/f-1/move", `{"parent_id":""}`},
	}
	principals := []struct {
		name      string
		principal folderRoutePrincipal
		writeWant int
	}{
		{"viewer", folderRoutePrincipal{role: types.TenantRoleViewer, userID: "viewer", tenant: 1}, http.StatusForbidden},
		{"owning contributor", folderRoutePrincipal{role: types.TenantRoleContributor, userID: "owner", tenant: 1}, http.StatusOK},
		{"non-owning contributor", folderRoutePrincipal{role: types.TenantRoleContributor, userID: "other", tenant: 1}, http.StatusForbidden},
		{"admin", folderRoutePrincipal{role: types.TenantRoleAdmin, userID: "admin", tenant: 1}, http.StatusOK},
	}

	for _, p := range principals {
		r := newKnowledgeFolderRouteEngine(t, p.principal,
			map[string]*types.KnowledgeBase{kb.ID: kb}, nil, nil, &folderRouteService{})
		for _, route := range reads {
			t.Run(p.name+" reads "+route.name, func(t *testing.T) {
				rec := serveFolderRoute(t, r, route.method, route.path, "")
				require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
			})
		}
		for _, route := range writes {
			t.Run(p.name+" writes "+route.name, func(t *testing.T) {
				rec := serveFolderRoute(t, r, route.method, route.path, route.body)
				want := p.writeWant
				if want == http.StatusOK {
					switch route.name {
					case "create":
						want = http.StatusCreated
					case "delete":
						want = http.StatusNoContent
					}
				}
				require.Equal(t, want, rec.Code, "body=%s", rec.Body.String())
			})
		}
	}
}

func TestKnowledgeFolderRouteAPIKeyMatrix(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 1, CreatorID: "owner"}
	reads := []string{
		"/api/v1/knowledge-bases/kb-1/folders",
		"/api/v1/knowledge-bases/kb-1/folders/f-1",
		"/api/v1/knowledge-bases/kb-1/folders/tree",
		"/api/v1/knowledge-bases/kb-1/folders/f-1/breadcrumb",
	}
	writes := []struct {
		method, path, body string
		success            int
	}{
		{http.MethodPost, "/api/v1/knowledge-bases/kb-1/folders", `{"name":"x"}`, http.StatusCreated},
		{http.MethodPut, "/api/v1/knowledge-bases/kb-1/folders/f-1", `{"name":"x"}`, http.StatusOK},
		{http.MethodDelete, "/api/v1/knowledge-bases/kb-1/folders/f-1", "", http.StatusNoContent},
		{http.MethodPost, "/api/v1/knowledge-bases/kb-1/folders/f-1/move", `{"parent_id":""}`, http.StatusOK},
	}
	keys := []struct {
		name      string
		scope     types.TenantAPIKeyScope
		readWant  int
		writeWant bool
	}{
		{"retrieve", types.TenantAPIKeyScope{Capabilities: types.StringArray{"retrieve"}}, http.StatusOK, false},
		{"ingest", types.TenantAPIKeyScope{Capabilities: types.StringArray{"ingest"}}, http.StatusForbidden, true},
		{"full", types.TenantAPIKeyScope{FullAccess: true}, http.StatusOK, true},
	}
	for _, key := range keys {
		r := newKnowledgeFolderRouteEngine(t,
			folderRoutePrincipal{role: types.TenantRoleViewer, userID: "api", tenant: 1, scope: &key.scope},
			map[string]*types.KnowledgeBase{kb.ID: kb}, nil, nil, &folderRouteService{})
		for _, path := range reads {
			t.Run(key.name+" reads "+path, func(t *testing.T) {
				rec := serveFolderRoute(t, r, http.MethodGet, path, "")
				require.Equal(t, key.readWant, rec.Code, "body=%s", rec.Body.String())
			})
		}
		for _, route := range writes {
			t.Run(key.name+" writes "+route.path, func(t *testing.T) {
				rec := serveFolderRoute(t, r, route.method, route.path, route.body)
				want := http.StatusForbidden
				if key.writeWant {
					want = route.success
				}
				require.Equal(t, want, rec.Code, "body=%s", rec.Body.String())
			})
		}
	}
}

func TestKnowledgeFolderAPIKeyAllowListAndDefaultDeny(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 1, CreatorID: "owner"}
	scope := types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-other"},
		Capabilities:     types.StringArray{"retrieve", "ingest"},
	}
	r := newKnowledgeFolderRouteEngine(t,
		folderRoutePrincipal{role: types.TenantRoleViewer, userID: "api", tenant: 1, scope: &scope},
		map[string]*types.KnowledgeBase{kb.ID: kb}, nil, nil, &folderRouteService{})
	for _, tc := range []struct{ name, method, path, body string }{
		{"read allow-list mismatch", http.MethodGet, "/api/v1/knowledge-bases/kb-1/folders/tree", ""},
		{"write allow-list mismatch", http.MethodPost, "/api/v1/knowledge-bases/kb-1/folders", `{"name":"x"}`},
		{"undeclared route default deny", http.MethodGet, "/api/v1/undeclared", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := serveFolderRoute(t, r, tc.method, tc.path, tc.body)
			require.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())
		})
	}
}

func TestKnowledgeFolderSharedViewerWriteRejectedByKBAccessWrite(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "shared-kb", TenantID: 9, CreatorID: "shared-contributor"}
	share := &folderKBShareService{permission: types.OrgRoleViewer, sourceTenantID: 9}
	r := newKnowledgeFolderRouteEngine(t,
		folderRoutePrincipal{role: types.TenantRoleContributor, userID: "shared-contributor", tenant: 1},
		map[string]*types.KnowledgeBase{kb.ID: kb}, nil, share, &folderRouteService{})
	rec := serveFolderRoute(t, r, http.MethodPost,
		"/api/v1/knowledge-bases/shared-kb/folders", `{"name":"denied"}`)
	require.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())
	require.Equal(t, 1, share.checks, "ownership guard must pass and KBAccessWrite must perform the rejecting share check")
}

func TestMoveKnowledgeToFolderPropagatesPayload(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 1, CreatorID: "owner"}
	knowledge := &types.Knowledge{ID: "knowledge-1", KnowledgeBaseID: kb.ID, TenantID: 1}
	for _, tc := range []struct {
		name         string
		payload      string
		wantFolderID string
	}{
		{name: "named folder", payload: `{"folder_id":"folder-current"}`, wantFolderID: "folder-current"},
		{name: "root", payload: `{"folder_id":"__root__"}`, wantFolderID: types.FolderRootID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &folderRouteService{}
			r := newKnowledgeFolderRouteEngine(t,
				folderRoutePrincipal{role: types.TenantRoleAdmin, userID: "admin", tenant: 1},
				map[string]*types.KnowledgeBase{kb.ID: kb},
				map[string]*types.Knowledge{knowledge.ID: knowledge}, nil, svc)

			rec := serveFolderRoute(
				t, r, http.MethodPut, "/api/v1/knowledges/knowledge-1/folder", tc.payload,
			)

			require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
			require.Equal(t, knowledge.ID, svc.movedKnowledgeID)
			require.Equal(t, tc.wantFolderID, svc.movedKnowledgeFolderID)
		})
	}
}

func TestMoveKnowledgeToFolderGuardMatrix(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 1, CreatorID: "owner"}
	knowledge := &types.Knowledge{ID: "knowledge-1", KnowledgeBaseID: kb.ID, TenantID: 1}
	tests := []struct {
		name      string
		principal folderRoutePrincipal
		kbIDs     types.StringArray
		want      int
	}{
		{"viewer", folderRoutePrincipal{role: types.TenantRoleViewer, userID: "viewer", tenant: 1}, nil, http.StatusForbidden},
		{"non-owning contributor", folderRoutePrincipal{role: types.TenantRoleContributor, userID: "other", tenant: 1}, nil, http.StatusForbidden},
		{"owning contributor", folderRoutePrincipal{role: types.TenantRoleContributor, userID: "owner", tenant: 1}, nil, http.StatusOK},
		{"admin", folderRoutePrincipal{role: types.TenantRoleAdmin, userID: "admin", tenant: 1}, nil, http.StatusOK},
		{"retrieve key", folderRoutePrincipal{role: types.TenantRoleViewer, userID: "api", tenant: 1, scope: &types.TenantAPIKeyScope{Capabilities: types.StringArray{"retrieve"}}}, nil, http.StatusForbidden},
		{"ingest key", folderRoutePrincipal{role: types.TenantRoleViewer, userID: "api", tenant: 1, scope: &types.TenantAPIKeyScope{Capabilities: types.StringArray{"ingest"}}}, nil, http.StatusOK},
		{"full key", folderRoutePrincipal{role: types.TenantRoleViewer, userID: "api", tenant: 1, scope: &types.TenantAPIKeyScope{FullAccess: true}}, nil, http.StatusOK},
		{"allow-list mismatch", folderRoutePrincipal{role: types.TenantRoleViewer, userID: "api", tenant: 1, scope: &types.TenantAPIKeyScope{Capabilities: types.StringArray{"ingest"}, KnowledgeBaseIDs: types.StringArray{"kb-other"}}}, nil, http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newKnowledgeFolderRouteEngine(t, tc.principal,
				map[string]*types.KnowledgeBase{kb.ID: kb},
				map[string]*types.Knowledge{knowledge.ID: knowledge}, nil, &folderRouteService{})
			rec := serveFolderRoute(t, r, http.MethodPut, "/api/v1/knowledges/knowledge-1/folder", `{"folder_id":"__root__"}`)
			require.Equal(t, tc.want, rec.Code, "body=%s", rec.Body.String())
		})
	}

	t.Run("missing knowledge returns 404 before stub", func(t *testing.T) {
		r := newKnowledgeFolderRouteEngine(t,
			folderRoutePrincipal{role: types.TenantRoleAdmin, userID: "admin", tenant: 1},
			map[string]*types.KnowledgeBase{kb.ID: kb}, nil, nil, &folderRouteService{})
		rec := serveFolderRoute(t, r, http.MethodPut, "/api/v1/knowledges/missing/folder", `{"folder_id":"__root__"}`)
		require.Equal(t, http.StatusNotFound, rec.Code, "body=%s", rec.Body.String())
	})
}
