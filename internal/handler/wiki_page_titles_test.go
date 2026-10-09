package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type fakeWikiPageTitlesService struct {
	interfaces.WikiPageService
	page      *types.WikiPage
	pages     map[string]*types.WikiPageLite
	got       []string
	getErr    error
	lookupErr error
	failAfter int
	lookupKBs []string
	getKB     string
	getSlug   string
}

func (f *fakeWikiPageTitlesService) GetPageBySlug(_ context.Context, kbID, slug string) (*types.WikiPage, error) {
	f.getKB, f.getSlug = kbID, slug
	return f.page, f.getErr
}

func (f *fakeWikiPageTitlesService) ListBySlugs(
	_ context.Context, kbID string, slugs []string,
) (map[string]*types.WikiPageLite, error) {
	f.lookupKBs = append(f.lookupKBs, kbID)
	if f.lookupErr != nil && len(f.got) >= f.failAfter {
		return nil, f.lookupErr
	}
	f.got = append(f.got, slugs...)
	out := make(map[string]*types.WikiPageLite)
	for _, slug := range slugs {
		if page, ok := f.pages[slug]; ok {
			out[slug] = page
		}
	}
	return out, nil
}

type fakeWikiPageTitlesKBService struct {
	interfaces.KnowledgeBaseService
	err      error
	disabled bool
}

func (f fakeWikiPageTitlesKBService) GetKnowledgeBaseByID(
	context.Context, string,
) (*types.KnowledgeBase, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.disabled {
		return &types.KnowledgeBase{}, nil
	}
	return &types.KnowledgeBase{
		Type:             types.KnowledgeBaseTypeWiki,
		IndexingStrategy: types.IndexingStrategy{WikiEnabled: true},
	}, nil
}

