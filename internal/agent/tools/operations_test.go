package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/modelcontext"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordedDispatch struct {
	identity operations.Identity
	call     operations.Call
}

type fakeDispatcher struct {
	calls     []recordedDispatch
	responses map[string]*operations.Response // keyed by method + " " + path
}

func (d *fakeDispatcher) Dispatch(
	ctx context.Context,
	id operations.Identity,
	call operations.Call,
) (*operations.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.calls = append(d.calls, recordedDispatch{identity: id, call: call})
	if resp, ok := d.responses[call.Method+" "+call.Path]; ok {
		return resp, nil
	}
	return &operations.Response{
		Status: http.StatusNotFound,
		Body:   []byte(`{"success":false,"error":{"message":"Knowledge not found"}}`),
	}, nil
}

type fakeApprover struct {
	decision  approval.Decision
	requests  []approval.PendingRequest
	executed  []string
	onRequest func()
}

func (a *fakeApprover) RequestBuiltinApproval(
	_ context.Context,
	req approval.PendingRequest,
) (approval.Decision, error) {
	a.requests = append(a.requests, req)
	if a.onRequest != nil {
		a.onRequest()
	}
	return a.decision, nil
}

func (a *fakeApprover) ResumeDecision(context.Context, *types.AgentPrompt, *event.EventBus) approval.Decision {
	return a.decision
}

func (a *fakeApprover) MarkExecuted(_ context.Context, id string) {
	a.executed = append(a.executed, id)
}

const docBody = `{"success":true,"data":{"id":"k1","knowledge_base_id":"kb1","title":"Q3 report.pdf",` +
	`"parse_status":"failed","error_message":"encrypted pdf","file_path":"local://secret"}}`

func newRunTool(d *fakeDispatcher, gate approval.BuiltinApproval) *RunOperationTool {
	return NewRunOperationTool(OperationsRuntime{
		Catalog:    operations.Builtin(),
		Dispatcher: d,
		Gate:       gate,
		Identity: operations.Identity{
			UserID: "u1", TenantID: 7, Agent: types.AuditAgentAction{AgentID: types.BuiltinWeKnoraAssistantID},
		},
	})
}

func interactiveCtx() context.Context {
	return WithToolExecContext(context.Background(), &ToolExecContext{
		SessionID: "s1", UserID: "web_user:u1", ToolCallID: "call-1",
		EventBus: event.NewEventBus(), Interactive: true,
	})
}

func runArgs(op string, params map[string]any) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"operation": op, "params": params})
	return b
}

func TestRunOperationReadProjectsResult(t *testing.T) {
	d := &fakeDispatcher{responses: map[string]*operations.Response{
		"GET /api/v1/knowledge/k1": {Status: http.StatusOK, Body: []byte(docBody)},
	}}
	tool := newRunTool(d, nil)

	res, err := tool.Execute(interactiveCtx(), runArgs("knowledge.get", map[string]any{"knowledge_id": "k1"}))
	require.NoError(t, err)
	require.True(t, res.Success, res.Error)
	assert.Empty(t, res.Data)
	assert.NotContains(t, res.Output, "local://secret", "undeclared fields never reach the model")
	assert.Contains(t, res.Output, `"error_message":"encrypted pdf"`)
	require.Len(t, d.calls, 1)
	assert.Equal(t, "u1", d.calls[0].identity.UserID)
	assert.Equal(t, "knowledge.get", d.calls[0].identity.Agent.Operation)
	assert.Equal(t, "s1", d.calls[0].identity.Agent.SessionID)
	assert.Equal(t, types.BuiltinWeKnoraAssistantID, d.calls[0].identity.Agent.AgentID)
}

func TestRunOperationRejectsUnknownOperationAndParams(t *testing.T) {
	d := &fakeDispatcher{}
	tool := newRunTool(d, nil)

	res, _ := tool.Execute(interactiveCtx(), runArgs("tenant.delete", map[string]any{}))
	assert.False(t, res.Success)
	assert.Contains(t, res.Error, "find_operations")

	res, _ = tool.Execute(
		interactiveCtx(),
		runArgs("knowledge.get", map[string]any{"knowledge_id": "k1", "tenant_id": 1}),
	)
	assert.False(t, res.Success)
	assert.Contains(t, res.Error, "unknown parameter")
	assert.Empty(t, d.calls)
}

