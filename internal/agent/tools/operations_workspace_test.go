package tools

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceUpdateApprovalsRolesAndRefresh(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		role                   types.TenantRole
		level                  types.AssistantAccessLevel
		approved, success, ask bool
	}{
		{"confirmed", types.TenantRoleOwner, "", true, true, true},
		{"declined", types.TenantRoleOwner, "", false, false, true},
		{"auto write", types.TenantRoleOwner, types.AssistantAccessAutoWrite, false, true, false},
		{"read only", types.TenantRoleOwner, types.AssistantAccessReadOnly, true, false, false},
		{"admin cannot rename", types.TenantRoleAdmin, types.AssistantAccessAutoWrite, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &fakeDispatcher{responses: map[string]*operations.Response{
				"GET /api/v1/tenants/current": {Status: 200, Body: []byte(`{
  "data": {
    "id": 7,
    "name": "Original",
    "description": "Old description",
    "credentials": {
      "app_secret": "hidden"
    }
  }
}`)},
				"PATCH /api/v1/tenants/current": {Status: 200, Body: []byte(`{
  "data": {
    "id": 7,
    "name": "New name",
    "description": "",
    "credentials": {
      "app_secret": "hidden"
    }
  }
}`)},
			}}
			gate := &fakeApprover{decision: approval.Decision{Approved: tc.approved}}
			tool := newRunTool(d, gate)
			tool.rt.Access = operations.Access{Role: tc.role, EnforceRole: true, Level: tc.level}
			res, err := tool.Execute(
				interactiveCtx(),
				runArgs("workspace.update", map[string]any{"name": "New name", "description": ""}),
			)
			require.NoError(t, err)
			assert.Equal(t, tc.success, res.Success, res.Error)
			if tc.ask {
				require.Len(t, gate.requests, 1)
				assert.Contains(t, gate.requests[0].Description, "Original")
				assert.Contains(t, gate.requests[0].Description, "New name")
				assert.Contains(t, gate.requests[0].Description, "all its members")
				assert.NotContains(t, gate.requests[0].Description, "hidden")
			} else {
				assert.Empty(t, gate.requests)
			}
			if tc.success {
				assert.JSONEq(t, `{"name":"New name","description":""}`, string(d.calls[len(d.calls)-1].call.Body))
				assert.Contains(t, res.Output, `"workspace_id":7`)
				assert.NotContains(t, res.Output, "hidden")
				assert.Equal(t, "workspace", res.Data["data_change"].(map[string]any)["area"])
			} else {
				for _, call := range d.calls {
					assert.Equal(t, "GET", call.call.Method)
				}
				assert.Empty(t, res.Data)
			}
		})
	}
}