func wikiPageTitlesEngine(h *WikiPageHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/knowledgebase/:kb_id/wiki/pages/*slug", h.GetPage)
	return r
}

func TestGetPageResolvesBacklinkTitles(t *testing.T) {
	fake := &fakeWikiPageTitlesService{
		page: &types.WikiPage{
			ID: "page-id", KnowledgeBaseID: "kb-1", Slug: "concept/current",
			Title: "当前页面", Content: "正文", Version: 7,
			InLinks: types.StringArray{
				"concept/a", "concept/a", "entity/b", "missing", "nil-page", "constructor", "__proto__",
			},
		},
		pages: map[string]*types.WikiPageLite{
			"concept/a":   {Slug: "concept/a", Title: "概念 A"},
			"entity/b":    {Slug: "entity/b", Title: "实体 B"},
			"nil-page":    nil,
			"constructor": {Slug: "constructor", Title: "构造函数"},
			"__proto__":   {Slug: "__proto__", Title: "原型"},
		},
	}
	rec := requestWikiPageTitles(fake, fakeWikiPageTitlesKBService{}, "concept/current")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "kb-1", fake.getKB)
	assert.Equal(t, "concept/current", fake.getSlug)
	for _, kbID := range fake.lookupKBs {
		assert.Equal(t, "kb-1", kbID)
	}
	var body types.WikiPageDetail
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	expectedPage, err := json.Marshal(fake.page)
	require.NoError(t, err)
	actualPage, err := json.Marshal(body.WikiPage)
	require.NoError(t, err)
	assert.JSONEq(t, string(expectedPage), string(actualPage))
	assert.Equal(t, map[string]string{
		"concept/a":   "概念 A",
		"entity/b":    "实体 B",
		"constructor": "构造函数",
		"__proto__":   "原型",
	}, body.InLinkTitles)
}

func requestWikiPageTitles(
	fake *fakeWikiPageTitlesService, kb fakeWikiPageTitlesKBService, slug string,
) *httptest.ResponseRecorder {
	h := &WikiPageHandler{wikiService: fake, kbService: kb}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/knowledgebase/kb-1/wiki/pages/"+slug, nil)
	wikiPageTitlesEngine(h).ServeHTTP(rec, req)
	return rec
}

func TestGetPageResolvesAllLargeAndLongBacklinkSets(t *testing.T) {
	// Cross several lookup boundaries so earlier results cannot be silently lost.
	for _, count := range []int{999, 1000, 1001, 2507} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			fake := &fakeWikiPageTitlesService{
				page: &types.WikiPage{Slug: "current"}, pages: map[string]*types.WikiPageLite{},
			}
			for i := 0; i < count; i++ {
				slug := fmt.Sprintf("concept/%d-%s", i, strings.Repeat("a", 200))
				fake.page.InLinks = append(fake.page.InLinks, slug)
				fake.pages[slug] = &types.WikiPageLite{Slug: slug, Title: fmt.Sprintf("标题 %d", i)}
			}
			rec := requestWikiPageTitles(fake, fakeWikiPageTitlesKBService{}, "current")
			require.Equal(t, http.StatusOK, rec.Code)
			var body types.WikiPageDetail
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			require.Len(t, body.InLinkTitles, count)
			assert.ElementsMatch(t, []string(fake.page.InLinks), fake.got)
			for slug, page := range fake.pages {
				assert.Equal(t, page.Title, body.InLinkTitles[slug])
			}
		})
	}
}

func TestGetPageReturnsEmptyTitleObject(t *testing.T) {
	for _, links := range []types.StringArray{nil, {}, {"missing"}} {
		fake := &fakeWikiPageTitlesService{page: &types.WikiPage{Slug: "current", InLinks: links}}
		rec := requestWikiPageTitles(fake, fakeWikiPageTitlesKBService{}, "current")
		require.Equal(t, http.StatusOK, rec.Code)
		var body map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.JSONEq(t, `{}`, string(body["in_link_titles"]))
		if len(links) == 0 {
			assert.Empty(t, fake.lookupKBs)
		}
	}
}

func TestGetPageTitleLookupFailureDoesNotReturnPartialSuccess(t *testing.T) {
	for _, failAfter := range []int{0, 1000} {
		t.Run(fmt.Sprint(failAfter), func(t *testing.T) {
			fake := &fakeWikiPageTitlesService{
				page: &types.WikiPage{Slug: "current"}, pages: map[string]*types.WikiPageLite{},
				lookupErr: errors.New("title query failed"), failAfter: failAfter,
			}
			for i := 0; i < 2001; i++ {
				slug := fmt.Sprintf("concept/%d", i)
				fake.page.InLinks = append(fake.page.InLinks, slug)
				fake.pages[slug] = &types.WikiPageLite{Slug: slug, Title: "Title"}
			}
			rec := requestWikiPageTitles(fake, fakeWikiPageTitlesKBService{}, "current")
			assert.Equal(t, http.StatusInternalServerError, rec.Code)
			assert.JSONEq(t, `{"error":"title query failed"}`, rec.Body.String())
		})
	}
}

func TestGetPageValidatesBeforeTitleLookup(t *testing.T) {
	for _, tc := range []struct {
		name   string
		slug   string
		kb     fakeWikiPageTitlesKBService
		getErr error
		status int
	}{
		{"missing slug", "", fakeWikiPageTitlesKBService{}, nil, http.StatusBadRequest},
		{
			"inaccessible KB", "current",
			fakeWikiPageTitlesKBService{err: errors.New("denied")},
			nil, http.StatusBadRequest,
		},
		{"wiki disabled", "current", fakeWikiPageTitlesKBService{disabled: true}, nil, http.StatusBadRequest},
		{"missing page", "current", fakeWikiPageTitlesKBService{}, repository.ErrWikiPageNotFound, http.StatusNotFound},
		{
			"page query failure", "current",
			fakeWikiPageTitlesKBService{},
			errors.New("page query failed"), http.StatusInternalServerError,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeWikiPageTitlesService{getErr: tc.getErr}
			rec := requestWikiPageTitles(fake, tc.kb, tc.slug)
			assert.Equal(t, tc.status, rec.Code)
			assert.Empty(t, fake.lookupKBs)
		})
	}
}

func TestGetPageBacklinkTitlesThroughRepository(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.WikiPage{}))
	current := &types.WikiPage{ID: "current", KnowledgeBaseID: "kb-1", Slug: "current", Title: "Current"}
	pages := make([]types.WikiPage, 0, 2507)
	for i := 0; i < 2507; i++ {
		slug := fmt.Sprintf("concept/%d-%s", i, strings.Repeat("a", 200))
		current.InLinks = append(current.InLinks, slug)
		pages = append(pages, types.WikiPage{
			ID: fmt.Sprint(i), KnowledgeBaseID: "kb-1", Slug: slug, Title: fmt.Sprintf("标题 %d", i),
		})
	}
	current.InLinks = append(current.InLinks, "other-kb-only", "deleted", "missing")
	require.NoError(t, db.CreateInBatches(pages, 100).Error)
	require.NoError(t, db.Create(current).Error)
	require.NoError(t, db.Create(&types.WikiPage{
		ID: "other", KnowledgeBaseID: "kb-2", Slug: "other-kb-only", Title: "Private",
	}).Error)
	// Seed a target that was soft-deleted before the request.
	require.NoError(t, db.Exec(`INSERT INTO wiki_pages (id, knowledge_base_id, slug, title, deleted_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		"deleted", "kb-1", "deleted", "Deleted title").Error)
	repo := repository.NewWikiPageRepository(db)
	h := &WikiPageHandler{
		wikiService: service.NewWikiPageService(repo, nil, nil, nil, nil, nil),
		kbService:   fakeWikiPageTitlesKBService{},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/knowledgebase/kb-1/wiki/pages/current", nil)
	wikiPageTitlesEngine(h).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var body types.WikiPageDetail
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.InLinkTitles, len(pages))
	for _, page := range pages {
		assert.Equal(t, page.Title, body.InLinkTitles[page.Slug])
	}
	assert.NotContains(t, body.InLinkTitles, "other-kb-only")
	assert.NotContains(t, body.InLinkTitles, "deleted")
	assert.NotContains(t, body.InLinkTitles, "missing")
}
