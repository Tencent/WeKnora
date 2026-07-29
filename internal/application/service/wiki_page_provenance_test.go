package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type wikiProvenanceValidationChunkRepo struct {
	interfaces.ChunkRepository
}

func (wikiProvenanceValidationChunkRepo) ListChunksByID(
	context.Context, uint64, []string,
) ([]*types.Chunk, error) {
	return nil, nil
}

func TestRefreshWikiBlockSourceValidationAcceptsManualNoSourceBlocks(t *testing.T) {
	svc := &wikiPageService{chunkRepo: wikiProvenanceValidationChunkRepo{}}
	set := &types.WikiPageBlockSet{Blocks: []*types.WikiPageBlock{
		{
			ID: "manual-paragraph", BlockType: types.WikiBlockTypeParagraph,
			Content:    "This paragraph was written by a person.",
			AuthorType: types.WikiEditSourceUser, ProvenanceStatus: types.WikiBlockProvenanceUnsupported,
		},
	}}

	checked, allFresh, err := svc.refreshWikiBlockSourceValidation(
		context.Background(), 1, "kb-1", set,
	)
	if err != nil {
		t.Fatalf("refreshWikiBlockSourceValidation() error = %v", err)
	}
	if !checked || !allFresh {
		t.Fatalf("manual no-source block validation = checked %v, allFresh %v; want true, true", checked, allFresh)
	}
}

func newWikiProvenanceServiceTest(t *testing.T) (context.Context, interfaces.WikiPageService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.Chunk{},
		&types.KnowledgeProcessingSpan{},
		&types.WikiFolder{},
		&types.WikiPage{},
		&types.WikiPageRevision{},
		&types.WikiPageBlockSet{},
		&types.WikiPageBlock{},
		&types.WikiBlockSource{},
	))
	repo := repository.NewWikiPageRepository(db)
	require.Implements(t, (*interfaces.WikiProvenanceRepository)(nil), repo)
	return context.Background(), NewWikiPageService(
		repo, repository.NewChunkRepository(db), nil, nil, nil,
	), db
}

func sourcedWikiTestBlock(blockType, content, logicalID, knowledgeID, chunkID string) *types.WikiPageBlock {
	block := &types.WikiPageBlock{
		LogicalBlockID:   logicalID,
		BlockType:        blockType,
		Content:          content,
		AuthorType:       types.WikiEditSourcePipeline,
		ProvenanceStatus: types.WikiBlockProvenanceVerified,
	}
	if knowledgeID != "" {
		evidence := stringsForWikiProvenanceTest(knowledgeID)
		block.Sources = []*types.WikiBlockSource{{
			KnowledgeID:      knowledgeID,
			DocumentTitle:    "Document " + knowledgeID,
			KnowledgeAttempt: 1,
			ChunkID:          chunkID,
			Evidence:         evidence,
			ChunkContentHash: wikiTextHash(evidence),
			ValidationStatus: types.WikiSourceValidationLocated,
		}}
	}
	return block
}

func stringsForWikiProvenanceTest(knowledgeID string) string {
	return "evidence from " + knowledgeID
}

func createWikiProvenanceTestChunk(
	t *testing.T, db *gorm.DB, tenantID uint64, kbID, knowledgeID, chunkID string,
) *types.Chunk {
	t.Helper()
	evidence := stringsForWikiProvenanceTest(knowledgeID)
	chunk := &types.Chunk{
		ID: chunkID, TenantID: tenantID, KnowledgeBaseID: kbID, KnowledgeID: knowledgeID,
		Content: evidence, SourceContent: evidence, ChunkType: types.ChunkTypeText,
		IsEnabled: true, IndexStatus: "ready",
	}
	require.NoError(t, repository.NewChunkRepository(db).CreateChunks(
		context.Background(), []*types.Chunk{chunk},
	))
	return chunk
}

