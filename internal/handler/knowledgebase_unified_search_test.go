package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type unifiedSearchHandlerStub struct {
	interfaces.UnifiedSearchService
	search func(context.Context, string, types.UnifiedSearchRequest) ([]*types.UnifiedSearchResult, error)
}

func (s *unifiedSearchHandlerStub) Search(
	ctx context.Context,
	kbID string,
	req types.UnifiedSearchRequest,
) ([]*types.UnifiedSearchResult, error) {
	return s.search(ctx, kbID, req)
}

func newUnifiedSearchHandlerRouter(
	kbService interfaces.KnowledgeBaseService,
	unifiedSearch interfaces.UnifiedSearchService,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	router.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Set(types.UserIDContextKey.String(), "u-test")
		c.Next()
	})
	handler := &KnowledgeBaseHandler{service: kbService, unifiedSearch: unifiedSearch}
	router.POST("/knowledge-bases/:id/unified-search", handler.UnifiedSearch)
	return router
}

func TestUnifiedSearchHandlerReturnsUnifiedResults(t *testing.T) {
	kbService := &stubKBOnlyService{
		getByID: func(context.Context, string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{ID: "kb-1", TenantID: 1}, nil
		},
	}
	unifiedSearch := &unifiedSearchHandlerStub{
		search: func(_ context.Context, kbID string, req types.UnifiedSearchRequest) ([]*types.UnifiedSearchResult, error) {
			require.Equal(t, "kb-1", kbID)
			require.Equal(t, "refund", req.Query)
			return []*types.UnifiedSearchResult{{ID: "chunk-1", Content: "result"}}, nil
		},
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-bases/kb-1/unified-search",
		strings.NewReader(`{"query":"refund"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	newUnifiedSearchHandlerRouter(kbService, unifiedSearch).ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body types.UnifiedSearchResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.Len(t, body.Data, 1)
	require.Equal(t, "chunk-1", body.Data[0].ID)
}

func TestUnifiedSearchHandlerRejectsInvalidJSON(t *testing.T) {
	kbService := &stubKBOnlyService{
		getByID: func(context.Context, string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{ID: "kb-1", TenantID: 1}, nil
		},
	}
	unifiedSearch := &unifiedSearchHandlerStub{
		search: func(context.Context, string, types.UnifiedSearchRequest) ([]*types.UnifiedSearchResult, error) {
			t.Fatal("unified search service must not be called for invalid JSON")
			return nil, nil
		},
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-bases/kb-1/unified-search",
		strings.NewReader(`{"query":`),
	)
	request.Header.Set("Content-Type", "application/json")
	newUnifiedSearchHandlerRouter(kbService, unifiedSearch).ServeHTTP(response, request)

	require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
}

func TestUnifiedSearchHandlerRejectsInvalidContractValues(t *testing.T) {
	tests := map[string]string{
		"missing query":   `{"top_k":10}`,
		"top k too large": `{"query":"refund","top_k":51}`,
		"negative weight": `{"query":"refund","rag_weight":-1}`,
		"rrf k too large": `{"query":"refund","rrf_k":1001}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			kbService := &stubKBOnlyService{
				getByID: func(context.Context, string) (*types.KnowledgeBase, error) {
					return &types.KnowledgeBase{ID: "kb-1", TenantID: 1}, nil
				},
			}
			unifiedSearch := &unifiedSearchHandlerStub{
				search: func(context.Context, string, types.UnifiedSearchRequest) ([]*types.UnifiedSearchResult, error) {
					t.Fatal("unified search service must not be called for invalid contract values")
					return nil, nil
				},
			}

			response := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/knowledge-bases/kb-1/unified-search",
				strings.NewReader(body),
			)
			request.Header.Set("Content-Type", "application/json")
			newUnifiedSearchHandlerRouter(kbService, unifiedSearch).ServeHTTP(response, request)

			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		})
	}
}
