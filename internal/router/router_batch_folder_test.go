package router

import (
	"context"
	"net/http"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type batchRouteEnqueuer struct{ calls int }

func (e *batchRouteEnqueuer) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	e.calls++
	return &asynq.TaskInfo{ID: "task-" + task.Type()}, nil
}

type batchRouteKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *batchRouteKBService) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	if s.kb != nil && s.kb.ID == id {
		return s.kb, nil
	}
	return nil, apprepo.ErrKnowledgeBaseNotFound
}

type batchRouteKnowledgeService struct {
	interfaces.KnowledgeService
	kbID      string
	knowledge map[string]*types.Knowledge
	writes    int
}

func (s *batchRouteKnowledgeService) ResolveBatchKnowledgeScope(
	_ context.Context, _ string, explicitIDs, _ []string, _ bool,
) ([]string, error) {
	return append([]string(nil), explicitIDs...), nil
}

func (s *batchRouteKnowledgeService) GetKnowledgeBatch(
	_ context.Context, _ uint64, ids []string,
) ([]*types.Knowledge, error) {
	out := make([]*types.Knowledge, 0, len(ids))
	for _, id := range ids {
		if knowledge := s.knowledge[id]; knowledge != nil {
			out = append(out, knowledge)
		}
	}
	return out, nil
}

func (s *batchRouteKnowledgeService) GetKnowledgeByIDOnly(
	_ context.Context, id string,
) (*types.Knowledge, error) {
	if knowledge := s.knowledge[id]; knowledge != nil {
		return knowledge, nil
	}
	return nil, apprepo.ErrKnowledgeNotFound
}

func (s *batchRouteKnowledgeService) UpdateKnowledgeTagBatch(
	_ context.Context, kbID string, updates map[string][]string,
) error {
	if kbID != s.kbID || len(updates) == 0 {
		return types.ErrInvalidArgument
	}
	s.writes++
	return nil
}

func (s *batchRouteKnowledgeService) MoveBatchToFolder(
	_ context.Context, kbID string, knowledgeIDs, folderIDs []string, _ string,
) error {
	if kbID != s.kbID || len(knowledgeIDs)+len(folderIDs) == 0 {
		return types.ErrInvalidArgument
	}
	s.writes++
	return nil
}

func newBatchRouteEngine(
	t *testing.T, principal folderRoutePrincipal, creatorID string,
) (*gin.Engine, *batchRouteKnowledgeService, *batchRouteEnqueuer) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	enabled := true
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 1, CreatorID: creatorID}
	service := &batchRouteKnowledgeService{
		kbID: "kb-1",
		knowledge: map[string]*types.Knowledge{
			"knowledge-1": {ID: "knowledge-1", TenantID: 1, KnowledgeBaseID: "kb-1"},
		},
	}
	enqueuer := &batchRouteEnqueuer{}
	cfg := &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}
	kbService := &batchRouteKBService{kb: kb}
	h := handler.NewKnowledgeHandler(cfg, service, kbService, nil, nil, enqueuer, nil)
	g := newRBACGuards(cfg, nil, nil, h, nil, nil, kbService, service, nil, nil, nil)

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
	RegisterKnowledgeRoutes(v1, h, g)
	return r, service, enqueuer
}

type batchRouteCase struct {
	name, method, path, body string
	isDelete                 bool
}

var batchRouteCases = []batchRouteCase{
	{"delete", http.MethodPost, "/api/v1/knowledge/batch-delete", `{"kb_id":"kb-1","knowledge_ids":["knowledge-1"]}`, true},
	{"reparse", http.MethodPost, "/api/v1/knowledge/batch-reparse", `{"kb_id":"kb-1","knowledge_ids":["knowledge-1"]}`, false},
	{"tags", http.MethodPut, "/api/v1/knowledge/tags", `{"kb_id":"kb-1","knowledge_ids":["knowledge-1"],"tag_ids":["tag-1"]}`, false},
	{"move folder", http.MethodPost, "/api/v1/knowledges/batch-move-folder", `{"kb_id":"kb-1","knowledge_ids":["knowledge-1"],"target_folder_id":"folder-2"}`, false},
}

func TestBatchKnowledgeRoutesJWTGuardMatrix(t *testing.T) {
	principals := []struct {
		name      string
		principal folderRoutePrincipal
		want      func(batchRouteCase) int
	}{
		{"viewer", folderRoutePrincipal{role: types.TenantRoleViewer, userID: "viewer", tenant: 1}, func(batchRouteCase) int { return http.StatusForbidden }},
		{"owning contributor", folderRoutePrincipal{role: types.TenantRoleContributor, userID: "owner", tenant: 1}, func(batchRouteCase) int { return http.StatusOK }},
		{"non-owning contributor", folderRoutePrincipal{role: types.TenantRoleContributor, userID: "other", tenant: 1}, func(route batchRouteCase) int {
			if route.isDelete {
				return http.StatusForbidden
			}
			return http.StatusOK
		}},
		{"admin", folderRoutePrincipal{role: types.TenantRoleAdmin, userID: "admin", tenant: 1}, func(batchRouteCase) int { return http.StatusOK }},
	}
	for _, principal := range principals {
		for _, route := range batchRouteCases {
			t.Run(principal.name+" "+route.name, func(t *testing.T) {
				r, _, _ := newBatchRouteEngine(t, principal.principal, "owner")
				rec := serveFolderRoute(t, r, route.method, route.path, route.body)
				require.Equal(t, principal.want(route), rec.Code, "body=%s", rec.Body.String())
			})
		}
	}
}

func TestBatchKnowledgeRoutesAPIKeyCapabilities(t *testing.T) {
	keys := []struct {
		name  string
		scope types.TenantAPIKeyScope
		want  int
	}{
		{"retrieve rejected", types.TenantAPIKeyScope{Capabilities: types.StringArray{"retrieve"}}, http.StatusForbidden},
		{"ingest allowed", types.TenantAPIKeyScope{Capabilities: types.StringArray{"ingest"}}, http.StatusOK},
		{"full allowed", types.TenantAPIKeyScope{FullAccess: true}, http.StatusOK},
		{"allow-list mismatch", types.TenantAPIKeyScope{
			Capabilities: types.StringArray{"ingest"}, KnowledgeBaseIDs: types.StringArray{"kb-other"},
		}, http.StatusForbidden},
	}
	for _, key := range keys {
		for _, route := range batchRouteCases {
			t.Run(key.name+" "+route.name, func(t *testing.T) {
				principal := folderRoutePrincipal{
					role: types.TenantRoleViewer, userID: "api", tenant: 1, scope: &key.scope,
				}
				r, _, _ := newBatchRouteEngine(t, principal, "owner")
				rec := serveFolderRoute(t, r, route.method, route.path, route.body)
				require.Equal(t, key.want, rec.Code, "body=%s", rec.Body.String())
			})
		}
	}
}