func createWikiProvenanceTestChunksForSet(
	t *testing.T, db *gorm.DB, tenantID uint64, kbID string, set *types.WikiPageBlockSet,
) {
	t.Helper()
	seen := make(map[string]struct{})
	for _, block := range set.Blocks {
		for _, source := range block.Sources {
			if source == nil || source.ChunkID == "" {
				continue
			}
			if source.KnowledgeID != "" && source.KnowledgeAttempt > 0 {
				span := &types.KnowledgeProcessingSpan{
					KnowledgeID: source.KnowledgeID,
					Attempt:     source.KnowledgeAttempt,
					SpanID: fmt.Sprintf(
						"test-%s-%d", source.KnowledgeID, source.KnowledgeAttempt,
					),
				}
				require.NoError(t, db.Where(
					"knowledge_id = ? AND attempt = ?", source.KnowledgeID, source.KnowledgeAttempt,
				).FirstOrCreate(span).Error)
			}
			if _, exists := seen[source.ChunkID]; exists {
				continue
			}
			seen[source.ChunkID] = struct{}{}
			var count int64
			require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", source.ChunkID).Count(&count).Error)
			if count > 0 {
				continue
			}
			chunk := &types.Chunk{
				ID: source.ChunkID, TenantID: tenantID, KnowledgeBaseID: kbID,
				KnowledgeID: source.KnowledgeID, Content: source.Evidence,
				SourceContent: source.Evidence, ContentRevision: source.ChunkRevision,
				ChunkType: types.ChunkTypeText, IsEnabled: true, IndexStatus: "ready",
			}
			require.NoError(t, repository.NewChunkRepository(db).CreateChunks(
				context.Background(), []*types.Chunk{chunk},
			))
		}
	}
}

func TestSavePageWithProvenanceAndManualBodyEdit(t *testing.T) {
	ctx, baseService, db := newWikiProvenanceServiceTest(t)
	provenance := baseService.(interfaces.WikiProvenanceService)
	page := &types.WikiPage{
		TenantID: 1, KnowledgeBaseID: "kb-provenance-service", Slug: "concept/sources",
		Title: "Sources", PageType: types.WikiPageTypeConcept, Status: types.WikiPageStatusPublished,
		Content: "# Sources\n\nA sourced fact.\n", Summary: "Sourced summary",
	}
	set := &types.WikiPageBlockSet{
		RenderedContent: page.Content,
		RenderedSummary: page.Summary,
		Blocks: []*types.WikiPageBlock{
			sourcedWikiTestBlock(types.WikiBlockTypeSummary, page.Summary, "summary", "knowledge-a", "chunk-summary"),
			sourcedWikiTestBlock(types.WikiBlockTypeHeading, "# Sources\n\n", "heading", "", ""),
			sourcedWikiTestBlock(types.WikiBlockTypeParagraph, "A sourced fact.\n", "paragraph", "knowledge-a", "chunk-a"),
		},
	}
	createWikiProvenanceTestChunksForSet(t, db, page.TenantID, page.KnowledgeBaseID, set)

	created, err := provenance.SavePageWithProvenance(ctx, page, set)
	require.NoError(t, err)
	require.NotEmpty(t, created.CurrentBlockSetID)
	require.Equal(t, types.StringArray{"knowledge-a"}, created.SourceRefs)
	require.Equal(t, types.StringArray{"chunk-summary", "chunk-a"}, created.ChunkRefs)

	detail, err := provenance.GetPageWithSources(ctx, page.KnowledgeBaseID, page.Slug)
	require.NoError(t, err)
	require.Len(t, detail.Blocks, 3)
	require.NotEmpty(t, detail.Blocks[0].Sources[0].CitationKey)

	manual := *created
	manual.Content = "A human rewrote this body."
	updated, err := baseService.UpdatePage(types.WithWikiEditSource(ctx, types.WikiEditSourceUser), &manual)
	require.NoError(t, err)
	require.NotEmpty(t, updated.CurrentBlockSetID)
	require.NotEqual(t, created.CurrentBlockSetID, updated.CurrentBlockSetID)
	require.Equal(t, types.StringArray{"knowledge-a"}, updated.SourceRefs,
		"an unchanged sourced summary must retain its valid source")
	require.Equal(t, types.StringArray{"chunk-summary"}, updated.ChunkRefs)

	manualDetail, err := provenance.GetPageWithSources(ctx, page.KnowledgeBaseID, page.Slug)
	require.NoError(t, err)
	require.Len(t, manualDetail.Blocks, 2)
	require.Equal(t, types.WikiBlockTypeSummary, manualDetail.Blocks[0].BlockType)
	require.NotEmpty(t, manualDetail.Blocks[0].Sources)
	require.Equal(t, types.WikiEditSourceUser, manualDetail.Blocks[1].AuthorType)
	require.Empty(t, manualDetail.Blocks[1].Sources)
	var storedSet types.WikiPageBlockSet
	require.NoError(t, db.First(&storedSet, "id = ?", set.ID).Error)
	require.Equal(t, types.WikiBlockSetStatusSuperseded, storedSet.Status)
}

