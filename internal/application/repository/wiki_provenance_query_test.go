package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestWikiProvenanceQueryReturnsCurrentBlockSourcesAndEnforcesScope(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:wiki_provenance_query?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.WikiPage{},
		&types.Knowledge{},
		&types.Chunk{},
		&types.KnowledgeRevision{},
		&types.WikiProvenancePageRevision{},
		&types.WikiPageBlock{},
		&types.WikiBlockSource{},
	))

	page := &types.WikiPage{
		ID:              "page-1",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		Slug:            "entity/acme",
		Title:           "Acme",
		PageType:        types.WikiPageTypeEntity,
		Status:          types.WikiPageStatusPublished,
		Version:         2,
	}
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		Title:           "Acme source",
		FileName:        "acme.pdf",
		FileType:        "pdf",
	}
	chunk := &types.Chunk{
		ID:              "chunk-1",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		KnowledgeID:     "knowledge-1",
		ChunkIndex:      3,
		Content:         "Acme was founded in 2020 and is headquartered in Shanghai.",
	}
	knowledgeRevision := &types.KnowledgeRevision{
		ID:              "knowledge-revision-1",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		KnowledgeID:     "knowledge-1",
		RevisionNo:      2,
		ParseAttempt:    4,
		Status:          types.KnowledgeRevisionPublished,
		ContentHash:     "hash-1",
	}
	pageRevision := &types.WikiProvenancePageRevision{
		ID:                 "page-revision-2",
		TenantID:           7,
		KnowledgeBaseID:    "kb-1",
		PageID:             "page-1",
		RevisionNo:         2,
		Status:             types.WikiPageRevisionPublished,
		Title:              "Acme",
		RenderedContent:    "Acme was founded in 2020.",
		ProvenanceStatus:   types.WikiProvenanceVerified,
		PublishFingerprint: "fingerprint",
	}
	block := &types.WikiPageBlock{
		ID:               "block-1",
		TenantID:         7,
		KnowledgeBaseID:  "kb-1",
		PageID:           "page-1",
		PageRevisionID:   "page-revision-2",
		LogicalBlockID:   "fact-founded",
		BlockType:        types.WikiBlockFact,
		Content:          "Acme was founded in 2020.",
		AuthorType:       types.WikiBlockAuthorGenerated,
		ProvenanceStatus: types.WikiProvenanceVerified,
	}
	chunkID := chunk.ID
	source := &types.WikiBlockSource{
		ID:                  "source-1",
		TenantID:            7,
		KnowledgeBaseID:     "kb-1",
		PageID:              "page-1",
		BlockID:             "block-1",
		KnowledgeID:         "knowledge-1",
		KnowledgeRevisionID: "knowledge-revision-1",
		ChunkID:             &chunkID,
		SourceStart:         -1,
		SourceEnd:           -1,
		EvidenceHash:        "evidence-hash",
		SourceRole:          types.WikiSourceSupporting,
		Confidence:          1,
		ValidationStatus:    types.WikiSourceValidationVerified,
	}

	for _, value := range []any{page, knowledge, chunk, knowledgeRevision, pageRevision, block, source} {
		require.NoError(t, db.Create(value).Error)
	}

	repo := NewWikiProvenanceRepository(db)
	got, err := repo.GetPageProvenance(context.Background(), 7, "kb-1", "page-1")
	require.NoError(t, err)
	require.Equal(t, "page-revision-2", got.PageRevisionID)
	require.Equal(t, 2, got.RevisionNo)
	require.Equal(t, types.WikiProvenanceVerified, got.ProvenanceStatus)
	require.Len(t, got.Blocks, 1)
	require.Equal(t, "fact-founded", got.Blocks[0].LogicalBlockID)
	require.Len(t, got.Blocks[0].Sources, 1)
	gotSource := got.Blocks[0].Sources[0]
	require.Equal(t, "Acme source", gotSource.KnowledgeTitle)
	require.Equal(t, "acme.pdf", gotSource.FileName)
	require.Equal(t, 2, gotSource.KnowledgeRevisionNo)
	require.Equal(t, 4, gotSource.ParseAttempt)
	require.NotNil(t, gotSource.ChunkIndex)
	require.Equal(t, 3, *gotSource.ChunkIndex)
	require.Contains(t, gotSource.EvidenceExcerpt, "founded in 2020")
	require.True(t, gotSource.SourceAvailable)

	_, err = repo.GetPageProvenance(context.Background(), 8, "kb-1", "page-1")
	require.True(t, errors.Is(err, types.ErrWikiPublishScopeNotFound))
	_, err = repo.GetPageProvenance(context.Background(), 7, "kb-2", "page-1")
	require.True(t, errors.Is(err, types.ErrWikiPublishScopeNotFound))
}

