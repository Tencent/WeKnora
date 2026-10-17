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

func callKey(t *testing.T, opID string, params map[string]any) string {
	t.Helper()
	op, ok := operations.Builtin().Get(opID)
	require.True(t, ok, opID)
	raw := map[string]json.RawMessage{}
	for k, v := range params {
		raw[k], _ = json.Marshal(v)
	}
	call, err := op.Build(raw)
	require.NoError(t, err)
	return call.Method + " " + call.Path
}

func englishCtx() context.Context {
	return context.WithValue(interactiveCtx(), types.LanguageContextKey, "en-US")
}

func TestRunOperationGrantFollowsTheTargetScope(t *testing.T) {
	ok := func(body string) *operations.Response {
		return &operations.Response{Status: http.StatusOK, Body: []byte(body)}
	}
	responses := map[string]*operations.Response{
		"GET /api/v1/knowledge-bases/kb1": ok(`{"success":true,"data":{"id":"kb1","name":"Finance"}}`),
		"GET /api/v1/knowledge/k1":        ok(docBody),
		"GET /api/v1/agents/a1":           ok(`{"success":true,"data":{"id":"a1","name":"Support bot"}}`),
	}
	for _, tc := range []struct {
		name      string
		op        string
		params    map[string]any
		wantGrant string
		wantScope string
	}{
		{
			"knowledge base", "knowledge_base.update",
			map[string]any{"knowledge_base_id": "kb1", "name": "Finance 2"},
			"knowledge_base.update knowledge_base:kb1", `(only in knowledge base "Finance")`,
		},
		{
			"document, scoped to its knowledge base", "knowledge.update",
			map[string]any{"knowledge_id": "k1", "title": "x"},
			"knowledge.update knowledge_base:kb1", "(only in the knowledge bases this call touches)",
		},
		{
			"agent", "agent.update",
			map[string]any{"agent_id": "a1", "name": "Bot"},
			"agent.update agent:a1", `(only for agent "Support bot")`,
		},
		{"deletion", "knowledge.delete", map[string]any{"knowledge_id": "k1"}, "", ""},
		{"creation without a scope", "knowledge_base.create", map[string]any{"name": "New"}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &fakeDispatcher{responses: responses}
			gate := &fakeApprover{decision: approval.Decision{Approved: false}}
			_, err := newRunTool(d, gate).Execute(englishCtx(), runArgs(tc.op, tc.params))
			require.NoError(t, err)
			require.Len(t, gate.requests, 1)
			req := gate.requests[0]
			assert.Equal(t, tc.wantGrant, req.Grant)
			if tc.wantScope == "" {
				assert.Empty(t, req.GrantScope)
			} else {
				assert.Contains(t, req.GrantScope, tc.wantScope)
			}
			for _, call := range d.calls {
				assert.Equal(t, http.MethodGet, call.call.Method, "a declined call writes nothing")
			}
		})
	}
}

func TestRunOperationGrantedCallRunsWithoutAPrompt(t *testing.T) {
	params := map[string]any{"knowledge_id": "k1", "title": "Renamed"}
	update := callKey(t, "knowledge.update", params)
	d := &fakeDispatcher{responses: map[string]*operations.Response{
		"GET /api/v1/knowledge/k1": {Status: http.StatusOK, Body: []byte(docBody)},
		update:                     {Status: http.StatusOK, Body: []byte(`{"success":true,"data":{"id":"k1"}}`)},
	}}
	gate := &fakeApprover{decision: approval.Decision{Approved: true, Granted: true}}

	res, err := newRunTool(d, gate).Execute(interactiveCtx(), runArgs("knowledge.update", params))
	require.NoError(t, err)
	require.True(t, res.Success, res.Error)
	require.Len(t, d.calls, 2)
	assert.Equal(t, update, d.calls[1].call.Method+" "+d.calls[1].call.Path)
}

func TestConfirmedToolOffersGrantsOnlyForRecoverableChanges(t *testing.T) {
	gate := &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p1"}}
	access := operations.Access{Level: types.AssistantAccessConfirm}
	ctx := englishCtx()

	write := &wikiReplaceTextTool{
		BaseTool: NewBaseTool(ToolWikiReplaceText, "d", json.RawMessage(`{}`)),
		kbIDs:    []string{"kb2", "kb1"},
	}
	_, err := NewConfirmedTool(write, operations.RiskWrite, access, gate).Execute(ctx, json.RawMessage(`{}`))
	require.NoError(t, err)
	del := &wikiReplaceTextTool{
		BaseTool: NewBaseTool(ToolWikiDeletePage, "d", json.RawMessage(`{}`)),
		kbIDs:    []string{"kb1"},
	}
	_, err = NewConfirmedTool(del, operations.RiskDestructive, access, gate).Execute(ctx, json.RawMessage(`{}`))
	require.NoError(t, err)

	require.Len(t, gate.requests, 2)
	assert.Equal(t, ToolWikiReplaceText+" knowledge_base:kb1,kb2", gate.requests[0].Grant)
	assert.Equal(t, "Edit Wiki page text", gate.requests[0].GrantScope)
	assert.Empty(t, gate.requests[1].Grant, "a deletion always asks")
}