func TestManualCreateAndLegacyRewriteBootstrapUserBlocks(t *testing.T) {
	ctx, baseService, _ := newWikiProvenanceServiceTest(t)
	provenance := baseService.(interfaces.WikiProvenanceService)
	userCtx := types.WithWikiEditSource(ctx, types.WikiEditSourceUser)

	created, err := baseService.CreatePage(userCtx, &types.WikiPage{
		TenantID: 1, KnowledgeBaseID: "kb-manual-bootstrap", Slug: "concept/manual-create",
		Title: "Manual create", Content: "A person wrote this page.\n",
	})
	require.NoError(t, err)
	require.NotEmpty(t, created.CurrentBlockSetID)
	detail, err := provenance.GetPageWithSources(ctx, created.KnowledgeBaseID, created.Slug)
	require.NoError(t, err)
	require.Len(t, detail.Blocks, 1)
	require.Equal(t, types.WikiEditSourceUser, detail.Blocks[0].AuthorType)
	require.Empty(t, detail.Blocks[0].Sources)

	// A pipeline-created legacy row can still have page-level compatibility
	// refs. The first user body edit upgrades it to user blocks and clears those
	// stale refs rather than carrying them onto unrelated prose.
	legacy, err := baseService.CreatePage(ctx, &types.WikiPage{
		TenantID: 1, KnowledgeBaseID: "kb-manual-bootstrap", Slug: "concept/legacy-rewrite",
		Title: "Legacy", Content: "Old generated prose.\n",
		SourceRefs: types.StringArray{"knowledge-old"}, ChunkRefs: types.StringArray{"chunk-old"},
	})
	require.NoError(t, err)
	require.Empty(t, legacy.CurrentBlockSetID)

	rewrite := *legacy
	rewrite.Content = "A person replaced all legacy prose.\n"
	updated, err := baseService.UpdatePage(userCtx, &rewrite)
	require.NoError(t, err)
	require.NotEmpty(t, updated.CurrentBlockSetID)
	require.Empty(t, updated.SourceRefs)
	require.Empty(t, updated.ChunkRefs)
	detail, err = provenance.GetPageWithSources(ctx, updated.KnowledgeBaseID, updated.Slug)
	require.NoError(t, err)
	require.Len(t, detail.Blocks, 1)
	require.Equal(t, types.WikiEditSourceUser, detail.Blocks[0].AuthorType)
	require.Empty(t, detail.Blocks[0].Sources)
}

