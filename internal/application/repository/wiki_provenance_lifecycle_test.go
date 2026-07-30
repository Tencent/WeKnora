package repository

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openWikiProvenanceLifecycleTestDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.WikiPage{},
		&types.Knowledge{},
		&types.KnowledgeRevision{},
		&types.WikiProvenancePageRevision{},
		&types.WikiPageBlock{},
		&types.WikiBlockSource{},
		&types.WikiPageSource{},
	))
	return db
}

func seedWikiProvenanceLifecycle(t *testing.T, db *gorm.DB) {
	t.Helper()
	values := []any{
		&types.Knowledge{ID: "knowledge-1", TenantID: 7, KnowledgeBaseID: "kb-1", ParseStatus: types.ParseStatusCompleted},
		&types.Knowledge{ID: "knowledge-2", TenantID: 7, KnowledgeBaseID: "kb-1", ParseStatus: types.ParseStatusCompleted},
		&types.Knowledge{ID: "knowledge-other", TenantID: 8, KnowledgeBaseID: "kb-other", ParseStatus: types.ParseStatusCompleted},
		&types.WikiPage{ID: "page-shared", TenantID: 7, KnowledgeBaseID: "kb-1", Slug: "entity/shared", Title: "Shared", Summary: "shared summary", PageType: types.WikiPageTypeEntity, Status: types.WikiPageStatusPublished},
		&types.WikiPage{ID: "page-single", TenantID: 7, KnowledgeBaseID: "kb-1", Slug: "concept/single", Title: "Single", Summary: "single summary", PageType: types.WikiPageTypeConcept, Status: types.WikiPageStatusPublished},
		&types.KnowledgeRevision{ID: "revision-1", TenantID: 7, KnowledgeBaseID: "kb-1", KnowledgeID: "knowledge-1", RevisionNo: 1, Status: types.KnowledgeRevisionPublished},
		&types.WikiProvenancePageRevision{ID: "page-revision-shared", TenantID: 7, KnowledgeBaseID: "kb-1", PageID: "page-shared", RevisionNo: 1, Status: types.WikiPageRevisionPublished},
		&types.WikiProvenancePageRevision{ID: "page-revision-single", TenantID: 7, KnowledgeBaseID: "kb-1", PageID: "page-single", RevisionNo: 1, Status: types.WikiPageRevisionPublished},
		&types.WikiPageBlock{ID: "block-shared", TenantID: 7, KnowledgeBaseID: "kb-1", PageID: "page-shared", PageRevisionID: "page-revision-shared", LogicalBlockID: "shared-fact", BlockType: types.WikiBlockFact},
		&types.WikiPageBlock{ID: "block-single", TenantID: 7, KnowledgeBaseID: "kb-1", PageID: "page-single", PageRevisionID: "page-revision-single", LogicalBlockID: "single-fact", BlockType: types.WikiBlockFact},
		&types.WikiBlockSource{ID: "source-shared", TenantID: 7, KnowledgeBaseID: "kb-1", PageID: "page-shared", BlockID: "block-shared", KnowledgeID: "knowledge-1", KnowledgeRevisionID: "revision-1", SourceStart: -1, SourceEnd: -1},
		&types.WikiBlockSource{ID: "source-single", TenantID: 7, KnowledgeBaseID: "kb-1", PageID: "page-single", BlockID: "block-single", KnowledgeID: "knowledge-1", KnowledgeRevisionID: "revision-1", SourceStart: -1, SourceEnd: -1},
		&types.WikiPageSource{TenantID: 7, KnowledgeBaseID: "kb-1", PageID: "page-shared", KnowledgeID: "knowledge-1", SupportedBlockCount: 1},
		&types.WikiPageSource{TenantID: 7, KnowledgeBaseID: "kb-1", PageID: "page-shared", KnowledgeID: "knowledge-2", SupportedBlockCount: 1},
		&types.WikiPageSource{TenantID: 7, KnowledgeBaseID: "kb-1", PageID: "page-single", KnowledgeID: "knowledge-1", SupportedBlockCount: 1},
	}
	for _, value := range values {
		require.NoError(t, db.Create(value).Error)
	}
}

