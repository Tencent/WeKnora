package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type stubSearchSessionService struct {
	interfaces.SessionService
	err error
}

func (s *stubSearchSessionService) SearchKnowledge(
	_ context.Context, _ []string, _ []string, _ []types.TagScope, _ string, _ string,
) ([]*types.SearchResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []*types.SearchResult{{
		Content:   "chunk ![c](" + testResourceHandle + ")",
		ImageInfo: `[{"url":"` + testResourceHandle + `"}]`,
	}}, nil
}

func TestSearchKnowledge_PublicResourceURLs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	h := &Handler{
		sessionService: &stubSearchSessionService{},
		fileService:    &stubResourceFileService{},
	}
	r.POST("/knowledge-search", h.SearchKnowledge)

	body := bytes.NewBufferString(`{"query":"diagram","knowledge_base_ids":["kb-1"]}`)
	req := httptest.NewRequest(http.MethodPost, "/knowledge-search?resource_urls=public", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.NotContains(t, w.Body.String(), testResourceHandle)
	assert.Contains(t, w.Body.String(), "cdn.example.com")
}

func TestSearchKnowledge_InvalidResourceURLMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	h := &Handler{
		sessionService: &stubSearchSessionService{},
		fileService:    &stubResourceFileService{},
	}
	r.POST("/knowledge-search", h.SearchKnowledge)

	body := bytes.NewBufferString(`{"query":"diagram","knowledge_base_ids":["kb-1"]}`)
	req := httptest.NewRequest(http.MethodPost, "/knowledge-search?resource_urls=signed", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "resource_urls")
}

func TestSearchKnowledge_DefaultKeepsHandles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	h := &Handler{
		sessionService: &stubSearchSessionService{},
		fileService:    &stubResourceFileService{},
	}
	r.POST("/knowledge-search", h.SearchKnowledge)

	body := bytes.NewBufferString(`{"query":"diagram","knowledge_base_ids":["kb-1"]}`)
	req := httptest.NewRequest(http.MethodPost, "/knowledge-search", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var resp struct {
		Data []struct {
			Content string `json:"content"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 1)
	assert.Contains(t, resp.Data[0].Content, testResourceHandle)
}

func setupSearchKnowledgeRouter(err error) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	h := &Handler{
		sessionService: &stubSearchSessionService{err: err},
		fileService:    &stubResourceFileService{},
	}
	r.POST("/knowledge-search", h.SearchKnowledge)

	body := bytes.NewBufferString(`{"query":"diagram","knowledge_base_ids":["kb-1"]}`)
	req := httptest.NewRequest(http.MethodPost, "/knowledge-search", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSearchKnowledge_TypedForbiddenErrorPreserved(t *testing.T) {
	w := setupSearchKnowledgeRouter(
		apperrors.NewForbiddenError("rerank model not found or not accessible"))

	require.Equal(t, http.StatusForbidden, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "rerank model not found or not accessible")
}

func TestSearchKnowledge_TypedBadRequestErrorPreserved(t *testing.T) {
	w := setupSearchKnowledgeRouter(
		apperrors.NewBadRequestError("rerank_model_id matches multiple models"))

	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "rerank_model_id matches multiple models")
}

func TestSearchKnowledge_GenericErrorFlattenedTo500(t *testing.T) {
	w := setupSearchKnowledgeRouter(errors.New("db down"))

	require.Equal(t, http.StatusInternalServerError, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), `"code":1007`)
}
