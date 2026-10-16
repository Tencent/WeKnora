package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type hybridSearchTestService struct {
	interfaces.KnowledgeBaseService
	searchCalls  int
	rerankCalls  int
	searchParams types.SearchParams
	results      []*types.SearchResult
}

func (s *hybridSearchTestService) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{ID: id, TenantID: 1}, nil
}

func (s *hybridSearchTestService) HybridSearch(
	_ context.Context,
	_ string,
	params types.SearchParams,
) ([]*types.SearchResult, error) {
	s.searchCalls++
	s.searchParams = params
	if s.results != nil {
		return s.results, nil
	}
	return []*types.SearchResult{}, nil
}

func (s *hybridSearchTestService) HybridSearchWithRerank(
	_ context.Context,
	_ string,
	params types.SearchParams,
) (*types.RetrievalResult, error) {
	s.rerankCalls++
	s.searchParams = params
	return &types.RetrievalResult{
		Results: []*types.SearchResult{{ID: "c1", Score: 0.8}},
		Meta: types.RetrievalMeta{Rerank: &types.RerankDiagnostics{
			Applied: true, Outcome: types.RerankOutcomeOK, ModelID: "rr-1",
		}},
	}, nil
}

func newHybridSearchTestRouter(svc interfaces.KnowledgeBaseService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	router.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Set(types.UserIDContextKey.String(), "u-test")
		c.Next()
	})
	handler := &KnowledgeBaseHandler{service: svc}
	router.POST("/knowledge-bases/:id/hybrid-search", handler.HybridSearch)
	return router
}

func TestHybridSearchRejectsMissingQueryText(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "missing field", body: `{}`},
		{name: "empty field", body: `{"query_text":""}`},
		{name: "whitespace field", body: `{"query_text":"   "}`},
		{name: "wrong field name", body: `{"query":"MiniMax"}`},
		{name: "embedding with keyword matching", body: `{"query_embedding":[0.1]}`},
		{
			name: "embedding with all matching disabled",
			body: `{"query_embedding":[0.1],"disable_keywords_match":true,"disable_vector_match":true}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &hybridSearchTestService{}
			response := performHybridSearchRequest(svc, tt.body)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d body=%s", response.Code, response.Body.String())
			}
			if svc.searchCalls != 0 {
				t.Fatalf("invalid request reached HybridSearch %d time(s)", svc.searchCalls)
			}
			if !strings.Contains(response.Body.String(), `"code":1000`) {
				t.Fatalf("expected bad-request envelope, got %s", response.Body.String())
			}
		})
	}
}

func TestHybridSearchRejectsInvalidRerank(t *testing.T) {
	for name, body := range map[string]string{
		"negative top_k": `{"query_text":"q","rerank":{"top_k":-1}}`,
		"rerank without query_text": `{"query_embedding":[0.1],"disable_keywords_match":true,` +
			`"rerank":{"model_id":"rr-1"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			svc := &hybridSearchTestService{}
			response := performHybridSearchRequest(svc, body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d body=%s", response.Code, response.Body.String())
			}
			if svc.searchCalls+svc.rerankCalls != 0 {
				t.Fatal("invalid request reached the service")
			}
		})
	}
}

func TestHybridSearchWithoutRerankKeepsResponseShape(t *testing.T) {
	svc := &hybridSearchTestService{}
	response := performHybridSearchRequest(svc, `{"query_text":"q"}`)
	if response.Code != http.StatusOK || svc.searchCalls != 1 || svc.rerankCalls != 0 {
		t.Fatalf("code=%d search=%d rerank=%d", response.Code, svc.searchCalls, svc.rerankCalls)
	}
	if strings.Contains(response.Body.String(), `"meta"`) {
		t.Fatalf("plain hybrid search must not grow a meta field: %s", response.Body.String())
	}
}

func TestHybridSearchWithRerankReturnsMeta(t *testing.T) {
	svc := &hybridSearchTestService{}
	response := performHybridSearchRequest(svc,
		`{"query_text":"q","match_count":5,"rerank":{"model_id":"rr-1","top_k":3,"threshold":0}}`)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", response.Code, response.Body.String())
	}
	if svc.rerankCalls != 1 || svc.searchCalls != 0 {
		t.Fatalf("search=%d rerank=%d", svc.searchCalls, svc.rerankCalls)
	}
	rr := svc.searchParams.Rerank
	if rr == nil || rr.ModelID != "rr-1" || rr.TopK != 3 || rr.Threshold == nil || *rr.Threshold != 0 {
		t.Fatalf("rerank options not passed through: %+v", rr)
	}
	body := response.Body.String()
	if !strings.Contains(body, `"meta":{"rerank":{"applied":true,"outcome":"ok","model_id":"rr-1"`) {
		t.Fatalf("expected rerank meta, got %s", body)
	}
}

func TestHybridSearchAcceptsQueryText(t *testing.T) {
	svc := &hybridSearchTestService{}
	response := performHybridSearchRequest(svc, `{"query_text":"MiniMax","match_count":3}`)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", response.Code, response.Body.String())
	}
	if svc.searchCalls != 1 {
		t.Fatalf("expected one HybridSearch call, got %d", svc.searchCalls)
	}
	if svc.searchParams.QueryText != "MiniMax" {
		t.Fatalf("query text = %q, want MiniMax", svc.searchParams.QueryText)
	}
	if svc.searchParams.MatchCount != 3 {
		t.Fatalf("match count = %d, want 3", svc.searchParams.MatchCount)
	}
}

func TestHybridSearchAcceptsPrecomputedVectorWithoutQueryText(t *testing.T) {
	svc := &hybridSearchTestService{}
	response := performHybridSearchRequest(
		svc,
		`{"query_embedding":[0.1,0.2],"disable_keywords_match":true}`,
	)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", response.Code, response.Body.String())
	}
	if svc.searchCalls != 1 {
		t.Fatalf("expected one HybridSearch call, got %d", svc.searchCalls)
	}
	if len(svc.searchParams.QueryEmbedding) != 2 {
		t.Fatalf("query embedding length = %d, want 2", len(svc.searchParams.QueryEmbedding))
	}
	if !svc.searchParams.DisableKeywordsMatch || svc.searchParams.DisableVectorMatch {
		t.Fatalf("expected vector-only params, got %+v", svc.searchParams)
	}
}

func performHybridSearchRequest(svc interfaces.KnowledgeBaseService, body string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-bases/kb-1/hybrid-search",
		strings.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	newHybridSearchTestRouter(svc).ServeHTTP(response, request)
	return response
}
