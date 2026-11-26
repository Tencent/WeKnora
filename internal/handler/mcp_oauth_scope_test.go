package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func scopeTenantCall(t *testing.T, query string, agent *types.CustomAgent) (uint64, error) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/x?"+query, nil)
	c.Set(types.TenantIDContextKey.String(), uint64(10005))
	c.Set(types.UserIDContextKey.String(), "u1")
	h := &MCPOAuthHandler{agents: &scopedAgentStub{agent: agent}}
	return h.scopeTenant(c, 10005, "svc-1")
}

func TestMCPOAuthScopeTenant(t *testing.T) {
	agent := &types.CustomAgent{TenantID: 10000, Config: types.CustomAgentConfig{
		MCPSelectionMode: "selected", MCPServices: []string{"svc-1"},
	}}

	got, err := scopeTenantCall(t, "", nil)
	require.NoError(t, err)
	require.Equal(t, uint64(10005), got, "no agent context keeps the caller workspace")

	got, err = scopeTenantCall(t, "agent_id=a1&agent_source_tenant_id=10000", agent)
	require.NoError(t, err)
	require.Equal(t, uint64(10000), got, "shared agent resolves to its owner's workspace")

	agent.Config.MCPServices = []string{"other"}
	_, err = scopeTenantCall(t, "agent_id=a1&agent_source_tenant_id=10000", agent)
	require.Error(t, err, "service outside the agent's preset is refused")
}
