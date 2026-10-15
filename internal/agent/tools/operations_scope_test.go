package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func scopeDispatcher() *fakeDispatcher {
	return &fakeDispatcher{responses: map[string]*operations.Response{
		"GET /api/v1/knowledge/k1": {Status: 200, Body: []byte(docBody)},
		"GET /api/v1/knowledge/k2": {
			Status: 200,
			Body:   []byte(`{"data":{"id":"k2","knowledge_base_id":"kb1","title":"Second"}}`),
		},
		"GET /api/v1/knowledge/foreign": {
			Status: 200,
			Body:   []byte(`{"data":{"id":"foreign","knowledge_base_id":"kb2"}}`),
		},
		"GET /api/v1/knowledge-bases/kb1": {Status: 200, Body: []byte(`{"data":{"id":"kb1","name":"KB"}}`)},
		"GET /api/v1/agents/a1":           {Status: 200, Body: []byte(`{"data":{"id":"a1","name":"Agent"}}`)},
	}}
}

func TestOperationScopeMatrix(t *testing.T) {
	contexts := []struct {
		name  string
		scope operations.Scope
	}{
		{"KB", operations.Scope{KnowledgeBaseID: "kb1"}},
		{"document", operations.Scope{KnowledgeBaseID: "kb1", KnowledgeID: "k1"}},
		{"wiki", operations.Scope{KnowledgeBaseID: "kb1", WikiSlug: "guide/a"}},
		{"agent", operations.Scope{AgentID: "a1"}},
		{"no context", operations.Scope{}},
	}
	cases := []struct {
		name, op         string
		params           map[string]any
		allowedKB        bool
		allowedDocWrite  bool
		allowedWikiWrite bool
		allowedAgent     bool
		unscopedRead     bool
	}{
		{
			name:      "KB read",
			op:        "knowledge_base.get",
			params:    map[string]any{"knowledge_base_id": "kb1"},
			allowedKB: true,
		},
		{name: "foreign KB", op: "knowledge_base.get", params: map[string]any{"knowledge_base_id": "kb2"}},
		{
			name:      "KB write",
			op:        "knowledge_base.generate_profile",
			params:    map[string]any{"knowledge_base_id": "kb1"},
			allowedKB: true,
		},
		{name: "document read", op: "knowledge.get", params: map[string]any{"knowledge_id": "k1"}, allowedKB: true},
		{name: "other document read", op: "chunk.list", params: map[string]any{"knowledge_id": "k2"}, allowedKB: true},
		{name: "foreign document", op: "knowledge.get", params: map[string]any{"knowledge_id": "foreign"}},
		{
			name: "document write",
			op:   "knowledge.update",
			params: map[string]any{
				"knowledge_id": "k1",
				"title":        "New",
			},
			allowedKB:       true,
			allowedDocWrite: true,
		},
		{
			name: "other document write",
			op:   "knowledge.update",
			params: map[string]any{
				"knowledge_id": "k2",
				"title":        "New",
			},
			allowedKB: true,
		},
		{
			name: "batch",
			op:   "knowledge.batch_delete",
			params: map[string]any{
				"knowledge_base_id": "kb1",
				"knowledge_ids": []string{
					"k1",
					"k2",
				},
			},
			allowedKB: true,
		},
		{
			name: "batch current document",
			op:   "knowledge.batch_delete",
			params: map[string]any{
				"knowledge_base_id": "kb1",
				"knowledge_ids":     []string{"k1"},
			},
			allowedKB:       true,
			allowedDocWrite: true,
		},
		{
			name: "mixed batch",
			op:   "knowledge.batch_delete",
			params: map[string]any{
				"knowledge_base_id": "kb1",
				"knowledge_ids": []string{
					"k1",
					"foreign",
				},
			},
		},
		{
			name: "move inside",
			op:   "knowledge.move",
			params: map[string]any{
				"source_knowledge_base_id": "kb1",
				"knowledge_base_id":        "kb1",
				"knowledge_ids":            []string{"k1"},
				"mode":                     "reuse_vectors",
			},
			allowedKB:       true,
			allowedDocWrite: true,
		},
		{
			name: "move out",
			op:   "knowledge.move",
			params: map[string]any{
				"source_knowledge_base_id": "kb1",
				"knowledge_base_id":        "kb2",
				"knowledge_ids":            []string{"k1"},
				"mode":                     "reuse_vectors",
			},
		},
		{
			name: "move in",
			op:   "knowledge.move",
			params: map[string]any{
				"source_knowledge_base_id": "kb2",
				"knowledge_base_id":        "kb1",
				"knowledge_ids":            []string{"foreign"},
				"mode":                     "reuse_vectors",
			},
		},
		{
			name: "wiki same",
			op:   "wiki.revert",
			params: map[string]any{
				"knowledge_base_id": "kb1",
				"slug":              "guide/a",
				"version":           1,
			},
			allowedKB:        true,
			allowedWikiWrite: true,
		},
		{
			name: "wiki other",
			op:   "wiki.revert",
			params: map[string]any{
				"knowledge_base_id": "kb1",
				"slug":              "guide/b",
				"version":           1,
			},
			allowedKB: true,
		},
		{name: "agent read", op: "agent.get", params: map[string]any{"agent_id": "a1"}, allowedAgent: true},
		{name: "agent other", op: "agent.get", params: map[string]any{"agent_id": "a2"}},
		{
			name: "agent bindings",
			op:   "agent.update",
			params: map[string]any{
				"agent_id":           "a1",
				"knowledge_base_ids": []string{"other-kb"},
				"model_id":           "m2",
				"mcp_service_ids":    []string{"mcp2"},
			},
			allowedAgent: true,
		},
		{name: "KB list", op: "knowledge_base.list", params: map[string]any{}, unscopedRead: true},
		{name: "agent list", op: "agent.list", params: map[string]any{}, unscopedRead: true},
		{name: "model lookup", op: "model.get", params: map[string]any{"model_id": "m1"}, unscopedRead: true},
		{
			name:         "MCP lookup",
			op:           "mcp_service.get",
			params:       map[string]any{"mcp_service_id": "mcp1"},
			unscopedRead: true,
		},
		{
			name:         "progress",
			op:           "knowledge.get_move_progress",
			params:       map[string]any{"task_id": "task1"},
			unscopedRead: true,
		},
		{name: "create", op: "agent.create", params: map[string]any{"name": "New"}},
	}
	for _, level := range []types.AssistantScope{
		types.AssistantScopeWorkspace,
		types.AssistantScopeKnowledgeBase,
		types.AssistantScopePage,
	} {
		for _, page := range contexts {
			for _, tc := range cases {
				t.Run(string(level)+"/"+page.name+"/"+tc.name, func(t *testing.T) {
					d := scopeDispatcher()
					tool := newRunTool(d, nil)
					tool.rt.Scope = page.scope
					tool.rt.Scope.Level = level
					op, ok := tool.rt.Catalog.Get(tc.op)
					require.True(t, ok)
					input, err := parseRunOperationInput(runArgs(tc.op, tc.params))
					require.NoError(t, err)
					_, err = op.Build(input.Params)
					require.NoError(t, err)
					want := level == types.AssistantScopeWorkspace || tc.unscopedRead
					if page.scope.KnowledgeBaseID != "" && tc.allowedKB {
						want = level != types.AssistantScopePage || !op.Risk.Mutates() || page.name == "KB" ||
							(page.name == "document" && tc.allowedDocWrite) ||
							(page.name == "wiki" && tc.allowedWikiWrite)
					}
					if level == types.AssistantScopePage && page.name == "agent" && tc.allowedAgent {
						want = true
					}
					blocked := tool.checkScope(context.Background(), op, input.Params, tool.rt.Identity)
					if want {
						require.Nil(t, blocked)
					} else {
						require.NotNil(t, blocked)
						assert.False(t, blocked.Success)
						assert.Contains(t, blocked.Error, string(level))
						assert.Contains(t, blocked.Error, "assistant panel")
					}
					for _, call := range d.calls {
						assert.Equal(t, http.MethodGet, call.call.Method)
						assert.Equal(t, "knowledge.get", call.identity.Agent.Operation)
						assert.Equal(t, tool.rt.Identity.UserID, call.identity.UserID)
						assert.Equal(t, tool.rt.Identity.TenantID, call.identity.TenantID)
					}
				})
			}
		}
	}
}

