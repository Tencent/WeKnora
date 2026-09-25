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
	calls    int
	lastOpts *types.KnowledgeSearchOptions
}

func (s *stubSearchSessionService) SearchKnowledge(
	_ context.Context, _ []string, _ []string, _ []types.TagScope, _ string, opts *types.KnowledgeSearchOptions,
) (*types.RetrievalResult, error) {
	s.calls++
	s.lastOpts = opts
	return &types.RetrievalResult{
		Results: []*types.SearchResult{{
			Content:   "chunk ![c](" + testResourceHandle + ")",
			ImageInfo: `[{"url":"` + testResourceHandle + `"}]`,
		}},
		Meta: types.RetrievalMeta{Rerank: &types.RerankDiagnostics{
			Applied: true, Outcome: types.RerankOutcomeOK, ModelID: "rr-1", ModelSource: types.RerankModelSourceTenant,
		}},
	}, nil
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

func performKnowledgeSearch(t *testing.T, svc *stubSearchSessionService, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	h := &Handler{sessionService: svc, fileService: &stubResourceFileService{}}
	r.POST("/knowledge-search", h.SearchKnowledge)
	req := httptest.NewRequest(http.MethodPost, "/knowledge-search", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSearchKnowledge_PassesRetrievalOverridesAndReturnsMeta(t *testing.T) {
	svc := &stubSearchSessionService{}
	w := performKnowledgeSearch(t, svc, `{
		"query":"q","knowledge_base_ids":["kb-1"],
		"vector_threshold":0.4,"keyword_threshold":0,"match_count":7,"disable_keywords_match":true,
		"rerank":{"enabled":false}
	}`)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	opts := svc.lastOpts
	require.NotNil(t, opts)
	require.NotNil(t, opts.VectorThreshold)
	require.NotNil(t, opts.KeywordThreshold, "an explicit 0 must survive as an override")
	assert.Equal(t, 0.4, *opts.VectorThreshold)
	assert.Equal(t, 0.0, *opts.KeywordThreshold)
	assert.Equal(t, 7, opts.MatchCount)
	assert.True(t, opts.DisableKeywordsMatch)
	require.NotNil(t, opts.Rerank)
	assert.False(t, opts.Rerank.IsEnabled())

	var body struct {
		Meta types.RetrievalMeta `json:"meta"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.NotNil(t, body.Meta.Rerank)
	assert.Equal(t, types.RerankOutcomeOK, body.Meta.Rerank.Outcome)
	assert.Equal(t, types.RerankModelSourceTenant, body.Meta.Rerank.ModelSource)
}

func TestSearchKnowledge_OmittedOverridesKeepTenantConfig(t *testing.T) {
	svc := &stubSearchSessionService{}
	w := performKnowledgeSearch(t, svc, `{"query":"q","knowledge_base_ids":["kb-1"]}`)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	require.NotNil(t, svc.lastOpts)
	assert.Equal(t, types.KnowledgeSearchOptions{}, *svc.lastOpts)
}

func TestSearchKnowledge_RejectsInvalidOverrides(t *testing.T) {
	for name, extra := range map[string]string{
		"negative match_count":  `"match_count":-1`,
		"both recall paths off": `"disable_keywords_match":true,"disable_vector_match":true`,
		"negative rerank top_k": `"rerank":{"top_k":-2}`,
	} {
		t.Run(name, func(t *testing.T) {
			svc := &stubSearchSessionService{}
			w := performKnowledgeSearch(t, svc, `{"query":"q","knowledge_base_ids":["kb-1"],`+extra+`}`)
			require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
			assert.Zero(t, svc.calls)
		})
	}
}
