package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// savingAgentService stores what the handler hands it, so a test can read
// the allowed_tools list that would reach the database.
type savingAgentService struct {
	interfaces.CustomAgentService
	stored    *types.CustomAgent
	lookupErr error
	saved     *types.CustomAgent
}

func (s *savingAgentService) GetAgentByID(context.Context, string) (*types.CustomAgent, error) {
	return s.stored, s.lookupErr
}

func (s *savingAgentService) CreateAgent(_ context.Context, agent *types.CustomAgent) (*types.CustomAgent, error) {
	s.saved = agent
	return agent, nil
}

func (s *savingAgentService) UpdateAgent(
	_ context.Context, agent *types.CustomAgent, _ *string,
) (*types.CustomAgent, error) {
	s.saved = agent
	return agent, nil
}

func sendAgent(t *testing.T, svc *savingAgentService, method, allowedTools string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	h := &CustomAgentHandler{service: svc}
	r.POST("/agents", h.CreateAgent)
	r.PUT("/agents/:id", h.UpdateAgent)
	path := "/agents"
	if method == http.MethodPut {
		path = "/agents/agent-1"
	}
	body := `{"name":"agent","config":{"agent_mode":"smart-reasoning","allowed_tools":` + allowedTools + `}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	return rec
}

func TestSavingAnAgentChecksItsToolList(t *testing.T) {
	t.Run("create rejects names no tool answers to", func(t *testing.T) {
		svc := &savingAgentService{}
		rec := sendAgent(t, svc, http.MethodPost, `["thinking","serch_knowledge"]`)
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		require.Contains(t, rec.Body.String(), "serch_knowledge")
		require.Nil(t, svc.saved)
	})

	t.Run("create rewrites retired names and drops switch-managed tools", func(t *testing.T) {
		svc := &savingAgentService{}
		rec := sendAgent(t, svc, http.MethodPost, `["knowledge_search","web_search"]`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		require.Equal(t, []string{tools.ToolSearchKnowledge}, svc.saved.Config.AllowedTools)
		var body struct {
			Warnings allowedToolsWarnings `json:"allowed_tools_warnings"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Equal(t, []string{tools.ToolWebSearch}, body.Warnings.Dropped)
	})

	t.Run("create keeps an empty list empty and an unset list unset", func(t *testing.T) {
		svc := &savingAgentService{}
		require.Equal(t, http.StatusCreated, sendAgent(t, svc, http.MethodPost, `[]`).Code)
		require.NotNil(t, svc.saved.Config.AllowedTools)
		require.Empty(t, svc.saved.Config.AllowedTools)

		svc = &savingAgentService{}
		require.Equal(t, http.StatusCreated, sendAgent(t, svc, http.MethodPost, `null`).Code)
		require.Nil(t, svc.saved.Config.AllowedTools)
	})

	t.Run("update keeps unknown names that were already stored", func(t *testing.T) {
		svc := &savingAgentService{stored: &types.CustomAgent{
			ID: "agent-1", Config: types.CustomAgentConfig{AllowedTools: []string{"thinking", "none"}},
		}}
		rec := sendAgent(t, svc, http.MethodPut, `["thinking","none"]`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, []string{"thinking", "none"}, svc.saved.Config.AllowedTools)
		var body struct {
			Warnings allowedToolsWarnings `json:"allowed_tools_warnings"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Equal(t, []string{"none"}, body.Warnings.KeptUnknown)
	})

	t.Run("update rejects unknown names it adds", func(t *testing.T) {
		svc := &savingAgentService{stored: &types.CustomAgent{
			ID: "agent-1", Config: types.CustomAgentConfig{AllowedTools: []string{"none"}},
		}}
		rec := sendAgent(t, svc, http.MethodPut, `["none","nothing"]`)
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		require.Contains(t, rec.Body.String(), "nothing")
		require.Nil(t, svc.saved)
	})

	t.Run("update of a missing agent answers 404 before checking tools", func(t *testing.T) {
		svc := &savingAgentService{lookupErr: service.ErrAgentNotFound}
		rec := sendAgent(t, svc, http.MethodPut, `["nothing"]`)
		require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
		require.Nil(t, svc.saved)
	})
}
