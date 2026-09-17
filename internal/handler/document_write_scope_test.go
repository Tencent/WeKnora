package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type documentWriteHandlerKnowledge struct {
	interfaces.KnowledgeService
	called bool
}

func (*documentWriteHandlerKnowledge) GetKnowledgeByIDOnly(context.Context, string) (*types.Knowledge, error) {
	return &types.Knowledge{ID: "doc", TenantID: 7, KnowledgeBaseID: "kb"}, nil
}

func (s *documentWriteHandlerKnowledge) UpdateKnowledgeTagBatch(
	ctx context.Context,
	kb string,
	_ map[string][]string,
) error {
	if err := access.RequireKBWrite(ctx, &types.KnowledgeBase{ID: kb, TenantID: 7}); err != nil {
		return err
	}
	s.called = true
	return nil
}

type documentDeleteEnqueuer struct{ task *asynq.Task }

func (q *documentDeleteEnqueuer) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	q.task = task
	return &asynq.TaskInfo{ID: "delete-task"}, nil
}

func documentHandlerRouter() *gin.Engine {
	r := gin.New()
	r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
		ctx := types.WithCaller(
			c.Request.Context(),
			types.Caller{TenantID: 7, UserID: "user", Role: types.TenantRoleAdmin},
		)
		ctx = types.WithExecutionTenant(ctx, 7)
		c.Set(types.TenantIDContextKey.String(), uint64(7))
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	return r
}

func TestDocumentTagBodyRouteIssuesWriteGrant(t *testing.T) {
	for _, body := range []string{`{"kb_id":"kb","updates":{"doc":["tag"]}}`, `{"updates":{"doc":["tag"]}}`} {
		t.Run(body, func(t *testing.T) {
			kg := &documentWriteHandlerKnowledge{}
			h := &KnowledgeHandler{
				kgService: kg,
				kbService: &stubKBService{get: func(context.Context, string) (*types.KnowledgeBase, error) {
					return &types.KnowledgeBase{ID: "kb", TenantID: 7}, nil
				}},
			}
			r := documentHandlerRouter()
			r.PUT("/tags", h.UpdateKnowledgeTagBatch)
			req := httptest.NewRequest(http.MethodPut, "/tags", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, 200, w.Code, w.Body.String())
			require.True(t, kg.called)
		})
	}
}

func TestSingleDocumentDeletePersistsAuthorizedKBInTask(t *testing.T) {
	q := &documentDeleteEnqueuer{}
	h := &KnowledgeHandler{kgService: &documentWriteHandlerKnowledge{}, asynqClient: q}
	r := documentHandlerRouter()
	r.DELETE("/:id", h.DeleteKnowledge)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/doc", nil))
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NotNil(t, q.task)
	var payload types.KnowledgeListDeletePayload
	require.NoError(t, json.Unmarshal(q.task.Payload(), &payload))
	require.Equal(t, uint64(7), payload.TenantID)
	require.Equal(t, "kb", payload.KnowledgeBaseID)
	require.Equal(t, []string{"doc"}, payload.KnowledgeIDs)
}
