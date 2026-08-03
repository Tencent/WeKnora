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

type manualWikiProvenanceHarness struct {
	db        *gorm.DB
	page      interfaces.WikiPageService
	publisher interfaces.WikiProvenancePublishService
	query     interfaces.WikiProvenanceQueryService
}

func newManualWikiProvenanceHarness(t *testing.T) manualWikiProvenanceHarness {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.KnowledgeBase{},
		&types.Knowledge{},
		&types.Chunk{},
		&types.WikiFolder{},
		&types.WikiPage{},
		&types.WikiPageRevision{},
		&types.KnowledgeRevision{},
		&types.WikiProvenancePageRevision{},
		&types.WikiPageBlock{},
		&types.WikiBlockSource{},
		&types.WikiPageSource{},
	))
	require.NoError(t, db.Create(&types.KnowledgeBase{
		ID: "kb-manual", TenantID: 7, Name: "Manual Wiki KB",
	}).Error)

	provenanceRepo := repository.NewWikiProvenanceRepository(db)
	publisher := NewWikiProvenancePublishService(provenanceRepo)
	query := NewWikiProvenanceQueryService(provenanceRepo)
	page := NewWikiPageService(
		repository.NewWikiPageRepository(db), nil, nil, nil, nil, publisher, query,
	)
	return manualWikiProvenanceHarness{db: db, page: page, publisher: publisher, query: query}
}

func manualWikiUserContext() context.Context {
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "user-1")
	return types.WithWikiEditSource(ctx, types.WikiEditSourceUser)
}

func TestManualWikiCreatePublishesManualBlocks(t *testing.T) {
	h := newManualWikiProvenanceHarness(t)
	created, err := h.page.CreatePage(manualWikiUserContext(), &types.WikiPage{
		ID: "page-manual", TenantID: 7, KnowledgeBaseID: "kb-manual",
		Slug: "concept/manual", Title: "Manual page", PageType: types.WikiPageTypeConcept,
		Content: "First handwritten paragraph.\n\nSecond handwritten paragraph.",
	})
	require.NoError(t, err)
	require.Equal(t, 1, created.Version)
	require.Equal(t, types.WikiEditSourceUser, created.LastEditSource)
	require.Equal(t, "user-1", created.LastEditorID)

	got, err := h.query.GetPageProvenance(context.Background(), 7, "kb-manual", created.ID)
	require.NoError(t, err)
	require.Equal(t, created.Version, got.RevisionNo)
	require.Equal(t, created.Version, got.CurrentPageVersion)
	require.Empty(t, got.StaleReason)
	require.Len(t, got.Blocks, 2)
	for _, block := range got.Blocks {
		require.Equal(t, types.WikiBlockAuthorManual, block.AuthorType)
		require.Empty(t, block.Sources)
	}
}

func TestRenderStoredWikiBlockMatchesGeneratedAndManualMarkdown(t *testing.T) {
	require.Equal(t, "- 1. Generated item", renderStoredWikiBlock(types.WikiPageProvenanceBlock{
		BlockType: types.WikiBlockListItem, Content: "1. Generated item",
		AuthorType: types.WikiBlockAuthorGenerated,
	}))
	require.Equal(t, "+ Manual item", renderStoredWikiBlock(types.WikiPageProvenanceBlock{
		BlockType: types.WikiBlockListItem, Content: "+ Manual item",
		AuthorType: types.WikiBlockAuthorManual,
	}))
}