func TestManualParagraphSurvivesSourceDocumentDeletion(t *testing.T) {
	ctx, baseService, db := newWikiProvenanceServiceTest(t)
	provenance := baseService.(interfaces.WikiProvenanceService)
	page := &types.WikiPage{
		TenantID: 1, KnowledgeBaseID: "kb-provenance-mixed-edit", Slug: "concept/mixed-edit",
		Title: "Mixed edit", PageType: types.WikiPageTypeConcept, Status: types.WikiPageStatusPublished,
		Content: "# Mixed edit\n\nA sourced fact.\n",
	}
	set := &types.WikiPageBlockSet{
		RenderedContent: page.Content,
		Blocks: []*types.WikiPageBlock{
			sourcedWikiTestBlock(types.WikiBlockTypeHeading, "# Mixed edit\n\n", "heading", "", ""),
			sourcedWikiTestBlock(types.WikiBlockTypeParagraph, "A sourced fact.\n", "fact", "knowledge-a", "chunk-a"),
		},
	}
	createWikiProvenanceTestChunksForSet(t, db, page.TenantID, page.KnowledgeBaseID, set)
	created, err := provenance.SavePageWithProvenance(ctx, page, set)
	require.NoError(t, err)

	manual := *created
	manual.Content = "# Mixed edit\n\nA sourced fact.\n\nA person added this paragraph.\n"
	updated, err := baseService.UpdatePage(types.WithWikiEditSource(ctx, types.WikiEditSourceUser), &manual)
	require.NoError(t, err)
	require.Equal(t, types.StringArray{"knowledge-a"}, updated.SourceRefs)

	detail, err := provenance.GetPageWithSources(ctx, page.KnowledgeBaseID, page.Slug)
	require.NoError(t, err)
	require.Len(t, detail.Blocks, 3)
	require.Equal(t, types.WikiBlockProvenanceVerified, detail.Blocks[1].ProvenanceStatus)
	require.Equal(t, types.WikiEditSourceUser, detail.Blocks[2].AuthorType)
	require.Empty(t, detail.Blocks[2].Sources)

	handled, deleted, err := provenance.RemoveKnowledgeFromPage(
		ctx, page.KnowledgeBaseID, page.Slug, "knowledge-a",
	)
	require.NoError(t, err)
	require.True(t, handled)
	require.False(t, deleted, "a user-authored paragraph must keep the page alive")

	detail, err = provenance.GetPageWithSources(ctx, page.KnowledgeBaseID, page.Slug)
	require.NoError(t, err)
	require.Contains(t, detail.Content, "A person added this paragraph.")
	require.NotContains(t, detail.Content, "A sourced fact.")
	require.Empty(t, detail.SourceRefs)
	require.Len(t, detail.Blocks, 2)
	require.Equal(t, types.WikiEditSourceUser, detail.Blocks[1].AuthorType)
	require.Empty(t, detail.Blocks[1].Sources)
}

func TestSavePageWithProvenanceRejectsPartialClaimCoverage(t *testing.T) {
	ctx, baseService, db := newWikiProvenanceServiceTest(t)
	provenance := baseService.(interfaces.WikiProvenanceService)
	page := &types.WikiPage{
		TenantID: 1, KnowledgeBaseID: "kb-provenance-partial", Slug: "concept/partial",
		Title: "Partial", PageType: types.WikiPageTypeConcept, Status: types.WikiPageStatusPublished,
		Content: "# Partial\n\nOne supported claim and one unsupported claim.\n",
	}
	paragraph := sourcedWikiTestBlock(
		types.WikiBlockTypeParagraph,
		"One supported claim and one unsupported claim.\n",
		"partial-fact",
		"knowledge-a",
		"chunk-a",
	)
	paragraph.ProvenanceStatus = types.WikiBlockProvenancePartial
	set := &types.WikiPageBlockSet{
		RenderedContent: page.Content,
		Blocks: []*types.WikiPageBlock{
			sourcedWikiTestBlock(types.WikiBlockTypeHeading, "# Partial\n\n", "heading", "", ""),
			paragraph,
		},
	}
	createWikiProvenanceTestChunksForSet(t, db, page.TenantID, page.KnowledgeBaseID, set)

	_, err := provenance.SavePageWithProvenance(ctx, page, set)
	require.ErrorContains(t, err, "complete claim coverage")
}

func TestMetadataOnlyPageEditKeepsSourcesReadable(t *testing.T) {
	ctx, baseService, db := newWikiProvenanceServiceTest(t)
	provenance := baseService.(interfaces.WikiProvenanceService)
	page := &types.WikiPage{
		TenantID: 1, KnowledgeBaseID: "kb-provenance-metadata", Slug: "concept/metadata",
		Title: "Original title", PageType: types.WikiPageTypeConcept, Status: types.WikiPageStatusPublished,
		Content: "# Stable body\n\nA sourced fact.\n",
	}
	set := &types.WikiPageBlockSet{
		RenderedContent: page.Content,
		Blocks: []*types.WikiPageBlock{
			sourcedWikiTestBlock(types.WikiBlockTypeHeading, "# Stable body\n\n", "heading", "", ""),
			sourcedWikiTestBlock(types.WikiBlockTypeParagraph, "A sourced fact.\n", "fact", "knowledge-a", "chunk-a"),
		},
	}
	createWikiProvenanceTestChunksForSet(t, db, page.TenantID, page.KnowledgeBaseID, set)
	created, err := provenance.SavePageWithProvenance(ctx, page, set)
	require.NoError(t, err)

	metadataEdit := *created
	metadataEdit.Title = "Renamed title"
	updated, err := baseService.UpdatePage(types.WithWikiEditSource(ctx, types.WikiEditSourceUser), &metadataEdit)
	require.NoError(t, err)
	require.Equal(t, created.Version+1, updated.Version)
	require.Equal(t, created.CurrentBlockSetID, updated.CurrentBlockSetID)

	detail, err := provenance.GetPageWithSources(ctx, page.KnowledgeBaseID, page.Slug)
	require.NoError(t, err)
	require.Equal(t, "Renamed title", detail.Title)
	require.Len(t, detail.Blocks, 2)
}