func TestScopeRefusesBeforeApprovalAndDispatch(t *testing.T) {
	for _, resumed := range []bool{false, true} {
		for _, op := range []string{"knowledge_base.generate_profile", "agent.update"} {
			d := scopeDispatcher()
			gate := &fakeApprover{decision: approval.Decision{Approved: true}}
			tool := newRunTool(d, gate)
			tool.rt.Scope = operations.Scope{Level: types.AssistantScopePage, KnowledgeBaseID: "kb1", KnowledgeID: "k1"}
			params := map[string]any{"knowledge_base_id": "kb2"}
			if op == "agent.update" {
				params = map[string]any{"agent_id": "a1", "name": "New"}
			}
			ctx := interactiveCtx()
			if resumed {
				meta, _ := ToolExecFromContext(ctx)
				meta.Answered = &types.AgentPrompt{Kind: types.AgentPromptKindToolApproval}
			}
			result, err := tool.Execute(ctx, runArgs(op, params))
			require.NoError(t, err)
			require.False(t, result.Success)
			assert.Contains(t, result.Error, "scope page")
			assert.Empty(t, gate.requests)
			assert.Empty(t, d.calls)
		}
	}
}

func TestScopeRefusesFailedDocumentLookup(t *testing.T) {
	for _, body := range []string{"", `{"data":{}}`, `{"data":{"knowledge_base_id":"kb2"}}`, "invalid"} {
		d := &fakeDispatcher{responses: map[string]*operations.Response{}}
		if body != "" {
			d.responses["GET /api/v1/knowledge/k1"] = &operations.Response{Status: 200, Body: []byte(body)}
		}
		gate := &fakeApprover{decision: approval.Decision{Approved: true}}
		tool := newRunTool(d, gate)
		tool.rt.Scope = operations.Scope{Level: types.AssistantScopeKnowledgeBase, KnowledgeBaseID: "kb1"}
		result, err := tool.Execute(interactiveCtx(), runArgs("knowledge.delete", map[string]any{"knowledge_id": "k1"}))
		require.NoError(t, err)
		assert.False(t, result.Success)
		assert.Contains(t, result.Error, "scope knowledge_base")
		require.Len(t, d.calls, 1)
		assert.Equal(t, http.MethodGet, d.calls[0].call.Method)
		assert.Empty(t, gate.requests)
	}
}

