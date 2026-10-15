package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/event"
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
	_ context.Context,
	id operations.Identity,
	call operations.Call,
) (*operations.Response, error) {
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
	decision approval.Decision
	requests []approval.PendingRequest
	executed []string
}

func (a *fakeApprover) RequestBuiltinApproval(
	_ context.Context,
	req approval.PendingRequest,
) (approval.Decision, error) {
	a.requests = append(a.requests, req)
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

func TestFindOperationsDescribesMatches(t *testing.T) {
	tool := NewFindOperationsTool(operations.Builtin())
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"delete"}`))
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