func TestRemoveKnowledgeFromStructuredPageDeletesSourcedParagraphs(t *testing.T) {
	ctx, baseService, db := newWikiProvenanceServiceTest(t)
	provenance := baseService.(interfaces.WikiProvenanceService)
	page := &types.WikiPage{
		TenantID: 1, KnowledgeBaseID: "kb-provenance-delete", Slug: "entity/shared",
		Title: "Shared", PageType: types.WikiPageTypeEntity, Status: types.WikiPageStatusPublished,
		Content: "# Shared\n\nFact A.\n\nFact B.\n", Summary: "Summary from A",
	}
	set := &types.WikiPageBlockSet{
		RenderedContent: page.Content,
		RenderedSummary: page.Summary,
		Blocks: []*types.WikiPageBlock{
			sourcedWikiTestBlock(types.WikiBlockTypeSummary, page.Summary, "summary", "knowledge-a", "chunk-summary"),
			sourcedWikiTestBlock(types.WikiBlockTypeHeading, "# Shared\n\n", "heading", "", ""),
			sourcedWikiTestBlock(types.WikiBlockTypeParagraph, "Fact A.\n\n", "fact-a", "knowledge-a", "chunk-a"),
			sourcedWikiTestBlock(types.WikiBlockTypeParagraph, "Fact B.\n", "fact-b", "knowledge-b", "chunk-b"),
		},
	}
	// One fully-covered paragraph deliberately has evidence from both
	// documents. The authored Markdown block remains the deletion unit, so
	// deleting either contributor removes the whole mixed-source paragraph.
	set.Blocks[2].Sources = append(
		set.Blocks[2].Sources,
		sourcedWikiTestBlock(
			types.WikiBlockTypeParagraph, "Fact A.\n\n", "unused",
			"knowledge-b", "chunk-a-from-b",
		).Sources[0],
	)
	createWikiProvenanceTestChunksForSet(t, db, page.TenantID, page.KnowledgeBaseID, set)
	_, err := provenance.SavePageWithProvenance(ctx, page, set)
	require.NoError(t, err)

	handled, deleted, err := provenance.RemoveKnowledgeFromPage(ctx, page.KnowledgeBaseID, page.Slug, "knowledge-a")
	require.NoError(t, err)
	require.True(t, handled)
	require.False(t, deleted)
	detail, err := provenance.GetPageWithSources(ctx, page.KnowledgeBaseID, page.Slug)
	require.NoError(t, err)
	require.Equal(t, "# Shared\n\nFact B.\n", detail.Content)
	require.Empty(t, detail.Summary)
	require.Equal(t, types.StringArray{"knowledge-b"}, detail.SourceRefs)
	for _, block := range detail.Blocks {
		for _, source := range block.Sources {
			require.NotEqual(t, "knowledge-a", source.KnowledgeID)
		}
	}

	handled, deleted, err = provenance.RemoveKnowledgeFromPage(ctx, page.KnowledgeBaseID, page.Slug, "knowledge-b")
	require.NoError(t, err)
	require.True(t, handled)
	require.True(t, deleted)
	_, err = baseService.GetPageBySlug(ctx, page.KnowledgeBaseID, page.Slug)
	require.Error(t, err)
}

