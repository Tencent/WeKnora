package tools

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDataSourceSyncAlwaysConfirmsAndReturnsProgressHandle(t *testing.T) {
	for _, approved := range []bool{false, true} {
		t.Run(map[bool]string{true: "approved", false: "declined"}[approved], func(t *testing.T) {
			d := &fakeDispatcher{responses: map[string]*operations.Response{
				"GET /api/v1/datasource/ds1": {
					Status: 200,
					Body:   []byte(`{"id":"ds1","name":"Company docs","sync_deletions":true}`),
				},
				"POST /api/v1/datasource/ds1/sync": {
					Status: 200,
					Body:   []byte(`{"id":"log1","data_source_id":"ds1","status":"running"}`),
				},
			}}
			gate := &fakeApprover{decision: approval.Decision{Approved: approved, PendingID: "p1"}}
			tool := newRunTool(d, gate)
			tool.rt.Access = operations.Access{
				Level:       types.AssistantAccessAutoWrite,
				Role:        types.TenantRoleAdmin,
				EnforceRole: true,
			}
			res, err := tool.Execute(
				interactiveCtx(),
				runArgs("data_source.sync", map[string]any{"data_source_id": "ds1"}),
			)
			require.NoError(t, err)
			require.Len(t, gate.requests, 1)
			assert.Contains(t, gate.requests[0].Description, "Company docs")
			assert.Contains(t, gate.requests[0].Description, "remove documents deleted upstream")
			assert.Empty(t, gate.requests[0].Grant)
			assert.Equal(t, approved, res.Success, res.Error)
			if approved {
				require.Len(t, d.calls, 2)
				assert.Contains(t, res.Output, `"sync_log_id":"log1"`)
				assert.Contains(t, res.Output, `"status":"running"`)
			} else {
				require.Len(t, d.calls, 1)
				assert.Empty(t, res.Data)
			}
		})
	}
}

func TestCommonManagementRefusesUnavailableWritesBeforeDispatch(t *testing.T) {
	for _, tc := range []struct {
		op     string
		access operations.Access
	}{
		{
			"data_source.sync",
			operations.Access{
				Level:       types.AssistantAccessAutoWrite,
				Role:        types.TenantRoleContributor,
				EnforceRole: true,
			},
		},
		{
			"data_source.test",
			operations.Access{
				Level:       types.AssistantAccessReadOnly,
				Role:        types.TenantRoleViewer,
				EnforceRole: true,
			},
		},
		{
			"data_source.resume",
			operations.Access{
				Level:       types.AssistantAccessReadOnly,
				Role:        types.TenantRoleAdmin,
				EnforceRole: true,
			},
		},
		{"chunk.update", operations.Access{Level: types.AssistantAccessReadOnly}},
		{"knowledge_base.copy", operations.Access{Level: types.AssistantAccessReadOnly}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			d := &fakeDispatcher{}
			gate := &fakeApprover{}
			tool := newRunTool(d, gate)
			tool.rt.Access = tc.access
			res, err := tool.Execute(interactiveCtx(), runArgs(tc.op, map[string]any{}))
			require.NoError(t, err)
			assert.False(t, res.Success)
			assert.Contains(t, res.Error, "unavailable")
			assert.Empty(t, d.calls)
			assert.Empty(t, gate.requests)
		})
	}
}

func TestChunkUpdateRefusesTruncatedContentInEveryWriteMode(t *testing.T) {
	for _, level := range []types.AssistantAccessLevel{types.AssistantAccessConfirm, types.AssistantAccessAutoWrite} {
		d := &fakeDispatcher{responses: map[string]*operations.Response{
			"GET /api/v1/chunks/by-id/c1": {
				Status: 200,
				Body: []byte(
					`{"data":{"id":"c1","content":"` + strings.Repeat("x", 8001) + `","content_revision":1}}`,
				),
			},
		}}
		gate := &fakeApprover{decision: approval.Decision{Approved: true}}
		tool := newRunTool(d, gate)
		tool.rt.Access.Level = level
		res, err := tool.Execute(
			interactiveCtx(),
			runArgs(
				"chunk.update",
				map[string]any{
					"knowledge_id":      "k1",
					"chunk_id":          "c1",
					"expected_revision": 1,
					"content":           "replacement",
				},
			),
		)
		require.NoError(t, err)
		assert.False(t, res.Success)
		assert.Contains(t, res.Error, "truncated")
		require.Len(t, d.calls, 1)
		assert.Equal(t, http.MethodGet, d.calls[0].call.Method)
		assert.Empty(t, gate.requests)
	}
}

