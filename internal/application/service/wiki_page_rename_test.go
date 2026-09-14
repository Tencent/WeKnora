package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newWikiRenameService(
	t *testing.T,
) (*gorm.DB, interfaces.WikiPageRepository, interfaces.WikiPageService, *types.WikiPage) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	require.NoError(
		t,
		db.AutoMigrate(&types.WikiPage{}, &types.WikiPageRevision{}, &types.WikiPageIssue{}, &types.WikiFolder{}),
	)
	require.NoError(t, db.Migrator().DropIndex(&types.WikiPage{}, "idx_kb_slug"))
	require.NoError(t, db.Exec(`CREATE UNIQUE INDEX idx_wiki_pages_kb_slug
		ON wiki_pages (knowledge_base_id, slug) WHERE deleted_at IS NULL`).Error)
	repo := repository.NewWikiPageRepository(db)
	svc := NewWikiPageService(repo, nil, nil, nil, nil)
	page, err := svc.CreatePage(context.Background(), &types.WikiPage{
		TenantID:        42,
		KnowledgeBaseID: "kb-a",
		Slug:            "concept/old",
		Title:           "Topic",
		Content:         "original content",
		PageType:        types.WikiPageTypeConcept,
		SourceRefs:      types.StringArray{"doc-id|Source"},
		ChunkRefs:       types.StringArray{"source-chunk"},
	})
	require.NoError(t, err)
	return db, repo, svc, page
}

func renameServiceRequest(page *types.WikiPage) interfaces.WikiPageRenameRequest {
	return interfaces.WikiPageRenameRequest{
		KnowledgeBaseID: page.KnowledgeBaseID,
		PageID:          page.ID,
		OldSlug:         page.Slug,
		NewSlug:         "concept/new",
	}
}

func TestWikiRenameServiceKeepsHistoryAccessible(t *testing.T) {
	_, _, svc, page := newWikiRenameService(t)
	ctx := context.Background()
	page.Content = "edited content"
	page, err := svc.UpdatePage(ctx, page)
	require.NoError(t, err)
	createdAt := page.CreatedAt
	result, err := svc.(interfaces.WikiPageRenamer).RenamePage(ctx, renameServiceRequest(page))
	require.NoError(t, err)
	require.Equal(t, page.ID, result.Page.ID)
	require.Equal(t, 2, result.Page.Version)
	require.True(t, createdAt.Equal(result.Page.CreatedAt))
	require.Equal(t, page.SourceRefs, result.Page.SourceRefs)
	require.Equal(t, page.ChunkRefs, result.Page.ChunkRefs)
	history, err := svc.ListRevisions(ctx, page.KnowledgeBaseID, "concept/new", 10, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, history.Total)
	require.Equal(t, 2, history.CurrentVersion)
	revision, err := svc.GetRevision(ctx, page.KnowledgeBaseID, "concept/new", 1)
	require.NoError(t, err)
	require.Equal(t, page.ID, revision.PageID)
	require.Equal(t, "original content", revision.Content)
	require.Equal(t, "concept/old", revision.Slug, "historical snapshots retain their original slug")
	_, err = svc.GetPageBySlug(ctx, page.KnowledgeBaseID, "concept/old")
	require.ErrorIs(t, err, repository.ErrWikiPageNotFound)
}

func TestWikiRenameServiceRejectsUnsupportedRepositoryWithoutFallback(t *testing.T) {
	_, repo, _, page := newWikiRenameService(t)
	legacyRepo := struct{ interfaces.WikiPageRepository }{repo}
	svc := NewWikiPageService(legacyRepo, nil, nil, nil, nil)
	result, err := svc.(interfaces.WikiPageRenamer).RenamePage(context.Background(), renameServiceRequest(page))
	require.ErrorIs(t, err, interfaces.ErrWikiRenameUnsupported)
	require.Nil(t, result)
	stored, err := repo.GetByID(context.Background(), page.ID)
	require.NoError(t, err)
	require.Equal(t, "concept/old", stored.Slug)
}

func TestWikiRenameServiceDoesNotMutateAfterCollision(t *testing.T) {
	_, repo, svc, page := newWikiRenameService(t)
	_, err := svc.CreatePage(context.Background(), &types.WikiPage{
		TenantID:        page.TenantID,
		KnowledgeBaseID: page.KnowledgeBaseID,
		Slug:            "concept/new",
		Title:           "Existing",
		PageType:        types.WikiPageTypeConcept,
	})
	require.NoError(t, err)
	result, err := svc.(interfaces.WikiPageRenamer).RenamePage(context.Background(), renameServiceRequest(page))
	require.ErrorIs(t, err, repository.ErrWikiPageSlugConflict)
	require.Nil(t, result)
	stored, err := repo.GetByID(context.Background(), page.ID)
	require.NoError(t, err)
	require.Equal(t, "concept/old", stored.Slug)
}