func TestRunOperationMutationNeedsInteractiveApproval(t *testing.T) {
	d := &fakeDispatcher{}
	gate := &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p1"}}
	tool := newRunTool(d, gate)
	args := runArgs("knowledge.delete", map[string]any{"knowledge_id": "k1"})

	nonInteractive := WithToolExecContext(context.Background(), &ToolExecContext{EventBus: event.NewEventBus()})
	res, _ := tool.Execute(nonInteractive, args)
	assert.False(t, res.Success)
	assert.Contains(t, res.Error, "confirmation")

	res, _ = tool.Execute(context.Background(), args)
	assert.False(t, res.Success)

	res, _ = newRunTool(d, nil).Execute(interactiveCtx(), args)
	assert.False(t, res.Success, "no approver means no mutation")

	assert.Empty(t, d.calls)
	assert.Empty(t, gate.requests)
}

func TestRunOperationRefusesTargetsTheUserCannotRead(t *testing.T) {
	d := &fakeDispatcher{} // every call 404s
	gate := &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p1"}}
	tool := newRunTool(d, gate)

	res, _ := tool.Execute(interactiveCtx(), runArgs("knowledge.delete", map[string]any{"knowledge_id": "k-other"}))
	assert.False(t, res.Success)
	assert.Contains(t, res.Error, "Knowledge not found")
	assert.Empty(t, gate.requests, "no prompt for a target the user cannot see")
	require.Len(t, d.calls, 1)
	assert.Equal(t, http.MethodGet, d.calls[0].call.Method)
}

func TestRunOperationApprovedMutation(t *testing.T) {
	d := &fakeDispatcher{responses: map[string]*operations.Response{
		"GET /api/v1/knowledge/k1": {Status: http.StatusOK, Body: []byte(docBody)},
		"DELETE /api/v1/knowledge/k1": {
			Status: http.StatusOK,
			Body:   []byte(`{"success":true,"data":{"task_id":"t1"}}`),
		},
	}}
	gate := &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p1"}}
	tool := newRunTool(d, gate)

	res, _ := tool.Execute(interactiveCtx(), runArgs("knowledge.delete", map[string]any{"knowledge_id": "k1"}))
	require.True(t, res.Success, res.Error)
	require.Len(t, gate.requests, 1)
	req := gate.requests[0]
	assert.Equal(t, "knowledge.delete", req.MCPToolName)
	assert.Equal(t, ToolRunOperation, req.RegisteredToolName)
	assert.Contains(t, req.Description, "Q3 report.pdf")
	assert.Equal(t, uint64(7), req.TenantID)
	assert.Equal(t, "web_user:u1", req.UserID)
	require.Len(t, d.calls, 2)
	assert.Equal(t, http.MethodDelete, d.calls[1].call.Method)
	assert.Equal(t, []string{"p1"}, gate.executed)
}

func TestRunOperationBatchApprovalNamesEveryItem(t *testing.T) {
	responses := map[string]*operations.Response{
		"GET /api/v1/knowledge-bases/kb1": {
			Status: http.StatusOK, Body: []byte(`{"success":true,"data":{"id":"kb1","name":"Finance"}}`),
		},
		"GET /api/v1/knowledge/k1": {Status: http.StatusOK, Body: []byte(docBody)},
		"GET /api/v1/knowledge/k2": {
			Status: http.StatusOK, Body: []byte(`{"success":true,"data":{"id":"k2","title":"Q4 plan.docx"}}`),
		},
		"POST /api/v1/knowledge/batch-delete": {
			Status: http.StatusOK, Body: []byte(`{"success":true,"data":{"task_id":"t1","deleted_count":2}}`),
		},
	}
	d := &fakeDispatcher{responses: responses}
	gate := &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p1"}}
	params := map[string]any{"knowledge_base_id": "kb1", "knowledge_ids": []string{"k1", "k2"}}

	res, _ := newRunTool(d, gate).Execute(interactiveCtx(), runArgs("knowledge.batch_delete", params))
	require.True(t, res.Success, res.Error)
	require.Len(t, gate.requests, 1)
	assert.Equal(t, "批量删除知识\nFinance\n- Q3 report.pdf\n- Q4 plan.docx", gate.requests[0].Description)
	assert.Contains(t, res.Output, `"deleted_count":2`)

	d = &fakeDispatcher{responses: responses}
	gate = &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p1"}}
	params["knowledge_ids"] = []string{"k1", "k-other"}
	res, _ = newRunTool(d, gate).Execute(interactiveCtx(), runArgs("knowledge.batch_delete", params))
	assert.False(t, res.Success)
	assert.Contains(t, res.Error, "Knowledge not found")
	assert.Empty(t, gate.requests, "one item the user cannot see blocks the whole batch")

	ids := make([]string, operations.MaxApprovalItems+1)
	for i := range ids {
		ids[i] = "k1"
	}
	params["knowledge_ids"] = ids
	d = &fakeDispatcher{responses: responses}
	res, _ = newRunTool(d, gate).Execute(interactiveCtx(), runArgs("knowledge.batch_delete", params))
	assert.False(t, res.Success)
	assert.Contains(t, res.Error, "at most 20")
	assert.Empty(t, d.calls)
}