func TestManualSummaryEditAndGenericCreateCannotKeepProvenancePointer(t *testing.T) {
	ctx, baseService, db := newWikiProvenanceServiceTest(t)
	provenance := baseService.(interfaces.WikiProvenanceService)
	page := &types.WikiPage{
		TenantID: 1, KnowledgeBaseID: "kb-provenance-summary-edit", Slug: "concept/summary-edit",
		Title: "Summary edit", PageType: types.WikiPageTypeConcept, Status: types.WikiPageStatusPublished,
		Content: "# Summary edit\n\nA sourced fact.\n", Summary: "Original summary",
	}
	set := &types.WikiPageBlockSet{
		RenderedContent: page.Content,
		RenderedSummary: page.Summary,
		Blocks: []*types.WikiPageBlock{
			sourcedWikiTestBlock(types.WikiBlockTypeSummary, page.Summary, "summary", "knowledge-a", "chunk-summary"),
			sourcedWikiTestBlock(types.WikiBlockTypeHeading, "# Summary edit\n\n", "heading", "", ""),
			sourcedWikiTestBlock(types.WikiBlockTypeParagraph, "A sourced fact.\n", "fact", "knowledge-a", "chunk-a"),
		},
	}
	createWikiProvenanceTestChunksForSet(t, db, page.TenantID, page.KnowledgeBaseID, set)
	created, err := provenance.SavePageWithProvenance(ctx, page, set)
	require.NoError(t, err)
	require.NotEmpty(t, created.CurrentBlockSetID)

	manual := *created
	manual.Summary = "A person rewrote only the summary."
	updated, err := baseService.UpdatePage(types.WithWikiEditSource(ctx, types.WikiEditSourceUser), &manual)
	require.NoError(t, err)
	require.NotEmpty(t, updated.CurrentBlockSetID)
	require.NotEqual(t, created.CurrentBlockSetID, updated.CurrentBlockSetID)
	require.Equal(t, types.StringArray{"knowledge-a"}, updated.SourceRefs)
	detail, err := provenance.GetPageWithSources(ctx, page.KnowledgeBaseID, page.Slug)
	require.NoError(t, err)
	require.Len(t, detail.Blocks, 3)
	require.Equal(t, types.WikiEditSourceUser, detail.Blocks[0].AuthorType)
	require.Empty(t, detail.Blocks[0].Sources)
	require.Equal(t, types.WikiBlockProvenanceVerified, detail.Blocks[2].ProvenanceStatus)
	require.NotEmpty(t, detail.Blocks[2].Sources)

	generic, err := baseService.CreatePage(ctx, &types.WikiPage{
		TenantID: 1, KnowledgeBaseID: page.KnowledgeBaseID, Slug: "concept/untrusted-pointer",
		Title: "Generic", Content: "Body", CurrentBlockSetID: "client-controlled-set-id",
	})
	require.NoError(t, err)
	require.Empty(t, generic.CurrentBlockSetID, "generic CRUD must ignore client-controlled provenance pointers")
}

func TestGetPageWithSourcesMarksDisabledChunkInvalid(t *testing.T) {
	ctx, baseService, db := newWikiProvenanceServiceTest(t)
	provenance := baseService.(interfaces.WikiProvenanceService)
	const (
		kbID        = "kb-provenance-freshness"
		knowledgeID = "knowledge-a"
		chunkID     = "chunk-a"
	)
	createWikiProvenanceTestChunk(t, db, 1, kbID, knowledgeID, chunkID)
	page := &types.WikiPage{
		TenantID: 1, KnowledgeBaseID: kbID, Slug: "concept/freshness",
		Title: "Freshness", PageType: types.WikiPageTypeConcept, Status: types.WikiPageStatusPublished,
		Content: "# Freshness\n\nA sourced fact.\n",
	}
	set := &types.WikiPageBlockSet{
		RenderedContent: page.Content,
		Blocks: []*types.WikiPageBlock{
			sourcedWikiTestBlock(types.WikiBlockTypeHeading, "# Freshness\n\n", "heading", "", ""),
			sourcedWikiTestBlock(types.WikiBlockTypeParagraph, "A sourced fact.\n", "fact", knowledgeID, chunkID),
		},
	}
	createWikiProvenanceTestChunksForSet(t, db, page.TenantID, page.KnowledgeBaseID, set)
	_, err := provenance.SavePageWithProvenance(ctx, page, set)
	require.NoError(t, err)

	detail, err := provenance.GetPageWithSources(ctx, kbID, page.Slug)
	require.NoError(t, err)
	require.Equal(t, types.WikiSourceValidationLocated, detail.Blocks[1].Sources[0].ValidationStatus)

	require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", chunkID).Update("is_enabled", false).Error)
	detail, err = provenance.GetPageWithSources(ctx, kbID, page.Slug)
	require.NoError(t, err)
	require.Equal(t, types.WikiSourceValidationInvalid, detail.Blocks[1].Sources[0].ValidationStatus)
	require.Equal(t, types.WikiBlockProvenanceUnsupported, detail.Blocks[1].ProvenanceStatus)
}