func TestManualWikiEditOnlyClearsChangedBlockSources(t *testing.T) {
	h := newManualWikiProvenanceHarness(t)
	knowledge := &types.Knowledge{
		ID: "knowledge-1", TenantID: 7, KnowledgeBaseID: "kb-manual",
		Title: "Source", FileName: "source.md", ParseStatus: types.ParseStatusCompleted,
	}
	require.NoError(t, h.db.Create(knowledge).Error)
	for index, content := range []string{"Original first fact.", "Original second fact."} {
		require.NoError(t, h.db.Create(&types.Chunk{
			ID: "chunk-" + string(rune('1'+index)), TenantID: 7,
			KnowledgeBaseID: "kb-manual", KnowledgeID: knowledge.ID,
			ChunkIndex: index, Content: content,
		}).Error)
	}
	chunk1 := "chunk-1"
	chunk2 := "chunk-2"
	_, err := h.publisher.Publish(context.Background(), &types.WikiProvenancePublishRequest{
		TenantID: 7, KnowledgeBaseID: "kb-manual", PageID: "page-generated",
		IdempotencyKey: "seed-generated-page",
		PageProjection: types.WikiPage{
			ID: "page-generated", TenantID: 7, KnowledgeBaseID: "kb-manual",
			Slug: "entity/generated", Title: "Generated", PageType: types.WikiPageTypeEntity,
			SourceRefs: types.StringArray{"knowledge-1|Source"},
			ChunkRefs:  types.StringArray{chunk1, chunk2},
		},
		KnowledgeRevisions: []types.KnowledgeRevision{{
			ID: "revision-alias", KnowledgeID: knowledge.ID,
			ContentHash: "knowledge-content-hash", ParseAttempt: 1,
		}},
		PageRevision: types.WikiProvenancePageRevision{
			Title: "Generated", RenderedContent: "Original first fact.\n\nOriginal second fact.",
			ProvenanceStatus: types.WikiProvenanceVerified,
		},
		Blocks: []types.WikiPageBlock{
			{ID: "first", LogicalBlockID: "fact-first", BlockType: types.WikiBlockFact,
				SortOrder: 0, Content: "Original first fact.", AuthorType: types.WikiBlockAuthorGenerated,
				ProvenanceStatus: types.WikiProvenanceVerified},
			{ID: "second", LogicalBlockID: "fact-second", BlockType: types.WikiBlockFact,
				SortOrder: 1, Content: "Original second fact.", AuthorType: types.WikiBlockAuthorGenerated,
				ProvenanceStatus: types.WikiProvenanceVerified},
		},
		Sources: []types.WikiBlockSource{
			{BlockID: "first", KnowledgeID: knowledge.ID, KnowledgeRevisionID: "revision-alias",
				ChunkID: &chunk1, SourceStart: -1, SourceEnd: -1, EvidenceHash: "hash-1",
				SourceRole: types.WikiSourceSupporting, Confidence: 1,
				ValidationStatus: types.WikiSourceValidationVerified},
			{BlockID: "second", KnowledgeID: knowledge.ID, KnowledgeRevisionID: "revision-alias",
				ChunkID: &chunk2, SourceStart: -1, SourceEnd: -1, EvidenceHash: "hash-2",
				SourceRole: types.WikiSourceSupporting, Confidence: 1,
				ValidationStatus: types.WikiSourceValidationVerified},
		},
	})
	require.NoError(t, err)

	current, err := h.page.GetPageBySlug(context.Background(), "kb-manual", "entity/generated")
	require.NoError(t, err)
	require.Equal(t, 1, current.Version)
	edit := *current
	edit.Content = "Manually changed first paragraph.\n\nOriginal second fact."
	updated, err := h.page.UpdatePage(manualWikiUserContext(), &edit)
	require.NoError(t, err)
	require.Equal(t, 2, updated.Version)
	require.Equal(t, types.WikiEditSourceUser, updated.LastEditSource)

	got, err := h.query.GetPageProvenance(context.Background(), 7, "kb-manual", updated.ID)
	require.NoError(t, err)
	require.Equal(t, 2, got.RevisionNo)
	require.Equal(t, 2, got.CurrentPageVersion)
	require.Empty(t, got.StaleReason)
	require.Len(t, got.Blocks, 2)
	require.Equal(t, "Manually changed first paragraph.", got.Blocks[0].Content)
	require.Equal(t, types.WikiBlockAuthorManual, got.Blocks[0].AuthorType)
	require.Empty(t, got.Blocks[0].Sources)
	require.Equal(t, "fact-second", got.Blocks[1].LogicalBlockID)
	require.Equal(t, types.WikiBlockAuthorGenerated, got.Blocks[1].AuthorType)
	require.Len(t, got.Blocks[1].Sources, 1)
	require.NotNil(t, got.Blocks[1].Sources[0].ChunkID)
	require.Equal(t, chunk2, *got.Blocks[1].Sources[0].ChunkID)
	require.Equal(t, types.StringArray{chunk2}, updated.ChunkRefs)

	var oldRevision types.WikiProvenancePageRevision
	require.NoError(t, h.db.Where("page_id = ? AND revision_no = ?", updated.ID, 1).
		First(&oldRevision).Error)
	require.Equal(t, types.WikiPageRevisionSuperseded, oldRevision.Status)
}
