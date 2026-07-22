package handler

import (
	"context"
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
	require.Contains(t, response.Body.String(), `"id":"chunk-1"`)
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