func TestStructuredRevertRejectsStaleSourceEvidence(t *testing.T) {
	ctx, baseService, db := newWikiProvenanceServiceTest(t)
	provenance := baseService.(interfaces.WikiProvenanceService)
	const (
		kbID        = "kb-provenance-revert"
		knowledgeID = "knowledge-a"
		chunkID     = "chunk-a"
	)
	createWikiProvenanceTestChunk(t, db, 1, kbID, knowledgeID, chunkID)
	page := &types.WikiPage{
		TenantID: 1, KnowledgeBaseID: kbID, Slug: "concept/revert",
		Title: "Revert", PageType: types.WikiPageTypeConcept, Status: types.WikiPageStatusPublished,
		Content: "# Revert\n\nOriginal sourced fact.\n",
	}
	set := &types.WikiPageBlockSet{
		RenderedContent: page.Content,
		Blocks: []*types.WikiPageBlock{
			sourcedWikiTestBlock(types.WikiBlockTypeHeading, "# Revert\n\n", "heading", "", ""),
			sourcedWikiTestBlock(types.WikiBlockTypeParagraph, "Original sourced fact.\n", "fact", knowledgeID, chunkID),
		},
	}
	createWikiProvenanceTestChunksForSet(t, db, page.TenantID, page.KnowledgeBaseID, set)
	created, err := provenance.SavePageWithProvenance(ctx, page, set)
	require.NoError(t, err)

	manual := *created
	manual.Content = "# Revert\n\nCurrent manual text.\n"
	current, err := baseService.UpdatePage(types.WithWikiEditSource(ctx, types.WikiEditSourceUser), &manual)
	require.NoError(t, err)
	require.Equal(t, 2, current.Version)
	require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", chunkID).Update("is_enabled", false).Error)

	_, err = baseService.RevertPageToVersion(ctx, kbID, page.Slug, 1)
	require.ErrorIs(t, err, ErrWikiRevertSourcesStale)
}

func TestRemoveLastBodyBlockDeletesPageEvenWhenSummaryHasAnotherSource(t *testing.T) {
	ctx, baseService, db := newWikiProvenanceServiceTest(t)
	provenance := baseService.(interfaces.WikiProvenanceService)
	page := &types.WikiPage{
		TenantID: 1, KnowledgeBaseID: "kb-provenance-summary-only", Slug: "entity/summary-only",
		Title: "Summary only", PageType: types.WikiPageTypeEntity, Status: types.WikiPageStatusPublished,
		Content: "# Summary only\n\nOnly body fact.\n", Summary: "Summary from another file",
	}
	set := &types.WikiPageBlockSet{
		RenderedContent: page.Content,
		RenderedSummary: page.Summary,
		Blocks: []*types.WikiPageBlock{
			sourcedWikiTestBlock(types.WikiBlockTypeSummary, page.Summary, "summary", "knowledge-b", "chunk-summary-b"),
			sourcedWikiTestBlock(types.WikiBlockTypeHeading, "# Summary only\n\n", "heading", "", ""),
			sourcedWikiTestBlock(types.WikiBlockTypeParagraph, "Only body fact.\n", "fact", "knowledge-a", "chunk-a"),
		},
	}
	createWikiProvenanceTestChunksForSet(t, db, page.TenantID, page.KnowledgeBaseID, set)
	_, err := provenance.SavePageWithProvenance(ctx, page, set)
	require.NoError(t, err)

	handled, deleted, err := provenance.RemoveKnowledgeFromPage(ctx, page.KnowledgeBaseID, page.Slug, "knowledge-a")
	require.NoError(t, err)
	require.True(t, handled)
	require.True(t, deleted)
}