func TestRunOperationBatchApprovalKeepsMultilineTitlesOnOneLine(t *testing.T) {
	for _, separator := range []string{"\n", "\r\n", "\r", "\u2028", "\u2029"} {
		t.Run(separator, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"data": map[string]any{"title": "First" + separator + "document"}})
			require.NoError(t, err)
			d := &fakeDispatcher{responses: map[string]*operations.Response{
				"GET /api/v1/knowledge-bases/kb1": {Status: http.StatusOK, Body: []byte(`{"data":{"name":"Docs"}}`)},
				"GET /api/v1/knowledge/k1":        {Status: http.StatusOK, Body: body},
				"GET /api/v1/knowledge/k2":        {Status: http.StatusOK, Body: []byte(`{"data":{"title":"Second"}}`)},
			}}
			gate := &fakeApprover{decision: approval.Decision{Approved: false}}
			_, err = newRunTool(d, gate).Execute(interactiveCtx(), runArgs("knowledge.batch_delete", map[string]any{
				"knowledge_base_id": "kb1", "knowledge_ids": []string{"k1", "k2"},
			}))
			require.NoError(t, err)
			require.Len(t, gate.requests, 1)
			assert.Equal(t, "批量删除知识\nDocs\n- First document\n- Second", gate.requests[0].Description)
			assert.Len(t, d.calls, 3, "declining never dispatches the delete")
		})
	}
}

func TestRunOperationFAQDeleteNamesEveryQuestion(t *testing.T) {
	d := &fakeDispatcher{responses: map[string]*operations.Response{
		"GET /api/v1/knowledge-bases/kb1": {
			Status: http.StatusOK, Body: []byte(`{"success":true,"data":{"id":"kb1","name":"Support"}}`),
		},
		"GET /api/v1/knowledge-bases/kb1/faq/entries/7": {
			Status: http.StatusOK,
			Body:   []byte(`{"success":true,"data":{"id":7,"standard_question":"How do I reset?"}}`),
		},
		"DELETE /api/v1/knowledge-bases/kb1/faq/entries": {Status: http.StatusOK, Body: []byte(`{"success":true}`)},
	}}
	gate := &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p1"}}

	res, _ := newRunTool(d, gate).Execute(interactiveCtx(), runArgs("faq.delete_entries",
		map[string]any{"knowledge_base_id": "kb1", "entry_ids": []int{7}}))
	require.True(t, res.Success, res.Error)
	require.Len(t, gate.requests, 1)
	assert.Equal(t, "批量删除FAQ条目\nSupport\n- How do I reset?", gate.requests[0].Description)
	require.Len(t, d.calls, 3)
	assert.JSONEq(t, `{"ids":[7]}`, string(d.calls[2].call.Body))
}

func TestRunOperationDeclinedOrEditedToAnotherOperation(t *testing.T) {
	responses := map[string]*operations.Response{
		"GET /api/v1/knowledge/k1": {Status: http.StatusOK, Body: []byte(docBody)},
	}
	args := runArgs("knowledge.reparse", map[string]any{"knowledge_id": "k1"})

	d := &fakeDispatcher{responses: responses}
	declined := &fakeApprover{decision: approval.Decision{Approved: false, PendingID: "p1", Reason: "not now"}}
	res, _ := newRunTool(d, declined).Execute(interactiveCtx(), args)
	assert.False(t, res.Success)
	assert.Contains(t, res.Error, "not now")
	assert.Len(t, d.calls, 1, "only the target read ran")

	d = &fakeDispatcher{responses: responses}
	edited := &fakeApprover{decision: approval.Decision{
		Approved: true, PendingID: "p2",
		ModifiedArgs: runArgs("knowledge.delete", map[string]any{"knowledge_id": "k1"}),
	}}
	res, _ = newRunTool(d, edited).Execute(interactiveCtx(), args)
	assert.False(t, res.Success)
	assert.Contains(t, res.Error, "cannot be changed")
	assert.Len(t, d.calls, 1)
	assert.Equal(t, []string{"p2"}, edited.executed)
}