func TestChunkUpdateConflictDoesNotRetryOrEmitChange(t *testing.T) {
	d := &fakeDispatcher{responses: map[string]*operations.Response{
		"GET /api/v1/chunks/by-id/c1": {
			Status: 200,
			Body:   []byte(`{"data":{"id":"c1","content":"current","content_revision":2}}`),
		},
		"PUT /api/v1/chunks/k1/c1": {
			Status: 409,
			Body:   []byte(`{"error":{"message":"Chunk was modified by another user; refresh and retry"}}`),
		},
	}}
	gate := &fakeApprover{}
	tool := newRunTool(d, gate)
	tool.rt.Access.Level = types.AssistantAccessAutoWrite
	res, err := tool.Execute(
		interactiveCtx(),
		runArgs(
			"chunk.update",
			map[string]any{"knowledge_id": "k1", "chunk_id": "c1", "expected_revision": 1, "content": "replacement"},
		),
	)
	require.NoError(t, err)
	assert.False(t, res.Success)
	assert.Contains(t, res.Error, "409")
	assert.Empty(t, res.Data)
	require.Len(t, d.calls, 2)
	assert.JSONEq(t, `{"expected_revision":1,"content":"replacement"}`, string(d.calls[1].call.Body))
}

func TestChunkUpdateApprovalAndRefreshScope(t *testing.T) {
	d := &fakeDispatcher{responses: map[string]*operations.Response{
		"GET /api/v1/chunks/by-id/c1": {
			Status: 200,
			Body:   []byte(`{"data":{"id":"c1","content":"current","content_revision":2}}`),
		},
		"GET /api/v1/knowledge/k1": {Status: 200, Body: []byte(`{"data":{"id":"k1","title":"Handbook"}}`)},
		"PUT /api/v1/chunks/k1/c1": {
			Status: 200,
			Body:   []byte(`{"data":{"id":"c1","knowledge_id":"k1","content_revision":3,"is_enabled":false}}`),
		},
	}}
	gate := &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p1"}}
	res, err := newRunTool(
		d,
		gate,
	).Execute(
		interactiveCtx(),
		runArgs(
			"chunk.update",
			map[string]any{
				"knowledge_id":      "k1",
				"chunk_id":          "c1",
				"expected_revision": 2,
				"is_enabled":        false,
			},
		),
	)
	require.NoError(t, err)
	require.True(t, res.Success, res.Error)
	require.Len(t, gate.requests, 1)
	assert.Contains(t, gate.requests[0].Description, "Handbook")
	assert.Contains(t, gate.requests[0].Description, "Enabled: false")
	assert.Contains(t, gate.requests[0].Description, "Current revision: 2")
	data, err := json.Marshal(res.Data)
	require.NoError(t, err)
	assert.JSONEq(t, `{
  "data_change": {
    "operation": "chunk.update",
    "area": "chunk",
    "ids": {
      "knowledge_id": [
        "k1"
      ]
    }
  }
}`, string(data))
}

func TestKnowledgeBaseCopyRefreshIncludesNewDestination(t *testing.T) {
	for _, name := range []string{"knowledge_base.copy", "knowledge_base.duplicate"} {
		op, _ := operations.Builtin().Get(name)
		change := operationDataChange(
			op,
			map[string]json.RawMessage{"knowledge_base_id": json.RawMessage(`"source"`)},
			operations.Projected{Data: map[string]any{"knowledge_base_id": "new"}},
		)
		assert.Equal(t, map[string][]string{"knowledge_base_id": {"source", "new"}}, change["ids"])
	}
}

func TestKnowledgeBaseCopyProgressRefreshesOnlyAtTerminalState(t *testing.T) {
	for _, status := range []string{"pending", "processing", "completed", "failed"} {
		d := &fakeDispatcher{responses: map[string]*operations.Response{
			"GET /api/v1/knowledge-bases/copy/progress/task1": {
				Status: 200,
				Body:   []byte(`{"data":{"task_id":"task1","target_id":"new","status":"` + status + `"}}`),
			},
		}}
		res, err := newRunTool(
			d,
			&fakeApprover{},
		).Execute(interactiveCtx(), runArgs("knowledge_base.get_copy_progress", map[string]any{"task_id": "task1"}))
		require.NoError(t, err)
		require.True(t, res.Success, res.Error)
		if status == "completed" || status == "failed" {
			data, err := json.Marshal(res.Data)
			require.NoError(t, err)
			assert.JSONEq(t, `{
  "data_change": {
    "operation": "knowledge_base.get_copy_progress",
    "area": "knowledge_base",
    "ids": {
      "knowledge_base_id": [
        "new"
      ]
    }
  }
}`, string(data))
		} else {
			assert.Empty(t, res.Data)
		}
	}
}
