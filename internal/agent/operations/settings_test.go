package operations

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchProviderOperationsAreScopedAndExcludeSecrets(t *testing.T) {
	for _, name := range []string{"list", "get", "list_types", "test", "update", "delete"} {
		op, ok := Builtin().Get("web_search_provider." + name)
		require.True(t, ok, name)
		role := types.TenantRoleViewer
		if name == "update" || name == "delete" || name == "test" {
			role = types.TenantRoleAdmin
		}
		assert.Equal(t, role, op.Role)
	}
	get, _ := Builtin().Get("web_search_provider.get")
	out := projectJSON(t, get, `{
  "data": {
    "id": "p1",
    "name": "Search",
    "provider": "google",
    "is_default": true,
    "parameters": {
      "api_key": "secret",
      "proxy_url": "secret"
    },
    "credentials": {
      "api_key": {
        "configured": true
      }
    },
    "tenant_id": 9
  }
}`)
	assert.JSONEq(t, `{
  "data": {
    "web_search_provider_id": "p1",
    "name": "Search",
    "provider": "google",
    "is_default": true
  }
}`, out)
	update, _ := Builtin().Get("web_search_provider.update")
	call, err := update.Build(
		map[string]json.RawMessage{
			"web_search_provider_id": raw("p1"),
			"is_default":             raw(false),
			"description":            raw(""),
		},
	)
	require.NoError(t, err)
	assert.Equal(t, http.MethodPatch, call.Method)
	assert.JSONEq(t, `{"is_default":false,"description":""}`, string(call.Body))
	for _, key := range []string{"parameters", "api_key", "base_url", "enabled", "tenant_id"} {
		_, err = update.Build(map[string]json.RawMessage{"web_search_provider_id": raw("p1"), key: raw("bad")})
		require.Error(t, err, key)
	}
	probe, _ := Builtin().Get("web_search_provider.test")
	assert.Equal(t, RiskProbe, probe.Risk)
	assert.JSONEq(
		t,
		`{"data":{"success":false,"error":"Connection failed"}}`,
		projectJSON(t, probe, `{"success":false,"error":"Connection failed"}`),
	)
	remove, _ := Builtin().Get("web_search_provider.delete")
	assert.Equal(t, RiskDestructive, remove.Risk)
}

func TestSyncSettingsCatalogPreservesExplicitValuesAndLimitsSelection(t *testing.T) {
	op, ok := Builtin().Get("data_source.update_sync")
	require.True(t, ok)
	assert.Equal(t, types.TenantRoleAdmin, op.Role)
	assert.Equal(t, RiskWrite, op.Risk)
	call, err := op.Build(
		map[string]json.RawMessage{
			"data_source_id": raw("ds1"),
			"sync_schedule":  raw(""),
			"sync_deletions": raw(false),
			"resource_ids":   raw([]string{"a"}),
		},
	)
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/datasource/ds1/sync-settings", call.URI())
	assert.JSONEq(t, `{"sync_schedule":"","sync_deletions":false,"resource_ids":["a"]}`, string(call.Body))
	for _, params := range []map[string]json.RawMessage{
		{
			"sync_mode": raw(
				"bad",
			),
		}, {
			"conflict_strategy": raw(
				"bad",
			),
		}, {
			"resource_ids": raw(
				make(
					[]string,
					51,
				),
			),
		}, {
			"credentials": raw(
				"secret",
			),
		}, {
			"status": raw(
				"active",
			),
		},
	} {
		params["data_source_id"] = raw("ds1")
		_, err = op.Build(params)
		require.Error(t, err)
	}
	get, _ := Builtin().Get("data_source.get")
	assert.JSONEq(t, `{"data":{"resource_ids":["a","b"]}}`, projectJSON(t, get, `{
  "config": {
    "resource_ids": [
      "a",
      "b"
    ],
    "credentials": {
      "token": "secret"
    },
    "settings": {
      "key": "secret"
    }
  }
}`))
	resources, _ := Builtin().Get("data_source.list_resources")
	call, err = resources.Build(map[string]json.RawMessage{"data_source_id": raw("ds1"), "parent_id": raw("folder")})
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/datasource/ds1/resources?parent_id=folder", call.URI())
	out := projectJSON(t, resources, `[
  {
    "external_id": "a",
    "name": "Docs",
    "has_children": true,
    "url": "secret",
    "metadata": {
      "token": "secret"
    }
  }
]`)
	assert.Contains(t, out, `"external_id":"a"`)
	assert.NotContains(t, out, "secret")
}

func TestAgentSearchProviderCanReturnToDefault(t *testing.T) {
	op, _ := Builtin().Get("agent.update")
	call, err := op.Build(
		map[string]json.RawMessage{
			"agent_id":               raw("a"),
			"web_search_provider_id": raw(""),
			"web_search_enabled":     raw(false),
		},
	)
	require.NoError(t, err)
	assert.JSONEq(t, `{"web_search_provider_id":"","web_search_enabled":false}`, string(call.Body))
}