func TestRunOperationSuspends(t *testing.T) {
	d := &fakeDispatcher{responses: map[string]*operations.Response{
		"GET /api/v1/knowledge/k1": {Status: http.StatusOK, Body: []byte(docBody)},
	}}
	gate := &fakeApprover{
		decision: approval.Decision{PendingID: "p1", Suspended: &types.PromptSuspension{PromptID: "p1"}},
	}
	res, _ := newRunTool(
		d,
		gate,
	).Execute(interactiveCtx(), runArgs("knowledge.reparse", map[string]any{"knowledge_id": "k1"}))
	assert.False(t, res.Success)
	require.NotNil(t, res.Suspend)
	assert.Len(t, d.calls, 1)
}

func TestOperationIdentityOnlyForSignedInWebUsers(t *testing.T) {
	web := types.WithCaller(types.WithPrincipal(context.Background(),
		types.Principal{Type: types.PrincipalWebUser, ID: "u1"}),
		types.Caller{TenantID: 7, UserID: "u1", Role: types.TenantRoleAdmin})
	id, ok := OperationIdentityFromContext(web)
	require.True(t, ok)
	assert.Equal(t, "u1", id.UserID)
	assert.Equal(t, uint64(7), id.TenantID)

	_, ok = OperationIdentityFromContext(types.WithTenantAPIKeyScope(web, types.TenantAPIKeyScope{KeyID: 3}))
	assert.False(t, ok, "API-key scoped runs never act as the key's user")

	embed := types.WithCaller(types.WithPrincipal(context.Background(),
		types.Principal{Type: types.PrincipalEmbedVisitor, ID: "v1"}),
		types.Caller{TenantID: 7, UserID: "u1"})
	_, ok = OperationIdentityFromContext(embed)
	assert.False(t, ok)

	synthetic := types.WithCaller(types.WithPrincipal(context.Background(),
		types.Principal{Type: types.PrincipalWebUser, ID: "system-7"}),
		types.Caller{TenantID: 7, UserID: "system-7"})
	_, ok = OperationIdentityFromContext(synthetic)
	assert.False(t, ok)
}

func TestRunOperationApprovalNamesTheChange(t *testing.T) {
	kb := `{"success":true,"data":{"id":"kb1","name":"Specs"}}`
	d := &fakeDispatcher{responses: map[string]*operations.Response{
		"GET /api/v1/knowledge-bases/kb1": {Status: http.StatusOK, Body: []byte(kb)},
		"GET /api/v1/knowledge/k1":        {Status: http.StatusOK, Body: []byte(docBody)},
		"POST /api/v1/knowledge-bases/kb1/knowledge/url": {
			Status: http.StatusCreated,
			Body:   []byte(`{"success":true,"data":{"id":"k9","title":"Imported"}}`),
		},
		"POST /api/v1/knowledge/folder": {
			Status: http.StatusOK,
			Body:   []byte(`{"success":true,"data":{"moved_count":1,"folder_path":"docs/spec"}}`),
		},
	}}
	gate := &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p1"}}
	tool := newRunTool(d, gate)

	res, _ := tool.Execute(interactiveCtx(), runArgs("knowledge.create_from_url", map[string]any{
		"knowledge_base_id": "kb1",
		"url":               "https://example.com/spec",
	}))
	require.True(t, res.Success, res.Error)
	require.NotEmpty(t, gate.requests)
	assert.Contains(t, gate.requests[0].Description, "Specs")
	assert.Contains(t, gate.requests[0].Description, "https://example.com/spec")

	gate.requests = nil
	res, _ = tool.Execute(interactiveCtx(), runArgs("knowledge.move_to_folder", map[string]any{
		"knowledge_base_id": "kb1",
		"knowledge_ids":     []string{"k1"},
		"folder_path":       "docs/spec",
	}))
	require.True(t, res.Success, res.Error)
	require.NotEmpty(t, gate.requests)
	desc := gate.requests[len(gate.requests)-1].Description
	assert.Contains(t, desc, "Specs")
	assert.Contains(t, desc, "docs/spec")
	assert.Contains(t, desc, "Q3 report.pdf")
	assert.NotContains(t, desc, "k1")
}

