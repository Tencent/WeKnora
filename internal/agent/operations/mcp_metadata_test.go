package operations

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPMetadataOperations(t *testing.T) {
	for _, id := range []string{"mcp_service.get_metadata", "mcp_service.refresh_metadata"} {
		op, ok := Builtin().Get(id)
		require.True(t, ok)
		assert.Equal(t, types.TenantRoleViewer, op.Role)
		assert.Equal(t, []string{"mcp_service_id"}, paramNames(op))
		call, err := op.Build(map[string]json.RawMessage{"mcp_service_id": raw("svc")})
		require.NoError(t, err)
		if id == "mcp_service.get_metadata" {
			assert.Equal(t, RiskRead, op.Risk)
			assert.Equal(t, http.MethodGet, call.Method)
			assert.Equal(t, "/api/v1/mcp-services/svc/metadata", call.URI())
		} else {
			assert.Equal(t, RiskWrite, op.Risk)
			assert.Equal(t, http.MethodPost, call.Method)
			assert.Equal(t, "/api/v1/mcp-services/svc/metadata/refresh", call.URI())
			assert.Equal(t, "mcp_service.get", op.Target.Operation)
			assert.Equal(t, "name", op.Target.Label)
		}
		assert.JSONEq(
			t,
			`{"data":{},"meta":{"synced":false,"tool_count":0}}`,
			projectJSON(t, op, `{"success":true,"synced":false,"tool_count":0,"data":null}`),
		)
		out := projectJSON(t, op, `{"synced":true,"tool_count":0,"data":{"service_id":"svc","tools":[],"stale":false}}`)
		assert.Contains(t, out, `"synced":true`)
		assert.Contains(t, out, `"tools":[]`)
		assert.Contains(t, out, `"mcp_service_id":"svc"`)
		out = projectJSON(t, op, `{
  "synced": true,
  "tool_count": 1,
  "data": {
    "stale": true,
    "tools": [
      {
        "name": "search",
        "description": "Search documents",
        "inputSchema": {
          "token": "SECRET"
        }
      }
    ],
    "instructions": "DO NOT EXPOSE",
    "principal": "PRIVATE",
    "config_fingerprint": "PRIVATE"
  }
}`)
		assert.Contains(t, out, `"stale":true`)
		assert.Contains(t, out, `"name":"search"`)
		assert.NotContains(t, out, "SECRET")
		assert.NotContains(t, out, "PRIVATE")
		assert.NotContains(t, out, "DO NOT EXPOSE")
	}
}

func TestMCPMetadataProjectionReportsIncompleteDirectory(t *testing.T) {
	tools := make([]map[string]any, 250)
	for i := range tools {
		tools[i] = map[string]any{"name": fmt.Sprintf("tool_%d", i), "description": "reference"}
	}
	body, err := json.Marshal(
		map[string]any{"synced": true, "tool_count": len(tools), "data": map[string]any{"tools": tools}},
	)
	require.NoError(t, err)
	op, _ := Builtin().Get("mcp_service.get_metadata")
	out, err := op.Project(body)
	require.NoError(t, err)
	assert.True(t, out.Truncated)
	assert.Equal(t, json.Number("250"), out.Meta["tool_count"])
	assert.Len(t, out.Data.(map[string]any)["tools"], defaultMaxListItems)
}
