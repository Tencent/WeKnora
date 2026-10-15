package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func patchTestAgent() *types.CustomAgent {
	return &types.CustomAgent{
		ID: "custom-1", Name: "Support", Description: "Answers tickets", Avatar: "🤖", TenantID: 1,
		Config: types.CustomAgentConfig{
			AgentMode:        types.AgentModeQuickAnswer,
			SystemPrompt:     "Be brief.",
			ModelID:          "model-1",
			KBSelectionMode:  "selected",
			KnowledgeBases:   []string{"kb-1"},
			MCPSelectionMode: "none",
			Temperature:      0.3,
			WebSearchEnabled: true,
		},
	}
}

func servePatch(t *testing.T, repo *agentAvatarRepository, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := service.NewCustomAgentService(repo, nil, nil, nil, nil, nil, nil, nil)
	h := &CustomAgentHandler{service: svc}
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	router.PATCH("/agents/:id", h.PatchAgent)
	req := httptest.NewRequest(http.MethodPatch, "/agents/custom-1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), types.TenantIDContextKey, uint64(1)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestPatchAgentKeepsFieldsItDoesNotCarry(t *testing.T) {
	repo := &agentAvatarRepository{agent: patchTestAgent()}
	rec := servePatch(t, repo, `{"name":"Helpdesk","model_id":"model-2"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, repo.writes)

	got := repo.agent
	assert.Equal(t, "Helpdesk", got.Name)
	assert.Equal(t, "model-2", got.Config.ModelID)
	assert.Equal(t, "Answers tickets", got.Description)
	assert.Equal(t, "🤖", got.Avatar)
	assert.Equal(t, "Be brief.", got.Config.SystemPrompt)
	assert.Equal(t, []string{"kb-1"}, got.Config.KnowledgeBases)
	assert.Equal(t, 0.3, got.Config.Temperature)
	assert.True(t, got.Config.WebSearchEnabled)
}

func TestPatchAgentSelectionLists(t *testing.T) {
	repo := &agentAvatarRepository{agent: patchTestAgent()}
	repo.agent.Config.KBSelectionMode = "all"
	rec := servePatch(t, repo,
		`{"knowledge_bases":["kb-2","kb-3"],"mcp_services":["mcp-1"],"web_search_enabled":false}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	cfg := repo.agent.Config
	assert.Equal(t, "selected", cfg.KBSelectionMode, "a list alone switches the scope to selected")
	assert.Equal(t, []string{"kb-2", "kb-3"}, cfg.KnowledgeBases)
	assert.Equal(t, "selected", cfg.MCPSelectionMode)
	assert.Equal(t, []string{"mcp-1"}, cfg.MCPServices)
	assert.False(t, cfg.WebSearchEnabled)

	rec = servePatch(t, repo, `{"knowledge_bases":[],"kb_selection_mode":"none"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "none", repo.agent.Config.KBSelectionMode, "an explicit mode wins")
	assert.Empty(t, repo.agent.Config.KnowledgeBases)
}

func TestPatchAgentRejectsBadInput(t *testing.T) {
	for name, body := range map[string]string{
		"unknown mode": `{"kb_selection_mode":"some"}`,
		"blank name":   `{"name":"  "}`,
		"not json":     `{`,
	} {
		t.Run(name, func(t *testing.T) {
			repo := &agentAvatarRepository{agent: patchTestAgent()}
			rec := servePatch(t, repo, body)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.Zero(t, repo.writes)
		})
	}
}

func TestPatchAgentNotFound(t *testing.T) {
	repo := &agentAvatarRepository{err: service.ErrAgentNotFound}
	rec := servePatch(t, repo, `{"name":"x"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
}
