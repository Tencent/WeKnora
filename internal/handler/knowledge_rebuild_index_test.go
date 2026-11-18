package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

// Regression test for #4607: the KB settings modal calls
// POST /api/v1/knowledge-bases/:id/rebuild-index after an indexing-strategy
// change, but the route did not exist on the backend.

type rebuildIndexKnowledge struct {
	interfaces.KnowledgeService
	rows []*types.Knowledge
	err  error
}

func (s *rebuildIndexKnowledge) ListKnowledgeByKnowledgeBaseID(
	context.Context, string,
) ([]*types.Knowledge, error) {
	return s.rows, s.err
}

type rebuildIndexEnqueuer struct{ tasks []*asynq.Task }

func (q *rebuildIndexEnqueuer) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	q.tasks = append(q.tasks, task)
	return &asynq.TaskInfo{ID: fmt.Sprintf("rebuild-task-%d", len(q.tasks))}, nil
}

func rebuildIndexTestHandler(kg *rebuildIndexKnowledge, q *rebuildIndexEnqueuer) *KnowledgeHandler {
	return &KnowledgeHandler{
		cfg:         transferHandlerConfig(),
		kbService:   transferHandlerKB(),
		kgService:   kg,
		asynqClient: q,
	}
}

func serveRebuildIndex(t *testing.T, h *KnowledgeHandler) (int, map[string]any) {
	t.Helper()
	r := documentHandlerRouter()
	r.POST("/:id/rebuild-index", h.RebuildKnowledgeBaseIndex)
	w := mutationRequest(r, http.MethodPost, "/kb/rebuild-index", "")
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return w.Code, body
}

func rebuildIndexDocs(n int) []*types.Knowledge {
	rows := make([]*types.Knowledge, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, &types.Knowledge{
			ID:              fmt.Sprintf("doc-%03d", i),
			TenantID:        7,
			KnowledgeBaseID: "kb",
		})
	}
	return rows
}

func decodeReparsePayload(t *testing.T, task *asynq.Task) types.KnowledgeListReparsePayload {
	t.Helper()
	var payload types.KnowledgeListReparsePayload
	require.NoError(t, json.Unmarshal(task.Payload(), &payload))
	return payload
}

func TestRebuildKnowledgeBaseIndexEnqueuesAllDocuments(t *testing.T) {
	kg := &rebuildIndexKnowledge{rows: rebuildIndexDocs(3)}
	q := &rebuildIndexEnqueuer{}
	code, body := serveRebuildIndex(t, rebuildIndexTestHandler(kg, q))

	require.Equal(t, http.StatusOK, code, body)
	require.Equal(t, true, body["success"])
	data := body["data"].(map[string]any)
	require.Equal(t, float64(3), data["document_count"])
	require.Len(t, data["task_ids"].([]any), 1)

	require.Len(t, q.tasks, 1)
	payload := decodeReparsePayload(t, q.tasks[0])
	require.Equal(t, "kb", payload.KnowledgeBaseID)
	require.Equal(t, uint64(7), payload.TenantID)
	require.Equal(t, []string{"doc-000", "doc-001", "doc-002"}, payload.KnowledgeIDs)
}

func TestRebuildKnowledgeBaseIndexChunksLargeKBs(t *testing.T) {
	kg := &rebuildIndexKnowledge{rows: rebuildIndexDocs(250)}
	q := &rebuildIndexEnqueuer{}
	code, body := serveRebuildIndex(t, rebuildIndexTestHandler(kg, q))

	require.Equal(t, http.StatusOK, code, body)
	data := body["data"].(map[string]any)
	require.Equal(t, float64(250), data["document_count"])
	require.Len(t, data["task_ids"].([]any), 2)

	// Same per-task admission cap as /knowledge/batch-reparse.
	require.Len(t, q.tasks, 2)
	first := decodeReparsePayload(t, q.tasks[0])
	second := decodeReparsePayload(t, q.tasks[1])
	require.Len(t, first.KnowledgeIDs, 200)
	require.Len(t, second.KnowledgeIDs, 50)
	require.Equal(t, "doc-000", first.KnowledgeIDs[0])
	require.Equal(t, "doc-249", second.KnowledgeIDs[49])
}

func TestRebuildKnowledgeBaseIndexRejectsMovingDocument(t *testing.T) {
	rows := rebuildIndexDocs(1)
	rows[0].Metadata = []byte(`{"_knowledge_transfer":{"operation":"move","phase":"moving"}}`)
	kg := &rebuildIndexKnowledge{rows: rows}
	q := &rebuildIndexEnqueuer{}
	code, _ := serveRebuildIndex(t, rebuildIndexTestHandler(kg, q))

	require.Equal(t, http.StatusConflict, code)
	require.Empty(t, q.tasks)
}

func TestRebuildKnowledgeBaseIndexRejectsEmptyKB(t *testing.T) {
	kg := &rebuildIndexKnowledge{rows: nil}
	q := &rebuildIndexEnqueuer{}
	code, _ := serveRebuildIndex(t, rebuildIndexTestHandler(kg, q))

	require.Equal(t, http.StatusBadRequest, code)
	require.Empty(t, q.tasks)
}