func TestRunOperationAgentApprovals(t *testing.T) {
	agent := `{"success":true,"data":{"id":"a1","name":"Support","config":{"system_prompt":"Be brief."}}}`
	d := &fakeDispatcher{responses: map[string]*operations.Response{
		"GET /api/v1/agents/a1":   {Status: http.StatusOK, Body: []byte(agent)},
		"POST /api/v1/agents":     {Status: http.StatusCreated, Body: []byte(agent)},
		"PATCH /api/v1/agents/a1": {Status: http.StatusOK, Body: []byte(agent)},
		"GET /api/v1/models/m1": {
			Status: http.StatusOK,
			Body:   []byte(`{"data":{"id":"m1","name":"qwen3-max","display_name":"Qwen3 Max"}}`),
		},
		"GET /api/v1/knowledge-bases/kb1": {Status: http.StatusOK, Body: []byte(`{"data":{"name":"Specs"}}`)},
	}}
	gate := &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p1"}}
	tool := newRunTool(d, gate)

	res, _ := tool.Execute(interactiveCtx(), runArgs("agent.create", map[string]any{"name": "Release\nnotes"}))
	require.True(t, res.Success, res.Error)
	require.Len(t, gate.requests, 1)
	assert.Equal(t, "创建智能体\nRelease notes", gate.requests[0].Description)
	assert.Contains(t, res.Output, `"agent_id":"a1"`)

	gate.requests = nil
	res, _ = tool.Execute(interactiveCtx(), runArgs("agent.update", map[string]any{
		"agent_id": "a1", "agent_mode": "smart-reasoning", "model_id": "m1",
		"knowledge_base_ids": []string{"kb1", "kb-gone"},
		"system_prompt":      "Answer in English.", "web_search_enabled": false,
	}))
	require.True(t, res.Success, res.Error)
	require.Len(t, gate.requests, 1)
	desc := gate.requests[0].Description
	assert.Contains(t, desc, "Support")
	assert.Contains(t, desc, "Mode: smart-reasoning")
	assert.Contains(t, desc, "Model: Qwen3 Max")
	assert.Contains(t, desc, "Knowledge bases: Specs, kb-gone", "an unreadable id is shown as is")
	assert.Contains(t, desc, "System prompt (18 characters): Answer in English.")
	assert.Contains(t, desc, "Web search: off")
	assert.JSONEq(t, `{"agent_mode":"smart-reasoning","model_id":"m1","knowledge_bases":["kb1","kb-gone"],`+
		`"system_prompt":"Answer in English.","web_search_enabled":false}`, string(d.calls[len(d.calls)-1].call.Body))
}

func TestRunOperationKnowledgeBaseCreateApproval(t *testing.T) {
	d := &fakeDispatcher{responses: map[string]*operations.Response{
		"GET /api/v1/models/emb": {
			Status: http.StatusOK,
			Body:   []byte(`{"data":{"id":"emb","name":"builtin-embedding","display_name":"builtin-embedding"}}`),
		},
		"GET /api/v1/models/sum": {
			Status: http.StatusOK,
			Body:   []byte(`{"data":{"id":"sum","name":"qwen","display_name":"Qwen"}}`),
		},
		"POST /api/v1/knowledge-bases": {
			Status: http.StatusOK,
			Body:   []byte(`{"data":{"id":"kb","name":"助手测试"}}`),
		},
	}}
	gate := &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p1"}}
	res, _ := newRunTool(d, gate).Execute(interactiveCtx(), runArgs("knowledge_base.create", map[string]any{
		"name": "助手测试", "type": "document",
		"embedding_model_id": "emb", "summary_model_id": "sum",
		"vlm_model_id": "builtin-vllm", "wiki_enabled": true,
	}))
	require.True(t, res.Success, res.Error)
	require.Len(t, gate.requests, 1)
	assert.Equal(t,
		"创建知识库\n助手测试\nType: document\nEmbedding model: builtin-embedding\n"+
			"Summary model: Qwen\nCaption model: builtin-vllm\nIndexes: wiki on",
		gate.requests[0].Description)
}

