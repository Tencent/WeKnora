package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAgentTrialCannotExecuteToolsOrCrossWorkspace(t *testing.T) {
	for _, tenant := range []uint64{1, 2} {
		model := &usageChatModel{result: &types.ChatResponse{Content: "test answer"}}
		models := &usageModelService{chat: model}
		agent := patchTestAgent()
		agent.TenantID = tenant
		agent.Config.MCPSelectionMode = "all"
		agent.Config.AllowedTools = []string{"wiki_delete_page"}
		h := &CustomAgentHandler{service: &savingAgentService{stored: agent}, models: models}
		r := gin.New()
		r.POST("/agents/:id/trial", h.TrialAgent)
		req := httptest.NewRequest(http.MethodPost, "/agents/custom-1/trial", strings.NewReader(`{"prompt":"test"}`))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(context.WithValue(req.Context(), types.TenantIDContextKey, uint64(1)))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if tenant != 1 {
			require.Equal(t, http.StatusNotFound, rec.Code)
			require.Empty(t, models.selected)
			continue
		}
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Contains(t, rec.Body.String(), "prompt_and_model_only")
		require.Equal(t, 2048, model.options.MaxTokens)
		require.Empty(t, model.options.Tools)
		require.Equal(t, "none", model.options.ToolChoice)
		require.Len(t, model.messages, 2)
		require.Equal(t, agent.Config.SystemPrompt, model.messages[0].Content)
		require.Equal(t, []string{"wiki_delete_page"}, agent.Config.AllowedTools)
	}
}

func TestRetrievalPreviewBoundsScopeAndResults(t *testing.T) {
	for _, mode := range []string{"keyword", "vector", "hybrid"} {
		svc := &hybridSearchTestService{results: []*types.SearchResult{{ID: "one"}, {ID: "two"}}}
		h := &KnowledgeBaseHandler{service: svc}
		r := gin.New()
		r.POST("/kb/:id", h.PreviewRetrieval)
		req := httptest.NewRequest(http.MethodPost, "/kb/kb1", strings.NewReader(
			`{"query":"test","limit":1,"mode":"`+mode+`","knowledge_base_ids":["other"]}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, 1, svc.searchCalls)
		require.Empty(t, svc.searchParams.KnowledgeBaseIDs)
		require.Equal(t, mode == "keyword", svc.searchParams.DisableVectorMatch)
		require.Equal(t, mode == "vector", svc.searchParams.DisableKeywordsMatch)
		require.Contains(t, rec.Body.String(), "one")
		require.NotContains(t, rec.Body.String(), "two")
	}
}

func TestPatchAgentToolAndSandboxFieldsKeepValidation(t *testing.T) {
	repo := &agentAvatarRepository{agent: patchTestAgent()}
	rec := servePatch(t, repo, `{"allowed_tools":["wiki_read_page"],"sandbox_config_id":""}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, []string{"wiki_read_page"}, repo.agent.Config.AllowedTools)
	require.Equal(t, "Be brief.", repo.agent.Config.SystemPrompt)
	writes := repo.writes
	rec = servePatch(t, repo, `{"allowed_tools":["invented-tool"]}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, writes, repo.writes)
	rec = servePatchWith(t, repo, `{"sandbox_config_id":"remote"}`, &CustomAgentHandler{desktop: true})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, writes, repo.writes)
}
