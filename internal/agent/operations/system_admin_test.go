package operations

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystemAdminOperationsRequireIndependentPermission(t *testing.T) {
	for _, id := range []string{"list_admins", "list_users", "list_settings", "get_queues", "list_tasks"} {
		op, ok := Builtin().Get("system_admin." + id)
		require.True(t, ok)
		require.True(t, op.SystemAdmin)
		assert.Equal(t, "GET", op.Method)
		assert.Equal(t, RiskRead, op.Risk)
		for _, enforce := range []bool{false, true} {
			for _, role := range []types.TenantRole{
				types.TenantRoleViewer,
				types.TenantRoleAdmin,
				types.TenantRoleOwner,
			} {
				access := Access{Role: role, EnforceRole: enforce, Level: types.AssistantAccessReadOnly}
				why, denied := Builtin().Withheld(access, op)
				assert.True(t, denied)
				assert.True(t, why.SystemAdmin)
				access.SystemAdmin = true
				_, denied = Builtin().Withheld(access, op)
				assert.False(t, denied)
			}
		}
		withoutPermission := op
		withoutPermission.SystemAdmin = false
		_, err := NewCatalog(withoutPermission)
		require.Error(t, err, "platform routes must opt into system-admin gating")
	}
	for _, route := range []string{
		"/api/v1/system/admin/settings",
		"/api/v1/system/admin/settings/:key",
		"/api/v1/system/admin/api-keys",
		"/api/v1/system/admin/promote",
		"/api/v1/system/admin/users/status",
	} {
		op := readOp("system_admin.forbidden", route)
		op.SystemAdmin = true
		_, err := NewCatalog(op)
		require.Error(t, err, route)
	}
}

func TestSystemAdminPaginationAndSafeProjections(t *testing.T) {
	users, _ := Builtin().Get("system_admin.list_users")
	call, err := users.Build(map[string]json.RawMessage{"offset": raw(50), "limit": raw(50)})
	require.NoError(t, err)
	assert.Contains(t, call.URI(), "offset=50")
	_, err = users.Build(map[string]json.RawMessage{"limit": raw(51)})
	require.Error(t, err)
	out, err := users.Project([]byte(`{
  "users": [
    {
      "id": "u1",
      "username": "Name",
      "email": "a@example.com",
      "is_active": false,
      "is_system_admin": true,
      "password": "secret",
      "preferences": {
        "hidden": "secret"
      },
      "avatar": "secret"
    }
  ],
  "has_more": true,
  "limit": 50,
  "offset": 50
}`))
	require.NoError(t, err)
	blob, _ := json.Marshal(out)
	assert.Contains(t, string(blob), `"has_more":true`)
	assert.Contains(t, string(blob), `"is_active":false`)
	assert.NotContains(t, string(blob), "secret")
	tasks, _ := Builtin().Get("system_admin.list_tasks")
	params := map[string]json.RawMessage{
		"queue":     raw("default"),
		"state":     raw("archived"),
		"cursor":    raw("opaque=+/"),
		"page_size": raw(20),
	}
	call, err = tasks.Build(params)
	require.NoError(t, err)
	assert.Contains(t, call.URI(), "cursor=opaque%3D%2B%2F")
	params["state"] = raw("failed")
	_, err = tasks.Build(params)
	require.Error(t, err)
	out, err = tasks.Project([]byte(`{
  "available": true,
  "tasks": [
    {
      "id": "t1",
      "state": "archived",
      "error_category": "timeout",
      "last_error": "secret",
      "payload": "secret",
      "worker": "secret",
      "tenant_id": 9,
      "knowledge_id": "k1"
    }
  ],
  "has_more": true,
  "next_cursor": "opaque"
}`))
	require.NoError(t, err)
	blob, _ = json.Marshal(out)
	assert.Contains(t, string(blob), `"next_cursor":"opaque"`)
	assert.Contains(t, string(blob), `"error_category":"timeout"`)
	assert.NotContains(t, string(blob), "secret")
	queues, _ := Builtin().Get("system_admin.get_queues")
	text := projectJSON(t, queues, `{
  "available": false,
  "queues_error": true,
  "workers_error": true,
  "models_error": true,
  "queues": [],
  "pools": [
    {
      "name": "core",
      "concurrency": 8,
      "cluster_capacity": 16,
      "active": 2
    }
  ],
  "models": [
    {
      "model_id": "m1",
      "name": "Model",
      "active": 1,
      "waiting": 3,
      "limit": 4
    }
  ]
}`)
	assert.Contains(t, text, `"available":false`)
	assert.Contains(t, text, `"queues_error":true`)
	assert.Contains(t, text, `"cluster_capacity":16`)
	assert.Contains(t, text, `"waiting":3`)
}

func TestSystemAdminOperationsDiscoverableInChinese(t *testing.T) {
	for query, id := range map[string]string{
		"系统管理员": "system_admin.list_admins",
		"平台用户":  "system_admin.list_users",
		"失败":    "system_admin.list_tasks",
	} {
		found := false
		for _, op := range Builtin().Find(query, "") {
			if op.ID == id {
				found = true
			}
		}
		assert.True(t, found, query)
	}
}
