package tools

import (
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunOperationMCPMoveWarnsOnTheCard(t *testing.T) {
	service := `{"data":{"id":"m1","name":"Docs","enabled":true,"auth_config":{"auth_type":"api_key"}}}`
	d := &fakeDispatcher{responses: map[string]*operations.Response{
		"GET /api/v1/mcp-services/m1": {Status: http.StatusOK, Body: []byte(service)},
		"PUT /api/v1/mcp-services/m1": {Status: http.StatusOK, Body: []byte(service)},
	}}
	gate := &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p1"}}
	tool := newRunTool(d, gate)

	res, _ := tool.Execute(englishCtx(), runArgs("mcp_service.update", map[string]any{
		"mcp_service_id": "m1", "url": "https://new.example.com/mcp", "transport_type": "http-streamable",
	}))
	require.True(t, res.Success, res.Error)
	require.Len(t, gate.requests, 1)
	req := gate.requests[0]
	assert.Contains(t, req.Description, "Docs")
	assert.Contains(t, req.Description, "URL: https://new.example.com/mcp")
	assert.Contains(t, req.Description, "Transport: http-streamable")
	assert.Contains(t, req.Description, "removes the saved API key, token and custom headers")
	assert.Empty(t, req.Grant, "workspace infrastructure has no session grant")
	assert.JSONEq(t, `{"url":"https://new.example.com/mcp","transport_type":"http-streamable"}`,
		string(d.calls[len(d.calls)-1].call.Body))

	gate.requests = nil
	res, _ = tool.Execute(englishCtx(), runArgs("mcp_service.update", map[string]any{
		"mcp_service_id": "m1", "enabled": false,
	}))
	require.True(t, res.Success, res.Error)
	require.Len(t, gate.requests, 1)
	assert.Contains(t, gate.requests[0].Description, "Enabled: off")
	assert.NotContains(t, gate.requests[0].Description, "removes the saved")
}

func TestRunOperationSkillInstallAsksEvenWithAutoWrite(t *testing.T) {
	d := &fakeDispatcher{responses: map[string]*operations.Response{
		"POST /api/v1/sandbox-configs/cfg-1/skills": {
			Status: http.StatusAccepted, Body: []byte(`{"success":true,"data":{"skill_id":"sk-9"}}`),
		},
		"GET /api/v1/agents/a1":   {Status: http.StatusOK, Body: []byte(`{"data":{"id":"a1","name":"Bot"}}`)},
		"PATCH /api/v1/agents/a1": {Status: http.StatusOK, Body: []byte(`{"data":{"id":"a1","name":"Bot"}}`)},
	}}
	gate := &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p1"}}
	tool := NewRunOperationTool(OperationsRuntime{
		Catalog: operations.Builtin(), Dispatcher: d, Gate: gate,
		Identity: operations.Identity{UserID: "u1", TenantID: 7},
		Access:   operations.Access{Level: types.AssistantAccessAutoWrite},
	})

	res, _ := tool.Execute(englishCtx(), runArgs("skill.install", map[string]any{
		"sandbox_config_id": "cfg-1", "source": "https://github.com/acme/skills/tree/main/pdf",
	}))
	require.True(t, res.Success, res.Error)
	require.Len(t, gate.requests, 1, "a skill install always asks")
	req := gate.requests[0]
	assert.Contains(t, req.Description, "https://github.com/acme/skills/tree/main/pdf")
	assert.Contains(t, req.Description, "Sandbox: cfg-1")
	assert.Empty(t, req.Grant)
	assert.Contains(t, res.Output, `"skill_id":"sk-9"`)

	res, _ = tool.Execute(englishCtx(), runArgs("agent.update", map[string]any{"agent_id": "a1", "name": "Bot"}))
	require.True(t, res.Success, res.Error)
	assert.Len(t, gate.requests, 1, "an ordinary write still runs without asking at this level")
}

func TestRunOperationModelRenameNamesTheModel(t *testing.T) {
	model := `{"data":{"id":"m1","name":"qwen3-max","display_name":"Qwen"}}`
	d := &fakeDispatcher{responses: map[string]*operations.Response{
		"GET /api/v1/models/m1":   {Status: http.StatusOK, Body: []byte(model)},
		"PATCH /api/v1/models/m1": {Status: http.StatusOK, Body: []byte(model)},
	}}
	gate := &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p1"}}
	tool := newRunTool(d, gate)

	res, _ := tool.Execute(englishCtx(), runArgs("model.update", map[string]any{
		"model_id": "m1", "display_name": "Production Qwen",
	}))
	require.True(t, res.Success, res.Error)
	require.Len(t, gate.requests, 1)
	assert.Contains(t, gate.requests[0].Description, "qwen3-max")
	assert.Contains(t, gate.requests[0].Description, "Display name: Production Qwen")
}
