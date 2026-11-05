package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type wikiReparseChunks struct {
	citedChunkRepo
}

// The ownership query uses PostgreSQL JSONB; the page store in this fixture is SQLite.
type wikiReparsePages struct {
	interfaces.WikiPageService
	slugs []string
}

func (s wikiReparsePages) ListSlugsBySourceRef(context.Context, string, string) ([]string, error) {
	return s.slugs, nil
}

func (wikiReparseChunks) ListChunksByKnowledgeIDAndTypes(
	context.Context, uint64, string, []types.ChunkType,
) ([]*types.Chunk, error) {
	return []*types.Chunk{citationChunk("chunk-1",
		"The PMP certification requires professional project management experience and a qualifying examination.")}, nil
}

// Route by prompt purpose because summary and citation calls run concurrently.
type wikiReparseModel struct {
	templateCaptureChatModel
}

func (*wikiReparseModel) Chat(
	ctx context.Context, _ []chat.Message, _ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	purpose, _ := types.LLMCallMetadataFromContext(ctx)
	var content string
	switch purpose {
	case "wiki_candidate_slug":
		content = `{"entities": [], "concepts": []}`
	case "wiki_summary":
		content = "SUMMARY: Updated certification requirements\n# Certification\n\n" +
			"PMP requires experience and an examination."
	case "wiki_chunk_citation":
		content = `{"citations": {}, "new_slugs": []}`
	default:
		return nil, fmt.Errorf("unexpected prompt purpose: %s", purpose)
	}
	return &types.ChatResponse{Content: content}, nil
}

func TestWikiReparsePreservesPagesOmittedByExtraction(t *testing.T) {
	for _, refs := range []types.StringArray{{"kn-1"}, {"kn-1|Certification", "kn-2"}} {
		t.Run(fmt.Sprintf("sources=%d", len(refs)), func(t *testing.T) {
			ctx := context.Background()
			const body = "# PMP\n\nPMP requires professional project management experience."
			pageSvc, page := newWikiWriteGuardStore(t, body)
			page.SourceRefs = refs
			require.NoError(t, pageSvc.UpdatePageMeta(ctx, page))
			svc := &wikiIngestService{
				wikiService: wikiReparsePages{WikiPageService: pageSvc, slugs: []string{page.Slug}},
				chunkRepo:   &wikiReparseChunks{},
				knowledgeSvc: &wikiKnowledgeLookupStub{lookup: func(context.Context, string) (*types.Knowledge, error) {
					return &types.Knowledge{ID: "kn-1", Title: "Certification"}, nil
				}},
			}
			batchCtx := &WikiBatchContext{
				SummaryContentByKnowledgeID: func(context.Context, string) string { return body },
				SlugTitleMany:               func(context.Context, []string) map[string]string { return nil },
			}
			result, updates, err := svc.mapOneDocument(ctx,
				&wikiReparseModel{},
				WikiIngestPayload{TenantID: 1, KnowledgeBaseID: page.KnowledgeBaseID},
				WikiPendingOp{KnowledgeID: "kn-1", Op: WikiOpIngest}, batchCtx)
			require.NoError(t, err)
			require.NotNil(t, result)
			for _, update := range updates {
				require.NotEqual(t, page.Slug, update.Slug,
					"missing extraction is not evidence that the source was deleted")
			}
			require.Len(t, updates, 1, "the document summary should still be refreshed")
			require.Equal(t, types.WikiPageTypeSummary, updates[0].Type)

			after, err := pageSvc.GetPageBySlug(ctx, page.KnowledgeBaseID, page.Slug)
			require.NoError(t, err)
			require.Equal(t, body, after.Content)
			require.Equal(t, refs, after.SourceRefs)
		})
	}
}

func TestWikiReparseSwapPreservesSourceAndDoesNotRequestRemovalNotice(t *testing.T) {
	ctx := context.Background()
	pageSvc, page := newWikiWriteGuardStore(t, "# PMP\n\nPrevious certification requirements.")
	page.SourceRefs = types.StringArray{"kn-1|Certification"}
	require.NoError(t, pageSvc.UpdatePageMeta(ctx, page))
	svc := &wikiIngestService{
		wikiService: pageSvc,
		knowledgeSvc: &wikiKnowledgeLookupStub{lookup: func(context.Context, string) (*types.Knowledge, error) {
			return &types.Knowledge{ID: "kn-1"}, nil
		}},
	}
	const updatedBody = "# PMP\n\nUpdated certification requirements."
	model := &templateCaptureChatModel{response: "SUMMARY: Certification\n" + updatedBody}
	batchCtx := &WikiBatchContext{
		SummaryContentByKnowledgeID: func(context.Context, string) string { return "Other source contribution." },
		SlugTitleMany:               func(context.Context, []string) map[string]string { return nil },
	}
	updates := []SlugUpdate{
		{Slug: page.Slug, Type: "retract", KnowledgeID: "kn-1",
			RetractDocContent: "Previous certification requirements."},
		{Slug: page.Slug, Type: types.WikiPageTypeEntity, KnowledgeID: "kn-1", SourceRef: "kn-1",
			Item: extractedItem{Name: "PMP", Details: "Updated certification requirements."}},
	}
	changed, _, failed, deferred, err := svc.reduceSlugUpdates(
		ctx, model, page.KnowledgeBaseID, page.Slug, updates, 1, batchCtx, nil)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, failed)
	require.False(t, deferred)
	require.Len(t, model.messages, 2)
	prompt := model.messages[1].Content
	require.Contains(t, prompt, "Previous certification requirements.")
	require.Contains(t, prompt, "Updated certification requirements.")
	require.Contains(t, prompt, "(no remaining sources)")
	require.NotContains(t, prompt, "This page's primary source document was removed.")
	after, err := pageSvc.GetPageBySlug(ctx, page.KnowledgeBaseID, page.Slug)
	require.NoError(t, err)
	require.Equal(t, updatedBody, after.Content)
	require.Equal(t, types.StringArray{"kn-1"}, after.SourceRefs)
}

func TestWikiSourceDeletionStillReconcilesPages(t *testing.T) {
	for _, refs := range []types.StringArray{{"kn-1"}, {"kn-1", "kn-2"}} {
		t.Run(fmt.Sprintf("sources=%d", len(refs)), func(t *testing.T) {
			ctx := context.Background()
			pageSvc, page := newWikiWriteGuardStore(t, "# PMP\n\nCertification requirements.")
			page.SourceRefs = refs
			require.NoError(t, pageSvc.UpdatePageMeta(ctx, page))
			svc, wikiRepo, _, _ := newMoveWikiService(t)
			svc.wikiService = pageSvc
			wikiRepo.pages = []*types.WikiPage{page}
			require.NoError(t, svc.cleanupWikiReferences(ctx, &types.Knowledge{
				ID: "kn-1", KnowledgeBaseID: page.KnowledgeBaseID,
			}, nil))
			after, err := pageSvc.GetPageBySlug(ctx, page.KnowledgeBaseID, page.Slug)
			if len(refs) == 1 {
				require.Error(t, err, "deleting the only source must still delete the page")
			} else {
				require.NoError(t, err)
				require.Equal(t, types.StringArray{"kn-2"}, after.SourceRefs)
			}
		})
	}
}