func TestScopeRechecksApprovedEdits(t *testing.T) {
	for _, resumed := range []bool{false, true} {
		d := scopeDispatcher()
		gate := &fakeApprover{decision: approval.Decision{
			Approved:  true,
			PendingID: "p",
			ModifiedArgs: runArgs("knowledge.update",
				map[string]any{
					"knowledge_id": "foreign",
					"title":        "Edited",
				}),
		}}
		tool := newRunTool(d, gate)
		tool.rt.Scope = operations.Scope{Level: types.AssistantScopeKnowledgeBase, KnowledgeBaseID: "kb1"}
		ctx := interactiveCtx()
		if resumed {
			meta, _ := ToolExecFromContext(ctx)
			meta.Answered = &types.AgentPrompt{Kind: types.AgentPromptKindToolApproval}
		}
		args := runArgs("knowledge.update", map[string]any{"knowledge_id": "k1", "title": "New"})
		result, err := tool.Execute(ctx, args)
		require.NoError(t, err)
		assert.False(t, result.Success)
		assert.Contains(t, result.Error, "scope knowledge_base")
		assert.Equal(t, []string{"p"}, gate.executed)
		for _, call := range d.calls {
			assert.Equal(t, http.MethodGet, call.call.Method)
		}
	}
}

func TestScopeBatchLimitBeforeLookups(t *testing.T) {
	d := scopeDispatcher()
	tool := newRunTool(d, nil)
	tool.rt.Scope = operations.Scope{Level: types.AssistantScopeKnowledgeBase, KnowledgeBaseID: "kb1"}
	ids := make([]string, operations.MaxApprovalItems+1)
	for i := range ids {
		ids[i] = "k1"
	}
	result, err := tool.Execute(interactiveCtx(), runArgs("knowledge.batch_delete", map[string]any{
		"knowledge_base_id": "kb1", "knowledge_ids": ids,
	}))
	require.NoError(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, result.Error, "at most 20")
	assert.Empty(t, d.calls)
}

func TestScopedOperationDispatchesAllowedWrite(t *testing.T) {
	d := scopeDispatcher()
	d.responses["PUT /api/v1/knowledge/k1"] = &operations.Response{Status: 200, Body: []byte(docBody)}
	gate := &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p"}}
	tool := newRunTool(d, gate)
	tool.rt.Scope = operations.Scope{Level: types.AssistantScopePage, KnowledgeBaseID: "kb1", KnowledgeID: "k1"}
	args := runArgs("knowledge.update", map[string]any{"knowledge_id": " k1 ", "title": "New"})
	result, err := tool.Execute(interactiveCtx(), args)
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	assert.Len(t, gate.requests, 1)
	assert.Equal(t, http.MethodPut, d.calls[len(d.calls)-1].call.Method)
}

func TestScopeUnknownIdentifierFailsClosed(t *testing.T) {
	tool := newRunTool(scopeDispatcher(), nil)
	tool.rt.Scope = operations.Scope{Level: types.AssistantScopePage, KnowledgeBaseID: "kb1"}
	op := operations.Operation{
		ID:     "future.get",
		Risk:   operations.RiskRead,
		Params: []operations.Param{{Name: "future_id"}},
	}
	result := tool.checkScope(context.Background(),
		op,
		map[string]json.RawMessage{"future_id": json.RawMessage(`"x"`)},
		tool.rt.Identity)
	require.NotNil(t, result)
}
