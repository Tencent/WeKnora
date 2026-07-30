package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	ifaces "github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type wikiRevisionTestHarness struct {
	svc ifaces.WikiPageService
}

func newWikiRevisionTestService(t *testing.T) (context.Context, wikiRevisionTestHarness, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.WikiFolder{}, &types.WikiPage{}, &types.WikiPageRevision{}))
	repo := repository.NewWikiPageRepository(db)
	svc := NewWikiPageService(repo, nil, nil, nil, nil)
	return context.Background(), wikiRevisionTestHarness{svc: svc}, db
}

func TestUpdateWikiPageSnapshotsSupersededVersion(t *testing.T) {
	ctx, h, _ := newWikiRevisionTestService(t)
	const kb = "kb-rev"

	created, err := h.svc.CreatePage(types.WithWikiEditSource(ctx, types.WikiEditSourceUser), &types.WikiPage{
		KnowledgeBaseID: kb, TenantID: 1, Slug: "concept/rag",
		Title: "RAG", PageType: types.WikiPageTypeConcept, Content: "v1 body", Summary: "s1",
	})
	require.NoError(t, err)
	require.Equal(t, 1, created.Version)
	require.Equal(t, types.WikiEditSourceUser, created.LastEditSource)

	// First real edit: v1 must be snapshotted, v2 becomes current.
	edit := *created
	edit.Content = "v2 body"
	updated, err := h.svc.UpdatePage(types.WithWikiEditSource(ctx, types.WikiEditSourceAgent), &edit)
	require.NoError(t, err)
	require.Equal(t, 2, updated.Version)
	require.Equal(t, types.WikiEditSourceAgent, updated.LastEditSource)

	resp, err := h.svc.ListRevisions(ctx, kb, "concept/rag", 50, 0)
	require.NoError(t, err)
	require.Equal(t, 2, resp.CurrentVersion)
	require.Equal(t, int64(1), resp.Total)
	require.Len(t, resp.Revisions, 1)
	require.Equal(t, 1, resp.Revisions[0].Version)
	// Snapshot attribution is the author of the superseded version, not the
	// editor that replaced it.
	require.Equal(t, types.WikiEditSourceUser, resp.Revisions[0].EditSource)
	// List mode omits the content column.
	require.Empty(t, resp.Revisions[0].Content)

	rev, err := h.svc.GetRevision(ctx, kb, "concept/rag", 1)
	require.NoError(t, err)
	require.Equal(t, "v1 body", rev.Content)

	// Bookkeeping-only write (identical content) must not create a snapshot.
	same := *updated
	_, err = h.svc.UpdatePage(ctx, &same)
	require.NoError(t, err)
	resp, err = h.svc.ListRevisions(ctx, kb, "concept/rag", 50, 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), resp.Total)
	require.Equal(t, 2, resp.CurrentVersion)
}

func TestUpdateWikiPagePersistsClearedFields(t *testing.T) {
	ctx, h, _ := newWikiRevisionTestService(t)
	const kb = "kb-clear"

	created, err := h.svc.CreatePage(ctx, &types.WikiPage{
		KnowledgeBaseID: kb, TenantID: 1, Slug: "concept/x",
		Title: "X", PageType: types.WikiPageTypeConcept, Content: "body", Summary: "to be cleared",
	})
	require.NoError(t, err)

	edit := *created
	edit.Summary = ""
	updated, err := h.svc.UpdatePage(ctx, &edit)
	require.NoError(t, err)
	require.Equal(t, 2, updated.Version)
	require.Empty(t, updated.Summary)

	// The cleared value must actually be stored, not skipped by a zero-value
	// aware write path: a fresh update with identical fields must be treated
	// as a no-op (no version bump).
	again := *updated
	roundTripped, err := h.svc.UpdatePage(ctx, &again)
	require.NoError(t, err)
	require.Equal(t, 2, roundTripped.Version)
	require.Empty(t, roundTripped.Summary)
}

func TestRevertWikiPageToVersion(t *testing.T) {
	ctx, h, _ := newWikiRevisionTestService(t)
	const kb = "kb-revert"

	created, err := h.svc.CreatePage(ctx, &types.WikiPage{
		KnowledgeBaseID: kb, TenantID: 1, Slug: "entity/acme",
		Title: "Acme", PageType: types.WikiPageTypeEntity, Content: "original body", Summary: "s1",
	})
	require.NoError(t, err)

	edit := *created
	edit.Content = "rewritten body"
	edit.Title = "Acme Corp"
	_, err = h.svc.UpdatePage(ctx, &edit)
	require.NoError(t, err)

	// Reverting to the current version is a client mistake, and must be
	// distinguishable so the handler can answer 400 rather than 500.
	_, err = h.svc.RevertPageToVersion(ctx, kb, "entity/acme", 2)
	require.ErrorIs(t, err, ErrWikiRevertToCurrentVersion)

	reverted, err := h.svc.RevertPageToVersion(ctx, kb, "entity/acme", 1)
	require.NoError(t, err)
	require.Equal(t, 3, reverted.Version, "revert applies as a fresh edit")
	require.Equal(t, "original body", reverted.Content)
	require.Equal(t, "Acme", reverted.Title)
	require.Equal(t, types.WikiEditSourceRevert, reverted.LastEditSource)

	// Both superseded versions are now snapshotted, so the revert itself is
	// undoable.
	resp, err := h.svc.ListRevisions(ctx, kb, "entity/acme", 50, 0)
	require.NoError(t, err)
	require.Equal(t, int64(2), resp.Total)
	require.Equal(t, 2, resp.Revisions[0].Version)
	rev2, err := h.svc.GetRevision(ctx, kb, "entity/acme", 2)
	require.NoError(t, err)
	require.Equal(t, "rewritten body", rev2.Content)
}

