package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type wikiPageHandlerKBStub struct {
	interfaces.KnowledgeBaseService
}

func (s *wikiPageHandlerKBStub) GetKnowledgeBaseByID(
	_ context.Context, id string,
) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{
		ID: id,
		IndexingStrategy: types.IndexingStrategy{
			WikiEnabled: true,
		},
	}, nil
}

type wikiPageOnlyServiceStub struct {
	interfaces.WikiPageService
	page     *types.WikiPage
	err      error
	getCalls int
}

func (s *wikiPageOnlyServiceStub) GetPageBySlug(
	_ context.Context, _, _ string,
) (*types.WikiPage, error) {
	s.getCalls++
	return s.page, s.err
}

type wikiPageProvenanceServiceStub struct {
	interfaces.WikiPageService
	interfaces.WikiProvenanceService
	page        *types.WikiPage
	detail      *types.WikiPageDetailResponse
	detailErr   error
	getCalls    int
	detailCalls int
}

func (s *wikiPageProvenanceServiceStub) GetPageBySlug(
	_ context.Context, _, _ string,
) (*types.WikiPage, error) {
	s.getCalls++
	return s.page, nil
}

func (s *wikiPageProvenanceServiceStub) GetPageWithSources(
	_ context.Context, _, _ string,
) (*types.WikiPageDetailResponse, error) {
	s.detailCalls++
	return s.detail, s.detailErr
}

func newWikiPageHandlerTestEngine(service interfaces.WikiPageService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := &WikiPageHandler{
		wikiService: service,
		kbService:   &wikiPageHandlerKBStub{},
	}
	r := gin.New()
	r.GET("/knowledgebase/:kb_id/wiki/pages/*slug", h.GetPage)
	return r
}

func performWikiPageGet(t *testing.T, engine *gin.Engine, path string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	engine.ServeHTTP(recorder, req)
	body := map[string]any{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	return recorder, body
}

func TestGetWikiPageDefaultResponseDoesNotLoadSources(t *testing.T) {
	service := &wikiPageOnlyServiceStub{page: &types.WikiPage{
		ID: "page-1", KnowledgeBaseID: "kb-1", Slug: "concept/rag", Title: "RAG",
	}}
	recorder, body := performWikiPageGet(
		t, newWikiPageHandlerTestEngine(service),
		"/knowledgebase/kb-1/wiki/pages/concept/rag",
	)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, service.getCalls)
	_, hasBlocks := body["blocks"]
	require.False(t, hasBlocks, "legacy response shape must not gain a blocks field")
}

func TestGetWikiPageIncludeSourcesUsesOptionalExtension(t *testing.T) {
	page := &types.WikiPage{ID: "page-1", KnowledgeBaseID: "kb-1", Slug: "concept/rag", Title: "RAG"}
	service := &wikiPageProvenanceServiceStub{
		page: page,
		detail: &types.WikiPageDetailResponse{
			WikiPage: page,
			Blocks: []*types.WikiPageBlock{{
				ID: "block-1", BlockType: types.WikiBlockTypeParagraph, SortOrder: 1,
				Content: "A grounded paragraph.",
				Sources: []*types.WikiBlockSource{{
					ID: "source-1", KnowledgeID: "knowledge-1", ChunkID: "chunk-1",
					Evidence: "grounded", ValidationStatus: types.WikiSourceValidationLocated,
				}},
			}},
		},
	}
	recorder, body := performWikiPageGet(
		t, newWikiPageHandlerTestEngine(service),
		"/knowledgebase/kb-1/wiki/pages/concept/rag?include_sources=true",
	)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, service.detailCalls)
	require.Zero(t, service.getCalls, "handler must not perform a second page read")
	blocks, ok := body["blocks"].([]any)
	require.True(t, ok)
	require.Len(t, blocks, 1)
}

func TestGetWikiPageIncludeSourcesFalseKeepsLegacyPath(t *testing.T) {
	page := &types.WikiPage{ID: "page-1", KnowledgeBaseID: "kb-1", Slug: "concept/rag"}
	service := &wikiPageProvenanceServiceStub{page: page}
	recorder, body := performWikiPageGet(
		t, newWikiPageHandlerTestEngine(service),
		"/knowledgebase/kb-1/wiki/pages/concept/rag?include_sources=false",
	)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, service.getCalls)
	require.Zero(t, service.detailCalls)
	_, hasBlocks := body["blocks"]
	require.False(t, hasBlocks)
}

func TestGetWikiPageRejectsInvalidIncludeSources(t *testing.T) {
	service := &wikiPageOnlyServiceStub{}
	recorder, body := performWikiPageGet(
		t, newWikiPageHandlerTestEngine(service),
		"/knowledgebase/kb-1/wiki/pages/concept/rag?include_sources=maybe",
	)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, body["error"], "boolean")
	require.Zero(t, service.getCalls)
}

func TestGetWikiPageIncludeSourcesRequiresProvenanceExtension(t *testing.T) {
	service := &wikiPageOnlyServiceStub{}
	recorder, _ := performWikiPageGet(
		t, newWikiPageHandlerTestEngine(service),
		"/knowledgebase/kb-1/wiki/pages/concept/rag?include_sources=true",
	)

	require.Equal(t, http.StatusNotImplemented, recorder.Code)
	require.Zero(t, service.getCalls)
}

func TestGetWikiPageIncludeSourcesPreservesNotFound(t *testing.T) {
	service := &wikiPageProvenanceServiceStub{detailErr: repository.ErrWikiPageNotFound}
	recorder, _ := performWikiPageGet(
		t, newWikiPageHandlerTestEngine(service),
		"/knowledgebase/kb-1/wiki/pages/concept/missing?include_sources=true",
	)

	require.Equal(t, http.StatusNotFound, recorder.Code)
}

func TestGetWikiPageIncludeSourcesReturnsInternalError(t *testing.T) {
	service := &wikiPageProvenanceServiceStub{detailErr: errors.New("source read failed")}
	recorder, _ := performWikiPageGet(
		t, newWikiPageHandlerTestEngine(service),
		"/knowledgebase/kb-1/wiki/pages/concept/rag?include_sources=true",
	)

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
}
