package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type recordingWikiSearchService struct {
	interfaces.WikiPageService
	pages         []*types.WikiPage
	literalCalled bool
	regexCalled   bool
	gotQuery      string
	gotLimit      int
}

func (f *recordingWikiSearchService) SearchPages(
	_ context.Context, _ string, query string, limit int,
) ([]*types.WikiPage, error) {
	f.regexCalled = true
	f.gotQuery = query
	f.gotLimit = limit
	return f.pages, nil
}

func (f *recordingWikiSearchService) SearchPagesLiteral(
	_ context.Context, _ string, query string, limit int,
) ([]*types.WikiPage, error) {
	f.literalCalled = true
	f.gotQuery = query
	f.gotLimit = limit
	return f.pages, nil
}

type fakeWikiLiteralKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (f *fakeWikiLiteralKBService) GetKnowledgeBaseByID(
	context.Context, string,
) (*types.KnowledgeBase, error) {
	return f.kb, nil
}

func newWikiLiteralKB() *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID:               "kb-1",
		TenantID:         1,
		IndexingStrategy: types.IndexingStrategy{WikiEnabled: true},
	}
}

func wikiLiteralSearchEngine(h *WikiPageHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.GET("/knowledgebase/:kb_id/wiki/search", h.SearchPages)
	return r
}

// GET /knowledgebase/{kb_id}/wiki/search serves the plain-text search box:
// the query must reach the store literally (SearchPagesLiteral), never as a
// raw POSIX regex — PostgreSQL would 500 on everyday inputs like "func(".
func TestSearchPages_SearchBoxQueriesGoThroughLiteralSearch(t *testing.T) {
	fake := &recordingWikiSearchService{pages: []*types.WikiPage{
		{ID: "p1", KnowledgeBaseID: "kb-1", Title: "部署"},
	}}
	h := &WikiPageHandler{wikiService: fake, kbService: &fakeWikiLiteralKBService{kb: newWikiLiteralKB()}}
	r := wikiLiteralSearchEngine(h)

	req := httptest.NewRequest(http.MethodGet, "/knowledgebase/kb-1/wiki/search?q=func%28&limit=5", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, fake.literalCalled, "search-box queries must use the literal search path")
	assert.False(t, fake.regexCalled, "search-box queries must not be forwarded as raw regexes")
	assert.Equal(t, "func(", fake.gotQuery)
	assert.Equal(t, 5, fake.gotLimit)
}

// On SQLite the whole stack (handler → service → repo.SearchAcross) returns
// the seeded page for metacharacter queries; this pins the same end-to-end
// behavior the fixed PostgreSQL path must provide.
func TestSearchPages_MatchesMetacharQueriesLiterally(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.WikiFolder{}, &types.WikiPage{}, &types.WikiPageRevision{}))
	repo := repository.NewWikiPageRepository(db)
	kb := newWikiLiteralKB()
	svc := service.NewWikiPageService(repo, nil, &fakeWikiLiteralKBService{kb: kb}, nil, nil, nil)
	h := &WikiPageHandler{wikiService: svc, kbService: &fakeWikiLiteralKBService{kb: kb}}
	r := wikiLiteralSearchEngine(h)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	require.NoError(t, repo.Create(ctx, &types.WikiPage{
		ID: "p-deploy", TenantID: 1, KnowledgeBaseID: "kb-1",
		Slug: "entity/deploy", Title: "部署手册", PageType: types.WikiPageTypeEntity,
		Status:  types.WikiPageStatusPublished,
		Content: "调用 func( 读取 arr[0]，C++ 客户端备份在 dir\\backup",
		Version: 1,
	}))

	cases := []struct {
		query    string
		wantHits int
	}{
		{query: "func(", wantHits: 1},
		{query: "arr[0", wantHits: 1},
		{query: "C++", wantHits: 1},
		{query: `dir\`, wantHits: 1},
		{query: "部署", wantHits: 1},
		{query: "no-such-token-xyz", wantHits: 0},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet,
				"/knowledgebase/kb-1/wiki/search?q="+url.QueryEscape(tc.query), nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code)

			var got struct {
				Pages []*types.WikiPage `json:"pages"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
			require.Len(t, got.Pages, tc.wantHits, "query %q should match literally", tc.query)
			if tc.wantHits > 0 {
				assert.Equal(t, "p-deploy", got.Pages[0].ID)
			}
		})
	}
}
