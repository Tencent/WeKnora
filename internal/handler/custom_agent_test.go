package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestValidateAgentSandboxConfigRejectsAnyConfigOnLite(t *testing.T) {
	h := &CustomAgentHandler{desktop: true}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	err := h.validateAgentSandboxConfig(ctx, types.CustomAgentConfig{SandboxConfigID: "cfg-1"})
	require.Error(t, err)
	require.NoError(t, h.validateAgentSandboxConfig(ctx, types.CustomAgentConfig{}))
}

func TestGetAgentToolsServesTheToolCatalog(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/agents/tools", nil)

	(&CustomAgentHandler{}).GetAgentTools(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Tools []struct {
				Name        string `json:"name"`
				Group       string `json:"group"`
				Danger      bool   `json:"danger"`
				Requirement struct {
					AnyOf []string `json:"any_of"`
					AllOf []string `json:"all_of"`
				} `json:"requirement"`
			} `json:"tools"`
			DefaultTools       []string `json:"default_tools"`
			SwitchManagedTools []string `json:"switch_managed_tools"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.True(t, body.Success)

	byName := make(map[string]int, len(body.Data.Tools))
	for i, tool := range body.Data.Tools {
		byName[tool.Name] = i
	}
	for _, name := range []string{
		tools.ToolSearchConversations, tools.ToolSearchKnowledge, tools.ToolWikiDeletePage, tools.ToolWikiSearch,
	} {
		require.Contains(t, byName, name)
	}
	search := body.Data.Tools[byName[tools.ToolSearchKnowledge]]
	require.Equal(t, tools.ToolGroupRAG, search.Group)
	require.ElementsMatch(t, []string{"vector", "keyword"}, search.Requirement.AnyOf)
	require.True(t, body.Data.Tools[byName[tools.ToolWikiDeletePage]].Danger)
	require.Equal(t, []string{"wiki"}, body.Data.Tools[byName[tools.ToolWikiSearch]].Requirement.AllOf)
	require.Equal(t, tools.DefaultAllowedTools(), body.Data.DefaultTools)
	require.Equal(t, tools.SwitchManagedTools(), body.Data.SwitchManagedTools)
}
