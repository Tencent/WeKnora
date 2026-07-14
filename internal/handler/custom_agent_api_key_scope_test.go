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
	err          error
	folderScopes []types.FolderScope
	kbIDs        []string
}

func (s *suggestedQuestionsAgentService) GetSuggestedQuestionsWithFolders(
	_ context.Context,
	_ string,
	kbIDs []string,
	_ []string,
	_ []string,
	_ int,
	folderScopes []types.FolderScope,
) ([]types.SuggestedQuestion, error) {
	s.kbIDs = kbIDs
	s.folderScopes = folderScopes
	return nil, s.err
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

func TestGetSuggestedQuestionsAcceptsSingleKBFolderIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	service := &suggestedQuestionsAgentService{}
	h := &CustomAgentHandler{service: service}
	r.GET("/agents/:id/suggested-questions", h.GetSuggestedQuestions)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodGet,
		"/agents/agent-1/suggested-questions?knowledge_base_ids=kb-1&folder_ids=folder-a,folder-b",
		nil,
	)
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if len(service.folderScopes) != 1 || service.folderScopes[0].KnowledgeBaseID != "kb-1" {
		t.Fatalf("folder scopes = %#v", service.folderScopes)
	}
	if len(service.kbIDs) != 0 {
		t.Fatalf("folder_ids shorthand must not also select the whole KB: %#v", service.kbIDs)
	}
	if got := service.folderScopes[0].FolderIDs; len(got) != 2 || got[0] != "folder-a" || got[1] != "folder-b" {
		t.Fatalf("folder ids = %#v", got)
	}
}

func TestGetSuggestedQuestionsRejectsAmbiguousFolderIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	h := &CustomAgentHandler{service: &suggestedQuestionsAgentService{}}
	r.GET("/agents/:id/suggested-questions", h.GetSuggestedQuestions)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodGet,
		"/agents/agent-1/suggested-questions?knowledge_base_ids=kb-1,kb-2&folder_ids=folder-a",
		nil,
	)
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
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
