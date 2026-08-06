package session

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type stubSearchSessionService struct {
	interfaces.SessionService
}

func (s *stubSearchSessionService) SearchKnowledge(
	_ context.Context, _ []string, _ []string, _ []types.TagScope, _ string,
) ([]*types.SearchResult, error) {
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
