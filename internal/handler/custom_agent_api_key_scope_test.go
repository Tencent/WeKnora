package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type suggestedQuestionsAgentService struct {
	interfaces.CustomAgentService
	err       error
	tagScopes []types.TagScope
}

func (s *suggestedQuestionsAgentService) GetSuggestedQuestions(
	_ context.Context,
	_ string,
	_ []string,
	_ []string,
	tagScopes []types.TagScope,
	_ int,
) ([]types.SuggestedQuestion, error) {
	s.tagScopes = tagScopes
	return nil, s.err
}

func TestGetSuggestedQuestionsPreservesAppErrorStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())

	h := &CustomAgentHandler{service: &suggestedQuestionsAgentService{
		err: apperrors.NewForbiddenError("API key scope does not allow one or more knowledge bases"),
	}}
	r.GET("/agents/:id/suggested-questions", h.GetSuggestedQuestions)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/agents/agent-1/suggested-questions?knowledge_base_ids=kb-blocked", nil)
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestGetSuggestedQuestionsParsesScopedTags(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())

	service := &suggestedQuestionsAgentService{}
	h := &CustomAgentHandler{service: service}
	r.GET("/agents/:id/suggested-questions", h.GetSuggestedQuestions)

	rawScopes := `[{"knowledge_base_id":"kb-1","tag_ids":["tag-1","tag-2"]}]`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodGet,
		"/agents/agent-1/suggested-questions?tag_scopes="+url.QueryEscape(rawScopes),
		nil,
	)
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, []types.TagScope{{KnowledgeBaseID: "kb-1", TagIDs: []string{"tag-1", "tag-2"}}}, service.tagScopes)
}

func TestGetSuggestedQuestionsRejectsInvalidScopedTags(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())

	h := &CustomAgentHandler{service: &suggestedQuestionsAgentService{}}
	r.GET("/agents/:id/suggested-questions", h.GetSuggestedQuestions)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodGet,
		"/agents/agent-1/suggested-questions?tag_scopes="+url.QueryEscape("not-json"),
		nil,
	)
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
}