func TestWikiProvenanceLifecycleDeletesSourcesIdempotentlyAndKeepsTenantScope(t *testing.T) {
	db := openWikiProvenanceLifecycleTestDB(t, "wiki_provenance_lifecycle")
	seedWikiProvenanceLifecycle(t, db)
	repo := NewWikiProvenanceLifecycleRepository(db)

	impacts, err := repo.ListKnowledgePageImpacts(context.Background(), 7, "kb-1", "knowledge-1")
	require.NoError(t, err)
	require.Len(t, impacts, 2)
	require.Equal(t, "concept/single", impacts[0].Slug)
	require.Equal(t, 1, impacts[0].TotalSourceCount)
	require.Equal(t, "entity/shared", impacts[1].Slug)
	require.Equal(t, 2, impacts[1].TotalSourceCount)

	wrongTenant, err := repo.ListKnowledgePageImpacts(context.Background(), 8, "kb-1", "knowledge-1")
	require.NoError(t, err)
	require.Empty(t, wrongTenant)
	_, err = repo.DeleteKnowledgeSources(context.Background(), 8, "kb-1", "knowledge-1", time.Now())
	require.True(t, errors.Is(err, types.ErrWikiPublishScopeNotFound))

	deletedAt := time.Now().UTC().Truncate(time.Second)
	result, err := repo.DeleteKnowledgeSources(context.Background(), 7, "kb-1", "knowledge-1", deletedAt)
	require.NoError(t, err)
	require.Len(t, result.AffectedPages, 2)
	require.EqualValues(t, 2, result.DeletedBlockSources)
	require.EqualValues(t, 2, result.DeletedPageSources)
	require.EqualValues(t, 1, result.DeletedKnowledgeRevisions)

	var count int64
	require.NoError(t, db.Model(&types.WikiBlockSource{}).Where("knowledge_id = ?", "knowledge-1").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Model(&types.WikiPageSource{}).Where("knowledge_id = ?", "knowledge-1").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Model(&types.WikiPageSource{}).Where("knowledge_id = ?", "knowledge-2").Count(&count).Error)
	require.EqualValues(t, 1, count)

	var revision types.KnowledgeRevision
	require.NoError(t, db.Unscoped().First(&revision, "id = ?", "revision-1").Error)
	require.Equal(t, types.KnowledgeRevisionDeleted, revision.Status)
	require.True(t, revision.DeletedAt.Valid)

	again, err := repo.DeleteKnowledgeSources(context.Background(), 7, "kb-1", "knowledge-1", deletedAt.Add(time.Minute))
	require.NoError(t, err)
	require.Empty(t, again.AffectedPages)
	require.Zero(t, again.DeletedBlockSources)
	require.Zero(t, again.DeletedPageSources)
	require.Zero(t, again.DeletedKnowledgeRevisions)
}

func TestWikiProvenanceLifecycleRollsBackEverySourceTable(t *testing.T) {
	db := openWikiProvenanceLifecycleTestDB(t, "wiki_provenance_lifecycle_rollback")
	seedWikiProvenanceLifecycle(t, db)
	require.NoError(t, db.Exec(`
		CREATE TRIGGER reject_page_source_delete
		BEFORE DELETE ON wiki_page_sources
		BEGIN
			SELECT RAISE(ABORT, 'injected page source delete failure');
		END
	`).Error)

	repo := NewWikiProvenanceLifecycleRepository(db)
	_, err := repo.DeleteKnowledgeSources(context.Background(), 7, "kb-1", "knowledge-1", time.Now())
	require.ErrorContains(t, err, "injected page source delete failure")

	var blockCount, pageCount int64
	require.NoError(t, db.Model(&types.WikiBlockSource{}).Where("knowledge_id = ?", "knowledge-1").Count(&blockCount).Error)
	require.NoError(t, db.Model(&types.WikiPageSource{}).Where("knowledge_id = ?", "knowledge-1").Count(&pageCount).Error)
	require.EqualValues(t, 2, blockCount)
	require.EqualValues(t, 2, pageCount)
	var revision types.KnowledgeRevision
	require.NoError(t, db.First(&revision, "id = ?", "revision-1").Error)
	require.Equal(t, types.KnowledgeRevisionPublished, revision.Status)
}

func TestWikiProvenancePublishRejectsKnowledgeOnceDeletionStarts(t *testing.T) {
	db := openWikiProvenanceLifecycleTestDB(t, "wiki_provenance_delete_publish_gate")
	require.NoError(t, db.Create(&types.WikiPage{ID: "page-1", TenantID: 7, KnowledgeBaseID: "kb-1", Slug: "entity/one"}).Error)
	require.NoError(t, db.Create(&types.Knowledge{ID: "knowledge-1", TenantID: 7, KnowledgeBaseID: "kb-1", ParseStatus: types.ParseStatusDeleting}).Error)
	repo := NewWikiProvenanceRepository(db)

	err := repo.LockPublishScope(context.Background(), 7, "kb-1", "page-1", []string{"knowledge-1"})
	require.True(t, errors.Is(err, types.ErrWikiPublishScopeNotFound))

	require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", "knowledge-1").Update("parse_status", types.ParseStatusCompleted).Error)
	require.NoError(t, repo.LockPublishScope(context.Background(), 7, "kb-1", "page-1", []string{"knowledge-1"}))
}