func TestWikiProvenanceQueryReturnsEmptyForPageWithoutPublishedRevision(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:wiki_provenance_query_empty?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.WikiPage{}, &types.WikiProvenancePageRevision{}))
	require.NoError(t, db.Create(&types.WikiPage{
		ID:              "legacy-page",
		TenantID:        9,
		KnowledgeBaseID: "kb-legacy",
		Slug:            "legacy/page",
		Status:          types.WikiPageStatusPublished,
	}).Error)

	repo := NewWikiProvenanceRepository(db)
	got, err := repo.GetPageProvenance(context.Background(), 9, "kb-legacy", "legacy-page")
	require.NoError(t, err)
	require.Equal(t, "legacy-page", got.PageID)
	require.Empty(t, got.PageRevisionID)
	require.Empty(t, got.Blocks)
}

func TestWikiProvenancePublishSnapshotsAndAdvancesExistingPageVersion(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:wiki_provenance_page_history?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.WikiPage{},
		&types.WikiPageRevision{},
		&types.WikiProvenancePageRevision{},
	))

	editedAt := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)
	page := &types.WikiPage{
		ID:              "page-versioned",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		Slug:            "entity/versioned",
		Title:           "Manual title",
		PageType:        types.WikiPageTypeEntity,
		Status:          types.WikiPageStatusPublished,
		Content:         "Manual content",
		Summary:         "Manual summary",
		Version:         7,
		LastEditSource:  types.WikiEditSourceUser,
		LastEditorID:    "user-1",
		UpdatedAt:       editedAt,
	}
	require.NoError(t, db.Create(page).Error)

	repo := NewWikiProvenanceRepository(db)
	next, err := repo.NextPageRevisionNo(context.Background(), 7, "kb-1", page.ID)
	require.NoError(t, err)
	require.Equal(t, 8, next)

	publishedAt := editedAt.Add(time.Hour)
	revision := &types.WikiProvenancePageRevision{
		PageID:          page.ID,
		RevisionNo:      next,
		Title:           "Generated title",
		Summary:         "Generated summary",
		RenderedContent: "Generated fact content",
	}
	projection := *page
	projection.Title = revision.Title
	projection.Summary = revision.Summary
	projection.Content = revision.RenderedContent
	require.NoError(t, repo.UpdateCurrentPage(
		context.Background(), 7, "kb-1", &projection, revision, publishedAt,
	))

	var current types.WikiPage
	require.NoError(t, db.First(&current, "id = ?", page.ID).Error)
	require.Equal(t, 8, current.Version)
	require.Equal(t, "Generated fact content", current.Content)
	require.Equal(t, types.WikiEditSourcePipeline, current.LastEditSource)
	require.Empty(t, current.LastEditorID)

	var snapshot types.WikiPageRevision
	require.NoError(t, db.First(&snapshot, "page_id = ? AND version = ?", page.ID, 7).Error)
	require.Equal(t, "Manual content", snapshot.Content)
	require.Equal(t, types.WikiEditSourceUser, snapshot.EditSource)
	require.Equal(t, "user-1", snapshot.EditorID)
	require.Equal(t, editedAt, snapshot.EditedAt)
}