func TestRunOperationAgentUpdateRefusesTruncatedPrompt(t *testing.T) {
	long, err := json.Marshal(map[string]any{"data": map[string]any{
		"id": "a1", "name": "Support", "config": map[string]any{"system_prompt": string(make([]rune, 8001))},
	}})
	require.NoError(t, err)
	d := &fakeDispatcher{responses: map[string]*operations.Response{
		"GET /api/v1/agents/a1": {Status: http.StatusOK, Body: long},
	}}
	gate := &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p1"}}
	res, _ := newRunTool(d, gate).Execute(interactiveCtx(), runArgs("agent.update",
		map[string]any{"agent_id": "a1", "system_prompt": "short"}))
	assert.False(t, res.Success)
	assert.Contains(t, res.Error, "truncated")
	assert.Empty(t, gate.requests)
}

func TestRunOperationFollowsAccessLevel(t *testing.T) {
	responses := map[string]*operations.Response{
		"GET /api/v1/knowledge/k1":          {Status: http.StatusOK, Body: []byte(docBody)},
		"POST /api/v1/knowledge/k1/reparse": {Status: http.StatusOK, Body: []byte(docBody)},
		"DELETE /api/v1/knowledge/k1": {
			Status: http.StatusOK, Body: []byte(`{"success":true,"data":{"task_id":"t1"}}`),
		},
	}
	newTool := func(level types.AssistantAccessLevel, d *fakeDispatcher, gate *fakeApprover) *RunOperationTool {
		access := operations.Access{Level: level}
		return NewRunOperationTool(OperationsRuntime{
			Catalog:    operations.Builtin(),
			Dispatcher: d,
			Gate:       gate,
			Identity:   operations.Identity{UserID: "u1", TenantID: 7},
			Access:     access,
		})
	}
	reparse := runArgs("knowledge.reparse", map[string]any{"knowledge_id": "k1"})
	remove := runArgs("knowledge.delete", map[string]any{"knowledge_id": "k1"})

	t.Run("auto_write runs writes without asking but still asks before deleting", func(t *testing.T) {
		d := &fakeDispatcher{responses: responses}
		gate := &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p1"}}
		tool := newTool(types.AssistantAccessAutoWrite, d, gate)

		res, _ := tool.Execute(interactiveCtx(), reparse)
		require.True(t, res.Success, res.Error)
		assert.Empty(t, gate.requests)

		res, _ = tool.Execute(interactiveCtx(), remove)
		require.True(t, res.Success, res.Error)
		assert.Len(t, gate.requests, 1)
	})

	t.Run("read_only refuses every change", func(t *testing.T) {
		d := &fakeDispatcher{responses: responses}
		gate := &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p1"}}
		tool := newTool(types.AssistantAccessReadOnly, d, gate)

		for _, args := range []json.RawMessage{reparse, remove} {
			res, _ := tool.Execute(interactiveCtx(), args)
			assert.False(t, res.Success)
			assert.Contains(t, res.Error, "read-only", "the refusal says why")
			assert.NotContains(t, res.Error, "unknown operation")
		}
		assert.Empty(t, d.calls)
		assert.Empty(t, gate.requests)
		assert.Contains(t, tool.Description(), "read-only")
	})

	t.Run("a role below the operation's is refused with the role it needs", func(t *testing.T) {
		d := &fakeDispatcher{responses: responses}
		tool := NewRunOperationTool(OperationsRuntime{
			Catalog:    operations.Builtin(),
			Dispatcher: d,
			Gate:       &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p1"}},
			Access:     operations.Access{Role: types.TenantRoleViewer, EnforceRole: true},
		})
		res, _ := tool.Execute(interactiveCtx(), remove)
		assert.False(t, res.Success)
		assert.Contains(t, res.Error, "contributor")
		assert.Empty(t, d.calls)
	})
}

