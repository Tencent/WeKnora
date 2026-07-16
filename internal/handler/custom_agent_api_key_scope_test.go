package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type suggestedQuestionsAgentService struct {
	interfaces.CustomAgentService
	err error
}

func (s *suggestedQuestionsAgentService) GetSuggestedQuestions(
	context.Context,
	string,
	[]string,
	[]string,
	[]string,
	int,
) ([]types.SuggestedQuestion, error) {
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
