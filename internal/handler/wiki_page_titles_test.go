package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeWikiPageTitlesService struct {
	interfaces.WikiPageService
	pages map[string]*types.WikiPageLite
	got   []string
}

func (f *fakeWikiPageTitlesService) ListBySlugs(
	_ context.Context, _ string, slugs []string,
) (map[string]*types.WikiPageLite, error) {
	f.got = append([]string(nil), slugs...)
	return f.pages, nil
}

type fakeWikiPageTitlesKBService struct {
	interfaces.KnowledgeBaseService
}

func (fakeWikiPageTitlesKBService) GetKnowledgeBaseByID(
	context.Context, string,
) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{
		Type:             types.KnowledgeBaseTypeWiki,
		IndexingStrategy: types.IndexingStrategy{WikiEnabled: true},
	}, nil
}

func wikiPageTitlesEngine(h *WikiPageHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/knowledgebase/:kb_id/wiki/page-titles", h.ListPageTitles)
	return r
}

func TestListPageTitlesResolvesRequestedSlugs(t *testing.T) {
	fake := &fakeWikiPageTitlesService{
		pages: map[string]*types.WikiPageLite{
			"concept/a": {Slug: "concept/a", Title: "概念 A"},
			"entity/b":  {Slug: "entity/b", Title: "实体 B"},
		},
	}
	h := &WikiPageHandler{
		wikiService: fake,
		kbService:   fakeWikiPageTitlesKBService{},
	}

	req := httptest.NewRequest(
		http.MethodGet,
		"/knowledgebase/kb-1/wiki/page-titles?slug=concept%2Fa&slug=concept%2Fa&slug=%20&slug=entity%2Fb",
		nil,
	)
	rec := httptest.NewRecorder()
	wikiPageTitlesEngine(h).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"concept/a", "entity/b"}, fake.got)
	var body struct {
		Titles map[string]string `json:"titles"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, map[string]string{
		"concept/a": "概念 A",
		"entity/b":  "实体 B",
	}, body.Titles)
}
