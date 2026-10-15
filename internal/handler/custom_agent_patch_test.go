package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/sandbox"
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
	return servePatchWithSkills(t, repo, body, nil)
}

func servePatchWithSkills(
	t *testing.T, repo *agentAvatarRepository, body string, skills usableSkillLister,
) *httptest.ResponseRecorder {
	t.Helper()
	return servePatchWith(t, repo, body, &CustomAgentHandler{usableSkills: skills})
}

func servePatchWith(
	t *testing.T, repo *agentAvatarRepository, body string, h *CustomAgentHandler,
) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h.service = service.NewCustomAgentService(repo, nil, nil, nil, nil, nil, nil, nil)
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
	assert.Equal(t, types.AgentModeQuickAnswer, got.Config.AgentMode)
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
		"unknown mode":  `{"kb_selection_mode":"some"}`,
		"unknown agent": `{"agent_mode":"agent"}`,
		"blank name":    `{"name":"  "}`,
		"not json":      `{`,
	} {
		t.Run(name, func(t *testing.T) {
			repo := &agentAvatarRepository{agent: patchTestAgent()}
			rec := servePatch(t, repo, body)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.Zero(t, repo.writes)
		})
	}
}

func TestPatchAgentMode(t *testing.T) {
	repo := &agentAvatarRepository{agent: patchTestAgent()}
	rec := servePatch(t, repo, `{"agent_mode":" smart-reasoning "}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, types.AgentModeSmartReasoning, repo.agent.Config.AgentMode)
	assert.True(t, repo.agent.Config.MultiTurnEnabled, "smart-reasoning keeps earlier turns")
	assert.Equal(t, "Be brief.", repo.agent.Config.SystemPrompt)
	assert.Equal(t, "Support", repo.agent.Name)

	rec = servePatch(t, repo, `{"agent_mode":"quick-answer"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, types.AgentModeQuickAnswer, repo.agent.Config.AgentMode)
	assert.True(t, repo.agent.Config.MultiTurnEnabled, "leaving smart-reasoning does not turn multi-turn off")
}

func TestPatchAgentNotFound(t *testing.T) {
	repo := &agentAvatarRepository{err: service.ErrAgentNotFound}
	rec := servePatch(t, repo, `{"name":"x"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
}

func TestPatchAgentSkills(t *testing.T) {
	for _, tc := range []struct {
		name, body, mode string
		skills           []string
	}{
		{"names alone", `{"selected_skills":["ppt-generator"]}`, "selected", []string{"ppt-generator"}},
		{"explicit selected", `{"skills_selection_mode":"selected"}`, "selected", []string{"ppt-generator"}},
		{"empty selection", `{"selected_skills":[]}`, "selected", []string{}},
		{"all clears names", `{"skills_selection_mode":"all"}`, "all", []string{}},
		{"none clears names", `{"skills_selection_mode":"none"}`, "none", []string{}},
		{"explicit all wins", `{"skills_selection_mode":"all","selected_skills":["unknown"]}`, "all", []string{}},
		{"explicit none wins", `{"skills_selection_mode":"none","selected_skills":["unknown"]}`, "none", []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent := patchTestAgent()
			agent.Config.SandboxConfigID = "cfg-1"
			agent.Config.SkillsSelectionMode = "none"
			agent.Config.SelectedSkills = []string{"ppt-generator"}
			agent.EnsureDefaults()
			want := agent.Config
			want.SkillsSelectionMode, want.SelectedSkills = tc.mode, tc.skills
			repo := &agentAvatarRepository{agent: agent}
			lister := &fakeUsableSkillLister{skills: []*types.TenantSkillEntity{nil, {Name: "ppt-generator"}}}

			rec := servePatchWithSkills(t, repo, tc.body, lister)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Equal(t, 1, repo.writes)
			assert.Equal(t, want, repo.agent.Config, "unmentioned config fields must be kept")
			assert.Equal(t, "Support", repo.agent.Name)
			assert.Equal(t, "Answers tickets", repo.agent.Description)
			assert.Equal(t, "🤖", repo.agent.Avatar)
			if tc.mode == "selected" && len(tc.skills) > 0 {
				assert.Equal(t, uint64(1), lister.tenantID)
				assert.Equal(t, "cfg-1", lister.configID)
			} else {
				assert.Empty(t, lister.configID)
			}
		})
	}
}

func TestPatchAgentRejectsUnavailableSkills(t *testing.T) {
	for _, body := range []string{
		`{"selected_skills":["ppt-generator","unknown","PPT-generator"]}`,
		`{"skills_selection_mode":"selected"}`,
	} {
		t.Run(body, func(t *testing.T) {
			agent := patchTestAgent()
			agent.Config.SandboxConfigID = "cfg-1"
			agent.Config.SkillsSelectionMode = "selected"
			agent.Config.SelectedSkills = []string{"unknown", "PPT-generator"}
			repo := &agentAvatarRepository{agent: agent}
			lister := &fakeUsableSkillLister{skills: []*types.TenantSkillEntity{{Name: "ppt-generator"}}}
			rec := servePatchWithSkills(t, repo, body, lister)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), "Unknown or unavailable skills: unknown, PPT-generator")
			assert.Zero(t, repo.writes)
			assert.Equal(t, "Support", repo.agent.Name)
			assert.Equal(t, []string{"unknown", "PPT-generator"}, repo.agent.Config.SelectedSkills)
		})
	}
}

func TestPatchAgentKeepsSavedSkillsOnUnrelatedChange(t *testing.T) {
	agent := patchTestAgent()
	agent.Config.SandboxConfigID = "cfg-1"
	agent.Config.SkillsSelectionMode = "selected"
	agent.Config.SelectedSkills = []string{"uninstalled"}
	repo := &agentAvatarRepository{agent: agent}
	lister := &fakeUsableSkillLister{}

	rec := servePatchWithSkills(t, repo, `{"name":"Renamed"}`, lister)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "Renamed", repo.agent.Name)
	assert.Equal(t, []string{"uninstalled"}, repo.agent.Config.SelectedSkills)
	assert.Zero(t, lister.tenantID, "saved skills are not re-checked")
}

func TestPatchAgentSkillsOnLiteUseHostTarget(t *testing.T) {
	repo := &agentAvatarRepository{agent: patchTestAgent()}
	lister := &fakeUsableSkillLister{skills: []*types.TenantSkillEntity{{Name: "ppt-generator"}}}
	h := &CustomAgentHandler{usableSkills: lister, desktop: true}

	rec := servePatchWith(t, repo, `{"selected_skills":["ppt-generator"]}`, h)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, sandbox.HostSkillTargetID, lister.configID)
	assert.Empty(t, repo.agent.Config.SandboxConfigID)
	assert.Equal(t, []string{"ppt-generator"}, repo.agent.Config.SelectedSkills)
}

func TestPatchAgentSkillsRequireSandbox(t *testing.T) {
	for _, body := range []string{
		`{"skills_selection_mode":"all"}`,
		`{"skills_selection_mode":"selected"}`,
		`{"selected_skills":["ppt-generator"]}`,
		`{"selected_skills":[]}`,
	} {
		t.Run(body, func(t *testing.T) {
			repo := &agentAvatarRepository{agent: patchTestAgent()}
			lister := &fakeUsableSkillLister{}
			rec := servePatchWithSkills(t, repo, body, lister)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), "Choose a sandbox in the agent editor")
			assert.Zero(t, repo.writes)
			assert.Zero(t, lister.tenantID)
		})
	}
	for _, body := range []string{`{"skills_selection_mode":"none"}`, `{"name":"Renamed"}`} {
		repo := &agentAvatarRepository{agent: patchTestAgent()}
		repo.agent.Config.SkillsSelectionMode = "all"
		rec := servePatch(t, repo, body)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	}
}

func TestPatchAgentRejectsInvalidSkillsInput(t *testing.T) {
	tooMany, err := json.Marshal(map[string]any{"selected_skills": make([]string, 51)})
	require.NoError(t, err)
	for _, body := range []string{
		`{"skills_selection_mode":"some"}`, `{"skills_selection_mode":""}`,
		`{"selected_skills":"ppt-generator"}`, string(tooMany),
	} {
		t.Run(body, func(t *testing.T) {
			repo := &agentAvatarRepository{agent: patchTestAgent()}
			repo.agent.Config.SandboxConfigID = "cfg-1"
			rec := servePatch(t, repo, body)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.Zero(t, repo.writes)
		})
	}
}

func TestPatchAgentSkillsFailClosedWithoutLister(t *testing.T) {
	repo := &agentAvatarRepository{agent: patchTestAgent()}
	repo.agent.Config.SandboxConfigID = "cfg-1"
	rec := servePatch(t, repo, `{"selected_skills":["ppt-generator"]}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.Zero(t, repo.writes)
}
