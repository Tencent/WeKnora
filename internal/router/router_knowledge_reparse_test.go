package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type reparseRouteKnowledge struct {
	interfaces.KnowledgeService
	singleIDs []string
	overrides *types.KnowledgeProcessOverrides
}

func (*reparseRouteKnowledge) GetOwningKBCreatorID(context.Context, string) (string, error) {
	return "user", nil
}

func (s *reparseRouteKnowledge) GetKnowledgeByIDOnly(_ context.Context, id string) (*types.Knowledge, error) {
	kbID := "kb"
	if id == "foreign" {
		kbID = "other"
	}
	return &types.Knowledge{ID: id, KnowledgeBaseID: kbID, TenantID: 7}, nil
}

func (s *reparseRouteKnowledge) GetKnowledgeBatch(ctx context.Context, _ uint64, ids []string) ([]*types.Knowledge, error) {
	var rows []*types.Knowledge
	for _, id := range ids {
		row, _ := s.GetKnowledgeByIDOnly(ctx, id)
		rows = append(rows, row)
	}
	return rows, nil
}

func (s *reparseRouteKnowledge) ReparseKnowledge(ctx context.Context, id string, overrides *types.KnowledgeProcessOverrides) (*types.Knowledge, error) {
	s.singleIDs = append(s.singleIDs, id)
	s.overrides = overrides
	return s.GetKnowledgeByIDOnly(ctx, id)
}

type reparseRouteKB struct {
	interfaces.KnowledgeBaseService
}

func (*reparseRouteKB) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{ID: id, TenantID: 7, CreatorID: "user"}, nil
}

type reparseRouteQueue struct{ tasks []*asynq.Task }

func (q *reparseRouteQueue) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	q.tasks = append(q.tasks, task)
	return &asynq.TaskInfo{ID: "reparse-task"}, nil
}

func TestKnowledgeReparseRoutes(t *testing.T) {
	cases := []struct {
		name, path, body string
		role             types.TenantRole
		status           int
		batch, override  bool
	}{
		{name: "scoped_single", path: "/knowledge-bases/kb/knowledge/doc/reparse", status: 200},
		{name: "scoped_single_overrides", path: "/knowledge-bases/kb/knowledge/doc/reparse", body: `{"process_config":{"summary_enabled":false}}`, status: 200, override: true},
		{name: "scoped_batch", path: "/knowledge-bases/kb/knowledge/batch-reparse", body: `{"ids":["doc"],"process_config":{"summary_enabled":false}}`, status: 200, batch: true, override: true},
		{name: "scoped_batch_matching_body", path: "/knowledge-bases/kb/knowledge/batch-reparse", body: `{"kb_id":"kb","ids":["doc"]}`, status: 200, batch: true},
		{name: "canonical_single", path: "/knowledge/doc/reparse", status: 200},
		{name: "canonical_batch", path: "/knowledge/batch-reparse", body: `{"kb_id":"kb","ids":["doc"]}`, status: 200, batch: true},
		{name: "canonical_batch_requires_kb", path: "/knowledge/batch-reparse", body: `{"ids":["doc"]}`, status: 400},
		{name: "scoped_single_wrong_kb", path: "/knowledge-bases/kb/knowledge/foreign/reparse", status: 400},
		{name: "scoped_batch_wrong_document", path: "/knowledge-bases/kb/knowledge/batch-reparse", body: `{"ids":["foreign"]}`, status: 400},
		{name: "scoped_batch_wrong_body_kb", path: "/knowledge-bases/kb/knowledge/batch-reparse", body: `{"kb_id":"other","ids":["doc"]}`, status: 400},
		{name: "scoped_single_viewer", path: "/knowledge-bases/kb/knowledge/doc/reparse", role: types.TenantRoleViewer, status: 403},
		{name: "scoped_batch_viewer", path: "/knowledge-bases/kb/knowledge/batch-reparse", body: `{"ids":["doc"]}`, role: types.TenantRoleViewer, status: 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			enabled := true
			cfg := &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}
			kg, kb, queue := &reparseRouteKnowledge{}, &reparseRouteKB{}, &reparseRouteQueue{}
			h := handler.NewKnowledgeHandler(cfg, kg, kb, nil, nil, nil, nil, nil, queue, nil, nil)
			kbHandler := handler.NewKnowledgeBaseHandler(cfg, kb, kg, nil, nil, queue, nil, nil, nil, nil, nil)
			guards := newRBACGuards(cfg, kbHandler, nil, h, nil, nil, kb, kg, nil, nil, nil)
			role := tc.role
			if role == "" {
				role = types.TenantRoleContributor
			}
			r := gin.New()
			r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
				ctx := types.WithCaller(c.Request.Context(), types.Caller{TenantID: 7, UserID: "user", Role: role})
				c.Request = c.Request.WithContext(types.WithExecutionTenant(ctx, 7))
				c.Set(types.TenantIDContextKey.String(), uint64(7))
				c.Next()
			})
			v1 := r.Group("/api/v1", guards.apiKeyAuthorizer.Middleware())
			RegisterKnowledgeRoutes(v1, h, guards)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1"+tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code, "body=%s", w.Body.String())
			if tc.status != http.StatusOK {
				require.Empty(t, kg.singleIDs)
				require.Empty(t, queue.tasks)
				return
			}
			if tc.batch {
				require.Empty(t, kg.singleIDs)
				require.Len(t, queue.tasks, 1)
				var payload types.KnowledgeListReparsePayload
				require.NoError(t, json.Unmarshal(queue.tasks[0].Payload(), &payload))
				require.Equal(t, "kb", payload.KnowledgeBaseID)
				require.Equal(t, uint64(7), payload.TenantID)
				require.Equal(t, []string{"doc"}, payload.KnowledgeIDs)
				if tc.override {
					require.NotNil(t, payload.ProcessConfig)
					require.NotNil(t, payload.ProcessConfig.SummaryEnabled)
					require.False(t, *payload.ProcessConfig.SummaryEnabled)
				}
			} else {
				require.Equal(t, []string{"doc"}, kg.singleIDs)
				require.Empty(t, queue.tasks)
				if tc.override {
					require.NotNil(t, kg.overrides)
					require.NotNil(t, kg.overrides.SummaryEnabled)
					require.False(t, *kg.overrides.SummaryEnabled)
				}
			}
		})
	}
}
