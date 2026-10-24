package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

// rebuildEnqueuer captures every enqueued task so tests can assert batching
// and payload contents across multiple enqueues.
type rebuildEnqueuer struct{ tasks []*asynq.Task }

func (q *rebuildEnqueuer) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	q.tasks = append(q.tasks, task)
	return &asynq.TaskInfo{ID: fmt.Sprintf("rebuild-task-%d", len(q.tasks))}, nil
}

func rebuildRows(n int, movingIdx int) []*types.Knowledge {
	rows := make([]*types.Knowledge, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, admissionKnowledge(i == movingIdx))
	}
	// admissionKnowledge hard-codes ID "doc"; make every row unique so the
	// enqueued payload carries exactly the listed documents.
	for i, row := range rows {
		row.ID = fmt.Sprintf("doc-%03d", i)
	}
	return rows
}

func rebuildPayloads(t *testing.T, queue *rebuildEnqueuer) []types.KnowledgeListReparsePayload {
	t.Helper()
	payloads := make([]types.KnowledgeListReparsePayload, 0, len(queue.tasks))
	for _, task := range queue.tasks {
		require.Equal(t, types.TypeKnowledgeListReparse, task.Type())
		var payload types.KnowledgeListReparsePayload
		require.NoError(t, json.Unmarshal(task.Payload(), &payload))
		payloads = append(payloads, payload)
	}
	return payloads
}

func rebuildHandlerRouter(role types.TenantRole) *gin.Engine {
	r := gin.New()
	r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
		ctx := types.WithCaller(
			c.Request.Context(),
			types.Caller{TenantID: 7, UserID: "user", Role: role},
		)
		ctx = types.WithExecutionTenant(ctx, 7)
		c.Set(types.TenantIDContextKey.String(), uint64(7))
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	return r
}

func TestRebuildKBIndexEnqueuesAllNonMovingDocuments(t *testing.T) {
	kg := &mutationAdmissionKnowledge{rows: rebuildRows(4, 1)} // doc-001 is moving
	queue := &rebuildEnqueuer{}
	h := &KnowledgeBaseHandler{
		cfg:              transferHandlerConfig(),
		service:          admissionKB("user"),
		knowledgeService: kg,
		asynqClient:      queue,
	}
	r := rebuildHandlerRouter(types.TenantRoleContributor)
	r.POST("/kb/:id/rebuild-index", h.RebuildKBIndex)

	req := httptest.NewRequest(http.MethodPost, "/kb/kb/rebuild-index", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code, w.Body.String())

	var body struct {
		Success bool `json:"success"`
		Data    struct {
			DocumentCount int    `json:"document_count"`
			SkippedMoving int    `json:"skipped_moving"`
			TaskCount     int    `json:"task_count"`
			TaskID        string `json:"task_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.Equal(t, 3, body.Data.DocumentCount)
	require.Equal(t, 1, body.Data.SkippedMoving)
	require.Equal(t, 1, body.Data.TaskCount)
	require.Equal(t, "rebuild-task-1", body.Data.TaskID)

	payloads := rebuildPayloads(t, queue)
	require.Len(t, payloads, 1)
	require.Equal(t, "kb", payloads[0].KnowledgeBaseID)
	require.Equal(t, uint64(7), payloads[0].TenantID)
	require.Equal(t, []string{"doc-000", "doc-002", "doc-003"}, payloads[0].KnowledgeIDs)
	require.Nil(t, payloads[0].ProcessConfig, "rebuild keeps each document's upload-time parse overrides")
}

func TestRebuildKBIndexChunksLargeKnowledgeBases(t *testing.T) {
	kg := &mutationAdmissionKnowledge{rows: rebuildRows(401, -1)}
	queue := &rebuildEnqueuer{}
	h := &KnowledgeBaseHandler{
		cfg:              transferHandlerConfig(),
		service:          admissionKB("user"),
		knowledgeService: kg,
		asynqClient:      queue,
	}
	r := rebuildHandlerRouter(types.TenantRoleContributor)
	r.POST("/kb/:id/rebuild-index", h.RebuildKBIndex)

	req := httptest.NewRequest(http.MethodPost, "/kb/kb/rebuild-index", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code, w.Body.String())

	var body struct {
		Data struct {
			DocumentCount int `json:"document_count"`
			TaskCount     int `json:"task_count"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, 401, body.Data.DocumentCount)
	require.Equal(t, 3, body.Data.TaskCount)

	payloads := rebuildPayloads(t, queue)
	require.Len(t, payloads, 3)
	require.Len(t, payloads[0].KnowledgeIDs, 200)
	require.Len(t, payloads[1].KnowledgeIDs, 200)
	require.Len(t, payloads[2].KnowledgeIDs, 1)
}

func TestRebuildKBIndexRejectsForeignTenantWithoutShare(t *testing.T) {
	kg := &mutationAdmissionKnowledge{rows: rebuildRows(2, -1)}
	queue := &rebuildEnqueuer{}
	h := &KnowledgeBaseHandler{
		cfg:              transferHandlerConfig(),
		service:          admissionKB("user"),
		knowledgeService: kg,
		asynqClient:      queue,
	}
	// Caller from another tenant with no KB share: ResolveKB grants nothing.
	r := gin.New()
	r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
		ctx := types.WithCaller(
			c.Request.Context(),
			types.Caller{TenantID: 8, UserID: "user", Role: types.TenantRoleAdmin},
		)
		ctx = types.WithExecutionTenant(ctx, 8)
		c.Set(types.TenantIDContextKey.String(), uint64(8))
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.POST("/kb/:id/rebuild-index", h.RebuildKBIndex)

	req := httptest.NewRequest(http.MethodPost, "/kb/kb/rebuild-index", strings.NewReader(""))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 403, w.Code, w.Body.String())
	require.Empty(t, queue.tasks)
}

func TestRebuildKBIndexEmptyKnowledgeBaseIsANoOp(t *testing.T) {
	kg := &mutationAdmissionKnowledge{rows: nil}
	queue := &rebuildEnqueuer{}
	h := &KnowledgeBaseHandler{
		cfg:              transferHandlerConfig(),
		service:          admissionKB("user"),
		knowledgeService: kg,
		asynqClient:      queue,
	}
	r := rebuildHandlerRouter(types.TenantRoleContributor)
	r.POST("/kb/:id/rebuild-index", h.RebuildKBIndex)

	req := httptest.NewRequest(http.MethodPost, "/kb/kb/rebuild-index", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code, w.Body.String())

	var body struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, float64(0), body.Data["document_count"])
	require.Equal(t, float64(0), body.Data["task_count"])
	require.NotContains(t, body.Data, "task_id")
	require.Empty(t, queue.tasks)
}
