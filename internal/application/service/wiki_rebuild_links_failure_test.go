package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const rebuildLinksTestKBID = "kb-rebuild-links"

// newRebuildLinksWiki seeds a three-page wiki in a real SQLite database:
// concept/a -> concept/b -> concept/c. No page has links stored yet, so every
// page must be written for the rebuild to be complete.
func newRebuildLinksWiki(
	t *testing.T,
) (context.Context, *gorm.DB, interfaces.WikiPageRepository, interfaces.WikiPageService) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.WikiFolder{}, &types.WikiPage{}, &types.WikiPageRevision{}))

	ctx := context.Background()
	repo := repository.NewWikiPageRepository(db)
	svc := NewWikiPageService(repo, nil, nil, nil, nil, nil)

	now := time.Now()
	for _, page := range []*types.WikiPage{
		{ID: "page-a", Slug: "concept/a", Title: "A", Content: "see [[concept/b]]"},
		{ID: "page-b", Slug: "concept/b", Title: "B", Content: "see [[concept/c]]"},
		{ID: "page-c", Slug: "concept/c", Title: "C", Content: "nothing to link"},
	} {
		page.TenantID = 1
		page.KnowledgeBaseID = rebuildLinksTestKBID
		page.PageType = types.WikiPageTypeConcept
		page.Status = types.WikiPageStatusPublished
		page.Version = 1
		page.CreatedAt = now
		page.UpdatedAt = now
		require.NoError(t, repo.Create(ctx, page))
	}

	return ctx, db, repo, svc
}

// failWikiMetaUpdatesWhen installs a SQLite trigger that makes every UPDATE on
// wiki_pages fail while `when` holds. The failure is raised by the database
// itself (RAISE(ABORT)), so it travels the real repository write path instead
// of being simulated by a mock.
func failWikiMetaUpdatesWhen(t *testing.T, db *gorm.DB, when string) {
	t.Helper()
	require.NoError(t, db.Exec(fmt.Sprintf(`
		CREATE TRIGGER fail_wiki_meta_update
		BEFORE UPDATE ON wiki_pages
		WHEN %s
		BEGIN
			SELECT RAISE(ABORT, 'injected wiki metadata write failure');
		END`, when)).Error)
}

func TestRebuildLinksSucceedsWhenEveryPageIsWritten(t *testing.T) {
	ctx, _, repo, svc := newRebuildLinksWiki(t)

	require.NoError(t, svc.RebuildLinks(ctx, rebuildLinksTestKBID))

	b, err := repo.GetBySlug(ctx, rebuildLinksTestKBID, "concept/b")
	require.NoError(t, err)
	require.Equal(t, []string{"concept/a"}, []string(b.InLinks))

	c, err := repo.GetBySlug(ctx, rebuildLinksTestKBID, "concept/c")
	require.NoError(t, err)
	require.Equal(t, []string{"concept/b"}, []string(c.InLinks))
}

func TestRebuildLinksReportsPartialWriteFailure(t *testing.T) {
	ctx, db, repo, svc := newRebuildLinksWiki(t)
	// concept/b can no longer be written; the other two pages still can.
	failWikiMetaUpdatesWhen(t, db, "OLD.slug = 'concept/b'")

	err := svc.RebuildLinks(ctx, rebuildLinksTestKBID)

	require.Error(t, err, "one unwritable page must not surface as a successful rebuild")
	require.Contains(t, err.Error(), "1 of 3")
	require.Contains(t, err.Error(), "concept/b")
	require.Contains(t, err.Error(), "injected wiki metadata write failure")

	// Rebuild stays best-effort: the writable pages were still refreshed.
	a, getErr := repo.GetBySlug(ctx, rebuildLinksTestKBID, "concept/a")
	require.NoError(t, getErr)
	require.Equal(t, []string{"concept/b"}, []string(a.OutLinks))

	// concept/b keeps the stale links — the exact half-rebuilt state the
	// caller has to be told about.
	b, getErr := repo.GetBySlug(ctx, rebuildLinksTestKBID, "concept/b")
	require.NoError(t, getErr)
	require.Empty(t, b.InLinks)
}

func TestRebuildLinksReportsTotalWriteFailure(t *testing.T) {
	ctx, db, repo, svc := newRebuildLinksWiki(t)
	failWikiMetaUpdatesWhen(t, db, "1 = 1")

	err := svc.RebuildLinks(ctx, rebuildLinksTestKBID)

	require.Error(t, err, "a rebuild that wrote nothing must not report success")
	require.Contains(t, err.Error(), "3 of 3")
	for _, slug := range []string{"concept/a", "concept/b", "concept/c"} {
		require.Contains(t, err.Error(), slug)
	}
	require.Contains(t, err.Error(), "injected wiki metadata write failure")

	c, getErr := repo.GetBySlug(ctx, rebuildLinksTestKBID, "concept/c")
	require.NoError(t, getErr)
	require.Empty(t, c.InLinks, "no page may be reported as rebuilt when the write failed")
}
