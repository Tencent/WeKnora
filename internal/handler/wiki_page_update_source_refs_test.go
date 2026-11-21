package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeWikiPageUpdateService struct {
	interfaces.WikiPageService
	stored  *types.WikiPage
	updated *types.WikiPage
}

func (f *fakeWikiPageUpdateService) GetPageBySlug(context.Context, string, string) (*types.WikiPage, error) {
	page := *f.stored
	return &page, nil
}

func (f *fakeWikiPageUpdateService) UpdatePage(_ context.Context, page *types.WikiPage) (*types.WikiPage, error) {
	f.updated = page
	return page, nil
}

func putWikiPage(f *fakeWikiPageUpdateService, body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	h := &WikiPageHandler{wikiService: f, kbService: fakeWikiPageTitlesKBService{}}
	r := gin.New()
	r.PUT("/knowledgebase/:kb_id/wiki/pages/*slug", h.UpdatePage)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/knowledgebase/kb-1/wiki/pages/concept/a", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	return rec
}

func TestUpdatePageAppliesSourceRefs(t *testing.T) {
	f := &fakeWikiPageUpdateService{stored: &types.WikiPage{
		Slug: "concept/a", Title: "A", Content: "body", Version: 3,
		SourceRefs: types.StringArray{"old-doc"},
	}}
	rec := putWikiPage(f, `{"source_refs":["doc-1","doc-2"]}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, f.updated)
	assert.Equal(t, types.StringArray{"doc-1", "doc-2"}, f.updated.SourceRefs)
	assert.Equal(t, "body", f.updated.Content)
}

func TestUpdatePageKeepsSourceRefsWhenAbsent(t *testing.T) {
	f := &fakeWikiPageUpdateService{stored: &types.WikiPage{
		Slug: "concept/a", Title: "A", Content: "body", Version: 3,
		SourceRefs: types.StringArray{"old-doc"},
	}}
	rec := putWikiPage(f, `{"content":"new"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, types.StringArray{"old-doc"}, f.updated.SourceRefs)
}

func TestUpdatePageClearsSourceRefsWithEmptyArray(t *testing.T) {
	f := &fakeWikiPageUpdateService{stored: &types.WikiPage{
		Slug: "concept/a", Title: "A", Version: 3, SourceRefs: types.StringArray{"old-doc"},
	}}
	rec := putWikiPage(f, `{"source_refs":[]}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, f.updated.SourceRefs)
}
