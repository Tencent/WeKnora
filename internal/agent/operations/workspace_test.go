package operations

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceOperationsScopeAndParameters(t *testing.T) {
	for _, tc := range []struct {
		id, method, route string
		role              types.TenantRole
		params            []string
	}{
		{"workspace.get", "GET", "/api/v1/tenants/current", types.TenantRoleViewer, nil},
		{
			"workspace.update",
			"PATCH",
			"/api/v1/tenants/current",
			types.TenantRoleOwner,
			[]string{
				"name",
				"description",
			},
		},
		{
			"workspace.list_members",
			"GET",
			"/api/v1/tenants/current/members",
			types.TenantRoleViewer,
			[]string{
				"q",
				"page",
				"page_size",
			},
		},
		{"system.get_info", "GET", "/api/v1/system/info", types.TenantRoleViewer, nil},
	} {
		op, ok := Builtin().Get(tc.id)
		require.True(t, ok, tc.id)
		assert.Equal(t, tc.method, op.Method)
		assert.Equal(t, tc.route, op.Route)
		assert.Equal(t, tc.role, op.Role)
		assert.ElementsMatch(t, tc.params, paramNames(op))
		_, err := op.Build(map[string]json.RawMessage{"tenant_id": raw(99)})
		require.Error(t, err, "caller cannot select another workspace")
	}
	op, _ := Builtin().Get("workspace.update")
	assert.Equal(t, "workspace.get", op.Target.Operation)
	call, err := op.Build(map[string]json.RawMessage{"description": raw("")})
	require.NoError(t, err)
	assert.JSONEq(t, `{"description":""}`, string(call.Body))
	for _, forbidden := range []string{"status", "storage_quota", "credentials", "api_key", "role"} {
		_, err := op.Build(map[string]json.RawMessage{"name": raw("New"), forbidden: raw("injected")})
		require.Error(t, err, forbidden)
	}
	members, _ := Builtin().Get("workspace.list_members")
	call, err = members.Build(
		map[string]json.RawMessage{"q": raw("demo@example.com"), "page": raw(2), "page_size": raw(50)},
	)
	require.NoError(t, err)
	assert.Contains(t, call.URI(), "page=2")
	for _, params := range []map[string]json.RawMessage{{"page": raw(0)}, {"page_size": raw(51)}} {
		_, err := members.Build(params)
		require.Error(t, err)
	}
}

func TestWorkspaceAndSystemProjectionsExcludeInternalData(t *testing.T) {
	for _, id := range []string{"workspace.get", "workspace.update"} {
		op, _ := Builtin().Get(id)
		out := projectJSON(t, op, `{
  "data": {
    "id": 7,
    "name": "Team",
    "description": "",
    "status": "active",
    "storage_quota": 1024,
    "storage_used": 100,
    "credentials": {
      "app_secret": "hidden"
    },
    "assistant_config": {
      "system_prompt": "hidden"
    },
    "api_key": "hidden"
  }
}`)
		assert.Contains(t, out, `"workspace_id":7`)
		assert.Contains(t, out, `"storage_used":100`)
		assert.Contains(t, out, `"description":""`)
		assert.NotContains(t, out, "hidden")
	}
	members, _ := Builtin().Get("workspace.list_members")
	out, err := members.Project([]byte(`{
  "data": {
    "members": [
      {
        "user_id": "u1",
        "username": "Alice",
        "email": "a@example.com",
        "role": "admin",
        "status": "active",
        "password": "hidden",
        "avatar": "hidden",
        "is_system_admin": true
      }
    ],
    "total": 51,
    "page": 2,
    "page_size": 50
  }
}`))
	require.NoError(t, err)
	blob, err := json.Marshal(out)
	require.NoError(t, err)
	assert.Contains(t, string(blob), `"total":51`)
	assert.Contains(t, string(blob), `"email":"a@example.com"`)
	assert.NotContains(t, string(blob), "is_system_admin")
	assert.NotContains(t, string(blob), "hidden")
	system, _ := Builtin().Get("system.get_info")
	info := projectJSON(t, system, `{
  "data": {
    "version": "v1",
    "edition": "lite",
    "uptime_seconds": 90,
    "db_version": "12 (failed)",
    "db_migration_failed": true,
    "db_migration_error": "postgres://hidden",
    "graph_database_engine": "disabled"
  }
}`)
	assert.Contains(t, info, `"db_migration_failed":true`)
	assert.Contains(t, info, `"version":"v1"`)
	assert.NotContains(t, info, "hidden")
}

func TestWorkspaceCatalogKeepsPrivilegedRoutesClosed(t *testing.T) {
	for _, route := range []string{
		"/api/v1/tenants",
		"/api/v1/tenants/:id",
		"/api/v1/tenants/all",
		"/api/v1/tenants/current/api-keys",
		"/api/v1/tenants/kv/:key",
		"/api/v1/system/admin/users",
		"/api/v1/tenants/current/invitations",
	} {
		_, err := NewCatalog(readOp("workspace.read", route))
		require.Error(t, err, route)
	}
	for _, id := range []string{"workspace.get", "workspace.list_members", "system.get_info"} {
		op, _ := Builtin().Get(id)
		op.Method, op.Risk = http.MethodDelete, RiskDestructive
		op.Target = &Target{Operation: "workspace.get", Label: "name"}
		_, err := NewCatalog(op)
		require.Error(t, err, id)
	}
}
