package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newRebuildLinksHandler wires the real wiki page service over a real SQLite
// repository into the HTTP handler, so the response contract is asserted
// end-to-end instead of through a stubbed service.
func newRebuildLinksHandler(t *testing.T) (*gin.Engine, *gorm.DB, interfaces.WikiPageRepository) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.WikiFolder{}, &types.WikiPage{}, &types.WikiPageRevision{}))

	ctx := context.Background()
	repo := repository.NewWikiPageRepository(db)
	now := time.Now()
	for _, page := range []*types.WikiPage{
		{ID: "page-a", Slug: "concept/a", Title: "A", Content: "see [[concept/b]]"},
		{ID: "page-b", Slug: "concept/b", Title: "B", Content: "nothing to link"},
	} {
		page.TenantID = 1
		page.KnowledgeBaseID = "kb-rebuild-links"
		page.PageType = types.WikiPageTypeConcept
		page.Status = types.WikiPageStatusPublished
		page.Version = 1
		page.CreatedAt = now
		page.UpdatedAt = now
		require.NoError(t, repo.Create(ctx, page))
	}

	handler := &WikiPageHandler{
		wikiService: service.NewWikiPageService(repo, nil, nil, nil, nil, nil),
		kbService: &stubKBService{get: func(_ context.Context, id string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{
				ID:               id,
				TenantID:         1,
				IndexingStrategy: types.IndexingStrategy{WikiEnabled: true},
			}, nil
		}},
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/knowledgebase/:kb_id/wiki/rebuild-links", func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		handler.RebuildLinks(c)
	})
	return engine, db, repo
}

func postRebuildLinks(engine *gin.Engine) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/knowledgebase/kb-rebuild-links/wiki/rebuild-links", nil)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)
	return recorder
}

func TestRebuildLinksHandlerReportsPartialWriteFailure(t *testing.T) {
	engine, db, _ := newRebuildLinksHandler(t)
	require.NoError(t, db.Exec(`
		CREATE TRIGGER fail_wiki_meta_update
		BEFORE UPDATE ON wiki_pages
		WHEN OLD.slug = 'concept/b'
		BEGIN
			SELECT RAISE(ABORT, 'injected wiki metadata write failure');
		END`).Error)

	recorder := postRebuildLinks(engine)
	t.Logf("rebuild-links response: %d %s", recorder.Code, recorder.Body.String())

	require.Equal(t, http.StatusInternalServerError, recorder.Code,
		"a half-rebuilt link graph must not answer 200")
	require.NotContains(t, recorder.Body.String(), "Links rebuilt successfully")
	require.Contains(t, recorder.Body.String(), "concept/b")
	require.Contains(t, recorder.Body.String(), "injected wiki metadata write failure")
}

func TestRebuildLinksHandlerReportsSuccessWhenEveryPageIsWritten(t *testing.T) {
	engine, _, _ := newRebuildLinksHandler(t)

	recorder := postRebuildLinks(engine)
	t.Logf("rebuild-links response: %d %s", recorder.Code, recorder.Body.String())

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "Links rebuilt successfully")
}
