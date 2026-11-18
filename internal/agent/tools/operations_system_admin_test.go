package tools

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystemAdminDiscoveryAndExecution(t *testing.T) {
	for _, isAdmin := range []bool{false, true} {
		access := operations.Access{
			Role:        types.TenantRoleOwner,
			SystemAdmin: isAdmin,
			Level:       types.AssistantAccessReadOnly,
		}
		find := NewFindOperationsTool(operations.Builtin(), access)
		found, err := find.Execute(interactiveCtx(), []byte(`{"query":"system_admin.list_users"}`))
		require.NoError(t, err)
		d := &fakeDispatcher{responses: map[string]*operations.Response{
			"GET /api/v1/system/admin/users": {
				Status: 200,
				Body:   []byte(`{"users":[{"id":"u","username":"Name","password":"secret"}],"has_more":false}`),
			},
		}}
		gate := &fakeApprover{}
		run := newRunTool(d, gate)
		run.rt.Access = access
		res, err := run.Execute(interactiveCtx(), runArgs("system_admin.list_users", map[string]any{}))
		require.NoError(t, err)
		assert.Equal(t, isAdmin, res.Success, res.Error)
		assert.Empty(t, gate.requests)
		assert.Empty(t, res.Data, "reads must not emit mutation refresh events")
		if !isAdmin {
			assert.Contains(t, found.Output, "Requires system administrator permission")
			assert.Contains(t, res.Error, "Requires system administrator permission")
			assert.Empty(t, d.calls)
		} else {
			assert.NotContains(t, found.Output, `"unavailable"`)
			require.Len(t, d.calls, 1)
			assert.NotContains(t, res.Output, "secret")
			// A permission revoked since discovery is still rejected by the router.
			d.responses["GET /api/v1/system/admin/users"] = &operations.Response{
				Status: 403,
				Body: []byte(
					`{"error":"Forbidden: system administrator required"}`,
				),
			}
			res, err = run.Execute(interactiveCtx(), runArgs("system_admin.list_users", map[string]any{}))
			require.NoError(t, err)
			assert.False(t, res.Success)
		}
	}
}