func TestRunOperationAutoWriteHonorsResumedAnswer(t *testing.T) {
	for _, approved := range []bool{false, true} {
		name := "declined"
		if approved {
			name = "approved with edited parameters"
		}
		t.Run(name, func(t *testing.T) {
			d := &fakeDispatcher{responses: map[string]*operations.Response{
				"POST /api/v1/knowledge/k1/reparse": {Status: http.StatusOK, Body: []byte(docBody)},
				"POST /api/v1/knowledge/k2/reparse": {Status: http.StatusOK, Body: []byte(docBody)},
			}}
			gate := &fakeApprover{decision: approval.Decision{
				Approved: approved, PendingID: "p1", Reason: "declined earlier",
				ModifiedArgs: runArgs("knowledge.reparse", map[string]any{"knowledge_id": "k2"}),
			}}
			tool := newRunTool(d, gate)
			// The original request asked for auto_write but was capped at confirm.
			// Raising the workspace ceiling before resume must not discard its answer.
			tool.rt.Access.Level = types.AssistantAccessAutoWrite
			ctx := interactiveCtx()
			meta, _ := ToolExecFromContext(ctx)
			meta.Interactive = false
			meta.Answered = &types.AgentPrompt{Kind: types.AgentPromptKindToolApproval}
			res, err := tool.Execute(ctx, runArgs("knowledge.reparse", map[string]any{"knowledge_id": "k1"}))
			require.NoError(t, err)
			assert.Empty(t, gate.requests, "resuming must not ask again")
			if !approved {
				assert.False(t, res.Success)
				assert.Contains(t, res.Error, "declined earlier")
				assert.Empty(t, d.calls)
				return
			}
			require.True(t, res.Success, res.Error)
			require.Len(t, d.calls, 1)
			assert.Equal(t, "/api/v1/knowledge/k2/reparse", d.calls[0].call.Path)
			assert.Equal(t, map[string]any{
				"operation": "knowledge.reparse", "area": "knowledge",
				"ids": map[string][]string{"knowledge_id": {"k2"}},
			}, res.Data["data_change"])
			assert.Equal(t, []string{"p1"}, gate.executed)
		})
	}
}

func TestFindOperationsExplainsWithheldMatches(t *testing.T) {
	type output struct {
		Operations  []operationView            `json:"operations"`
		Unavailable []unavailableOperationView `json:"unavailable"`
		Hint        string                     `json:"hint"`
	}
	find := func(access operations.Access, query string) output {
		res, err := NewFindOperationsTool(operations.Builtin(), access).
			Execute(context.Background(), json.RawMessage(`{"query":"`+query+`"}`))
		require.NoError(t, err)
		require.True(t, res.Success)
		var out output
		require.NoError(t, json.Unmarshal([]byte(res.Output), &out))
		return out
	}

	viewer := find(operations.Access{Role: types.TenantRoleViewer, EnforceRole: true}, "knowledge.delete")
	assert.Empty(t, viewer.Operations)
	require.Len(t, viewer.Unavailable, 1)
	assert.Equal(t, "knowledge.delete", viewer.Unavailable[0].ID)
	assert.Contains(t, viewer.Unavailable[0].Reason, "contributor")
	assert.Contains(t, viewer.Hint, "instead of searching again")

	readOnly := find(operations.Access{Level: types.AssistantAccessReadOnly}, "knowledge.delete")
	require.Len(t, readOnly.Unavailable, 1)
	assert.Contains(t, readOnly.Unavailable[0].Reason, "assistant settings")

	contributor := find(operations.Access{Role: types.TenantRoleContributor, EnforceRole: true}, "knowledge.delete")
	require.Len(t, contributor.Operations, 1)
	assert.Empty(t, contributor.Unavailable)
}

func TestFindOperationsDescribesMatches(t *testing.T) {
	tool := NewFindOperationsTool(operations.Builtin(), operations.Access{Level: types.AssistantAccessConfirm})
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"knowledge.delete"}`))
	require.NoError(t, err)
	require.True(t, res.Success)
	var out struct {
		Operations []operationView `json:"operations"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.Output), &out))
	require.Len(t, out.Operations, 1)
	assert.Equal(t, "knowledge.delete", out.Operations[0].ID)
	assert.Equal(t, "destructive", out.Operations[0].Risk)
	require.Len(t, out.Operations[0].Params, 1)
	assert.Equal(t, "knowledge_id", out.Operations[0].Params[0].Name)

	res, _ = tool.Execute(context.Background(), json.RawMessage(`{"query":"zzz-unmatched"}`))
	assert.Contains(t, res.Output, "hint")
}

func TestRunOperationAgentSkillsApproval(t *testing.T) {
	for _, tc := range []struct {
		mode, want string
		names      []string
	}{
		{"selected", "Skills: ppt-generator, csv-analysis", []string{"ppt-generator", "csv-analysis"}},
		{"none", "Skills: (none)", []string{}},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			body := []byte(`{"data":{"id":"a1","name":"Support","config":{}}}`)
			d := &fakeDispatcher{responses: map[string]*operations.Response{
				"GET /api/v1/agents/a1":   {Status: http.StatusOK, Body: body},
				"PATCH /api/v1/agents/a1": {Status: http.StatusOK, Body: body},
			}}
			gate := &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p1"}}
			res, err := newRunTool(d, gate).Execute(interactiveCtx(), runArgs("agent.update", map[string]any{
				"agent_id": "a1", "skills_selection_mode": tc.mode, "selected_skills": tc.names,
			}))
			require.NoError(t, err)
			require.True(t, res.Success, res.Error)
			require.Len(t, gate.requests, 1)
			assert.Contains(t, gate.requests[0].Description, "Skills scope: "+tc.mode+"\n"+tc.want)
			require.Len(t, d.calls, 3)
			for _, call := range d.calls {
				assert.Equal(t, "/api/v1/agents/a1", call.call.Path, "skill names need no ID lookup")
			}
		})
	}
}