func TestWikiPageRevisionsArePruned(t *testing.T) {
	ctx, h, db := newWikiRevisionTestService(t)
	const kb = "kb-prune-rev"

	page, err := h.svc.CreatePage(ctx, &types.WikiPage{
		KnowledgeBaseID: kb, TenantID: 1, Slug: "concept/hot",
		Title: "Hot", PageType: types.WikiPageTypeConcept, Content: "v1", Summary: "s",
	})
	require.NoError(t, err)

	total := types.WikiMaxRevisionsPerPage + 5
	for i := 2; i <= total+1; i++ {
		edit := *page
		edit.Content = fmt.Sprintf("v%d", i)
		page, err = h.svc.UpdatePage(ctx, &edit)
		require.NoError(t, err)
	}
	require.Equal(t, total+1, page.Version)

	var count int64
	require.NoError(t, db.Model(&types.WikiPageRevision{}).Where("page_id = ?", page.ID).Count(&count).Error)
	require.LessOrEqual(t, count, int64(types.WikiMaxRevisionsPerPage))

	// The newest snapshot survives, the oldest is gone.
	_, err = h.svc.GetRevision(ctx, kb, "concept/hot", page.Version-1)
	require.NoError(t, err)
	_, err = h.svc.GetRevision(ctx, kb, "concept/hot", 1)
	require.Error(t, err)
}

// A page the pipeline rewrites constantly must not lose the handful of human
// edits that motivated keeping history in the first place.
func TestPipelineChurnDoesNotEvictHumanRevisions(t *testing.T) {
	ctx, h, _ := newWikiRevisionTestService(t)
	const kb = "kb-mixed-rev"

	page, err := h.svc.CreatePage(types.WithWikiEditSource(ctx, types.WikiEditSourceUser), &types.WikiPage{
		KnowledgeBaseID: kb, TenantID: 1, Slug: "concept/hub",
		Title: "Hub", PageType: types.WikiPageTypeConcept, Content: "handwritten v1", Summary: "s",
	})
	require.NoError(t, err)

	// One more human edit, then a long tail of pipeline rewrites.
	edit := *page
	edit.Content = "handwritten v2"
	page, err = h.svc.UpdatePage(types.WithWikiEditSource(ctx, types.WikiEditSourceUser), &edit)
	require.NoError(t, err)

	for i := 0; i < types.WikiMaxRevisionsPerPage+10; i++ {
		next := *page
		next.Content = fmt.Sprintf("pipeline rewrite %d", i)
		page, err = h.svc.UpdatePage(ctx, &next)
		require.NoError(t, err)
	}

	v1, err := h.svc.GetRevision(ctx, kb, "concept/hub", 1)
	require.NoError(t, err, "the first human version must outlive pipeline churn")
	require.Equal(t, "handwritten v1", v1.Content)
	v2, err := h.svc.GetRevision(ctx, kb, "concept/hub", 2)
	require.NoError(t, err)
	require.Equal(t, "handwritten v2", v2.Content)

	// Old pipeline snapshots are still pruned.
	_, err = h.svc.GetRevision(ctx, kb, "concept/hub", 3)
	require.Error(t, err)
}

func TestDeletePageDropsRevisionHistory(t *testing.T) {
	ctx, h, db := newWikiRevisionTestService(t)
	const kb = "kb-del-rev"

	page, err := h.svc.CreatePage(ctx, &types.WikiPage{
		KnowledgeBaseID: kb, TenantID: 1, Slug: "concept/doomed",
		Title: "Doomed", PageType: types.WikiPageTypeConcept, Content: "v1", Summary: "s",
	})
	require.NoError(t, err)
	edit := *page
	edit.Content = "v2"
	page, err = h.svc.UpdatePage(ctx, &edit)
	require.NoError(t, err)

	var before int64
	require.NoError(t, db.Model(&types.WikiPageRevision{}).Where("page_id = ?", page.ID).Count(&before).Error)
	require.Equal(t, int64(1), before)

	require.NoError(t, h.svc.DeletePage(ctx, kb, "concept/doomed"))

	var after int64
	require.NoError(t, db.Model(&types.WikiPageRevision{}).Where("page_id = ?", page.ID).Count(&after).Error)
	require.Zero(t, after, "a deleted page must not leave unreachable content snapshots behind")
}
