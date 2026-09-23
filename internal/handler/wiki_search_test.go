package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeWikiSearchService struct {
	interfaces.WikiPageService
	pages []*types.WikiPage
	err   error
	got   struct {
		kbIDs []string
		query string
		limit int
	}
}

func (f *fakeWikiSearchService) SearchPagesAcross(
	_ context.Context, kbIDs []string, query string, limit int,
) ([]*types.WikiPage, error) {
	f.got.kbIDs = append([]string(nil), kbIDs...)
	f.got.query = query
	f.got.limit = limit
	if f.err != nil {
		return nil, f.err
	}
	return f.pages, nil
}

func wikiSearchEngine(t *testing.T, h *WikiPageHandler, scope *types.TenantAPIKeyScope) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.POST("/wiki-search", func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(1))
		if scope != nil {
			ctx = types.WithTenantAPIKeyScope(ctx, *scope)
		}
		c.Request = c.Request.WithContext(ctx)
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		h.SearchPagesAcross(c)
	})
	return r
}

func postWikiSearch(r *gin.Engine, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/wiki-search", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSearchPagesAcross_ReturnsSuccessData(t *testing.T) {
	fake := &fakeWikiSearchService{pages: []*types.WikiPage{
		{ID: "p1", KnowledgeBaseID: "kb-a", Title: "部署"},
	}}
	h := &WikiPageHandler{wikiService: fake}
	w := postWikiSearch(wikiSearchEngine(t, h, nil), `{"query":"部署","knowledge_base_ids":["kb-a","kb-b"]}`)
	require.Equal(t, http.StatusOK, w.Code)

	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, true, got["success"])
	require.NotNil(t, got["data"])
	assert.Equal(t, []string{"kb-a", "kb-b"}, fake.got.kbIDs)
	assert.Equal(t, "部署", fake.got.query)
}

func TestSearchPagesAcross_ReturnsSlimHitsWithoutContent(t *testing.T) {
	longBody := strings.Repeat("前", 80) + "部署手册正文" + strings.Repeat("后", 80)
	fake := &fakeWikiSearchService{pages: []*types.WikiPage{
		{
			ID:              "p1",
			TenantID:        99,
			KnowledgeBaseID: "kb-a",
			Slug:            "ops/deploy",
			Title:           "部署",
			PageType:        "entity",
			Status:          "published",
			Content:         longBody,
			Summary:         "发布步骤",
			Aliases:         types.StringArray{"发布"},
			Version:         3,
			InLinks:         types.StringArray{"index"},
			OutLinks:        types.StringArray{"ops/rollback"},
			WikiPath:        "/ops/deploy",
			FolderID:        "folder-1",
			SourceRefs:      types.StringArray{"doc-1|手册"},
		},
	}}
	h := &WikiPageHandler{wikiService: fake}
	w := postWikiSearch(wikiSearchEngine(t, h, nil), `{"query":"部署","knowledge_base_ids":["kb-a"]}`)
	require.Equal(t, http.StatusOK, w.Code)

	var got struct {
		Success bool             `json:"success"`
		Data    []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.True(t, got.Success)
	require.Len(t, got.Data, 1)

	hit := got.Data[0]
	assert.Equal(t, "p1", hit["id"])
	assert.Equal(t, "kb-a", hit["knowledge_base_id"])
	assert.Equal(t, "ops/deploy", hit["slug"])
	assert.Equal(t, "部署", hit["title"])
	assert.Equal(t, "entity", hit["page_type"])
	assert.Equal(t, "发布步骤", hit["summary"])
	assert.Equal(t, []any{"发布"}, hit["aliases"])

	snippet, ok := hit["match_snippet"].(string)
	require.True(t, ok)
	assert.Contains(t, snippet, "部署")
	assert.NotEqual(t, longBody, snippet)
	assert.Less(t, len([]rune(snippet)), 250)

	for _, dropped := range []string{
		"content", "tenant_id", "status", "version", "in_links", "out_links",
		"folder_id", "wiki_path", "source_refs", "chunk_refs", "page_metadata",
		"created_at", "updated_at",
	} {
		_, exists := hit[dropped]
		assert.False(t, exists, "dropped field %s should be absent", dropped)
	}
}

func TestSearchPagesAcross_MergesSingleKnowledgeBaseID(t *testing.T) {
	fake := &fakeWikiSearchService{pages: []*types.WikiPage{}}
	h := &WikiPageHandler{wikiService: fake}
	body := `{"query":"q","knowledge_base_id":"kb-1","knowledge_base_ids":["kb-2"]}`
	w := postWikiSearch(wikiSearchEngine(t, h, nil), body)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []string{"kb-2", "kb-1"}, fake.got.kbIDs)
}

func TestSearchPagesAcross_RequiresQueryAndKBs(t *testing.T) {
	h := &WikiPageHandler{wikiService: &fakeWikiSearchService{}}
	r := wikiSearchEngine(t, h, nil)

	w := postWikiSearch(r, `{"knowledge_base_ids":["kb-a"]}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = postWikiSearch(r, `{"query":"q"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSearchPagesAcross_APIKeyAllowListForbidden(t *testing.T) {
	fake := &fakeWikiSearchService{}
	h := &WikiPageHandler{wikiService: fake}
	scope := &types.TenantAPIKeyScope{KnowledgeBaseIDs: types.StringArray{"kb-allowed"}}
	w := postWikiSearch(wikiSearchEngine(t, h, scope), `{"query":"q","knowledge_base_ids":["kb-other"]}`)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Nil(t, fake.got.kbIDs)
}

func TestSearchPagesAcross_PropagatesNotFound(t *testing.T) {
	fake := &fakeWikiSearchService{err: errors.NewNotFoundError("knowledge base not found")}
	h := &WikiPageHandler{wikiService: fake}
	w := postWikiSearch(wikiSearchEngine(t, h, nil), `{"query":"q","knowledge_base_ids":["kb-a"]}`)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestSearchPagesAcross_PropagatesNonWikiBadRequest(t *testing.T) {
	fake := &fakeWikiSearchService{
		err: errors.NewBadRequestError("Wiki feature is not enabled for this knowledge base"),
	}
	h := &WikiPageHandler{wikiService: fake}
	w := postWikiSearch(wikiSearchEngine(t, h, nil), `{"query":"q","knowledge_base_ids":["kb-a"]}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMergeWikiSearchKBIDsMerges(t *testing.T) {
	got := mergeWikiSearchKBIDs([]string{" kb-a ", "kb-b", "kb-a"}, "kb-b")
	assert.Equal(t, []string{"kb-a", "kb-b", "kb-a", "kb-b"}, got)
}
