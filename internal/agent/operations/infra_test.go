package operations

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MCP, model and skill writes are admin-only, and none of them can carry a
// credential: secrets stay on the pages where the user types them.
func TestBuiltinInfrastructureWrites(t *testing.T) {
	catalog := Builtin()
	cases := []struct {
		id     string
		risk   Risk
		method string
		params []string
	}{
		{
			"mcp_service.create", RiskWrite, http.MethodPost,
			[]string{"name", "usage_instructions", "transport_type", "url", "auth_type", "api_key_header"},
		},
		{
			"mcp_service.update", RiskWrite, http.MethodPut,
			[]string{"mcp_service_id", "name", "usage_instructions", "enabled", "transport_type", "url"},
		},
		{"mcp_service.delete", RiskDestructive, http.MethodDelete, []string{"mcp_service_id"}},
		{"model.update", RiskWrite, http.MethodPatch, []string{"model_id", "display_name", "description"}},
		{"model.delete", RiskDestructive, http.MethodDelete, []string{"model_id"}},
		{"skill.install", RiskWrite, http.MethodPost, []string{"sandbox_config_id", "source"}},
		{"skill.install_catalog", RiskWrite, http.MethodPost, []string{"catalog_skill_id", "sandbox_config_ids"}},
		{"skill.set_enabled", RiskWrite, http.MethodPatch, []string{"sandbox_config_id", "skill_id", "enabled"}},
		{"skill.uninstall", RiskDestructive, http.MethodDelete, []string{"sandbox_config_id", "skill_id"}},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			op, ok := catalog.Get(tc.id)
			require.True(t, ok)
			assert.Equal(t, tc.risk, op.Risk)
			assert.Equal(t, tc.method, op.Method)
			assert.Equal(t, types.TenantRoleAdmin, op.Role)
			assert.ElementsMatch(t, tc.params, paramNames(op))
			for _, p := range op.Params {
				secrets := []string{"api_key", "token", "client_secret", "headers", "custom_headers", "envs"}
				assert.NotContains(t, secrets, p.Name)
			}
		})
	}

	create, _ := catalog.Get("mcp_service.create")
	transport, _ := create.Param("transport_type")
	assert.ElementsMatch(t, []string{"sse", "http-streamable"}, transport.Enum, "stdio is never offered")
	auth, _ := create.Param("auth_type")
	assert.Equal(t, "auth_config.auth_type", auth.Key)

	enabled, _ := catalog.Get("skill.set_enabled")
	toggle, _ := enabled.Param("enabled")
	assert.True(t, toggle.Required, "a toggle without enabled would be an env write")
}

func TestBuiltinSkillInstallsAlwaysAsk(t *testing.T) {
	catalog := Builtin()
	for _, id := range []string{"skill.install", "skill.install_catalog"} {
		op, ok := catalog.Get(id)
		require.True(t, ok, id)
		assert.True(t, op.AlwaysConfirm, id)
		assert.True(t, Access{Level: types.AssistantAccessAutoWrite}.NeedsApproval(op), id)
	}
	install, _ := catalog.Get("skill.install")
	source, _ := install.Param("source")
	assert.True(t, source.Required)
	assert.Equal(t, "source", install.LabelParam)

	call, err := install.Build(map[string]json.RawMessage{
		"sandbox_config_id": json.RawMessage(`"cfg-1"`),
		"source":            json.RawMessage(`"https://github.com/acme/skills/tree/main/pdf"`),
	})
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/sandbox-configs/cfg-1/skills", call.Path)
	assert.JSONEq(t, `{"source":"https://github.com/acme/skills/tree/main/pdf"}`, string(call.Body))
}

func TestBuiltinInfrastructureProjections(t *testing.T) {
	catalog := Builtin()
	project := func(id, body string) string {
		t.Helper()
		op, ok := catalog.Get(id)
		require.True(t, ok, id)
		projected, err := op.Project([]byte(body))
		require.NoError(t, err)
		out, err := json.Marshal(projected.Data)
		require.NoError(t, err)
		return string(out)
	}

	assert.JSONEq(t, `{"mcp_service_id":"m1","name":"Docs","description":"","enabled":true,
		"transport_type":"sse","is_builtin":false,"auth_type":"api_key"}`,
		project("mcp_service.get", `{"data":{"id":"m1","name":"Docs","description":"","enabled":true,
		"transport_type":"sse","is_builtin":false,"url":"https://mcp.example/sse",
		"auth_config":{"auth_type":"api_key","api_key_header":"X-Key","custom_headers":{"Authorization":"x"}},
		"credentials":{"api_key":{"configured":true}}}}`))

	assert.JSONEq(t, `[{"skill_id":"s1","name":"pdf","enabled":true,"status":"ready","missing_envs":["API_TOKEN"]}]`,
		project("skill.list_installed", `{"data":[{"id":"s1","name":"pdf","enabled":true,"status":"ready",
		"bundle_sha256":"abc","install_session_id":"sess","missing_envs":["API_TOKEN"],
		"envs":[{"name":"API_TOKEN","required":true,"is_set":false}]}]}`))

	assert.JSONEq(t, `[{"catalog_skill_id":"c1","name":"pdf","installed_on":["cfg-1"]}]`,
		project("skill.list_catalog", `{"data":[{"id":"c1","name":"pdf","installed_on":["cfg-1"],
		"installations":[{"skill_id":"s1","sandbox_config_id":"cfg-1"}]}]}`))

	assert.JSONEq(t, `{"success":false,"started":["cfg-1"],"failed":["cfg-2: not found"]}`,
		project("skill.install_catalog", `{"success":false,"data":{"installs":{"cfg-1":"s1"},
		"errors":{"cfg-2":"not found"},"started":["cfg-1"],"failed":["cfg-2: not found"]}}`))
}