func TestRunOperationDataChangeDecodedIDs(t *testing.T) {
	registry := modelcontext.NewRegistry(true)
	kb := registry.RegisterKnowledgeBase("real-kb")
	doc := registry.RegisterDocument("real-doc")
	calls := []types.LLMToolCall{{Function: types.FunctionCall{
		Name: ToolRunOperation,
		Arguments: string(runArgs("knowledge.move_to_folder", map[string]any{
			"knowledge_base_id": kb, "knowledge_ids": []string{doc}, "folder_path": "docs",
		})),
	}}}
	registry.DecodeToolCalls(calls)
	d := &fakeDispatcher{responses: map[string]*operations.Response{}}
	op, ok := operations.Builtin().Get("knowledge.move_to_folder")
	require.True(t, ok)
	input, err := parseRunOperationInput(json.RawMessage(calls[0].Function.Arguments))
	require.NoError(t, err)
	call, err := op.Build(input.Params)
	require.NoError(t, err)
	d.responses[call.Method+" "+call.Path] = &operations.Response{
		Status: http.StatusOK, Body: []byte(`{"success":true}`),
	}
	tool := newRunTool(d, nil)
	tool.rt.Access.Level = types.AssistantAccessAutoWrite
	result, err := tool.Execute(context.Background(), json.RawMessage(calls[0].Function.Arguments))
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	require.Len(t, d.calls, 1)
	assert.Contains(t, string(d.calls[0].call.Body), "real-kb")
	assert.Contains(t, string(d.calls[0].call.Body), "real-doc")
	assert.Equal(t, map[string]any{
		"operation": op.ID, "area": "knowledge",
		"ids": map[string][]string{"knowledge_base_id": {"real-kb"}, "knowledge_id": {"real-doc"}},
	}, result.Data["data_change"])
	assert.NotContains(t, result.Output, "data_change")
	assert.NotContains(t, registry.ModelToolResultForTool(ToolRunOperation, result), "data_change")
}

func TestRunOperationDataChangeCreatedIDsAndFailures(t *testing.T) {
	for _, status := range []int{http.StatusCreated, http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			d := &fakeDispatcher{responses: map[string]*operations.Response{
				"POST /api/v1/agents": {Status: status, Body: []byte(`{"data":{"id":"created-agent"}}`)},
			}}
			tool := newRunTool(d, nil)
			tool.rt.Access.Level = types.AssistantAccessAutoWrite
			result, err := tool.Execute(context.Background(), runArgs("agent.create", map[string]any{"name": "Agent"}))
			require.NoError(t, err)
			if status != http.StatusCreated {
				require.False(t, result.Success)
				assert.Empty(t, result.Data)
				return
			}
			require.True(t, result.Success, result.Error)
			assert.Equal(t, map[string]any{
				"operation": "agent.create", "area": "agent",
				"ids": map[string][]string{"agent_id": {"created-agent"}},
			}, result.Data["data_change"])
		})
	}
}

func TestOperationDataChangeSelectsOnlyResourceIDs(t *testing.T) {
	op, ok := operations.Builtin().Get("knowledge.move")
	require.True(t, ok)
	input, err := parseRunOperationInput(runArgs(op.ID, map[string]any{
		"knowledge_base_id": " destination ", "source_knowledge_base_id": "source",
		"knowledge_ids": []string{"doc", "doc"}, "mode": "reuse_vectors",
	}))
	require.NoError(t, err)
	_, err = op.Build(input.Params)
	require.NoError(t, err)
	change := operationDataChange(op, input.Params, operations.Projected{})
	assert.Equal(t, map[string][]string{
		"knowledge_base_id": {"source", "destination"}, "knowledge_id": {"doc"},
	}, change["ids"])
	create, ok := operations.Builtin().Get("agent.create")
	require.True(t, ok)
	assert.NotContains(t, operationDataChange(create, nil, operations.Projected{}), "ids")
}
