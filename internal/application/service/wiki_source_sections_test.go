package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestWikiSourceUpdateKeepsOtherSourcesAndManualText(t *testing.T) {
	ctx := context.Background()
	const original = `<!-- weknora-wiki-sources -->
# PMP

Keep this editorial note.

<!-- weknora-source {"k-row-guard":["chunk-old"]} -->
The old renewal period is one year.
<!-- /weknora-source -->

<!-- weknora-source {"k-row-guard":[],"other-source":["chunk-shared"]} -->
Both sources require certification.
<!-- /weknora-source -->
`
	pageService, page := newWikiWriteGuardStore(t, original)
	page.SourceRefs = types.StringArray{"k-row-guard|Renewal", "other-source|Handbook"}
	page.ChunkRefs = types.StringArray{"chunk-old", "chunk-shared"}
	require.NoError(t, setWikiSourceSectionMode(page, true))
	require.NoError(t, pageService.UpdatePageMeta(ctx, page))

	service := &wikiIngestService{wikiService: pageService, knowledgeSvc: liveKnowledgeStub{}}
	model := &templateCaptureChatModel{response: "SUMMARY: PMP\n\nPage rewritten."}
	batch := &WikiBatchContext{
		SlugTitleMany:               func(context.Context, []string) map[string]string { return nil },
		SummaryContentByKnowledgeID: func(context.Context, string) string { return "Other source summary." },
	}
	updates := []SlugUpdate{
		{Slug: page.Slug, Type: "retract", KnowledgeID: "k-row-guard"},
		{
			Slug: page.Slug, Type: types.WikiPageTypeEntity, KnowledgeID: "k-row-guard",
			SourceRef: "k-row-guard|Renewal", Item: extractedItem{Details: "The new renewal period is two years."},
		},
	}

	for range 2 {
		_, _, _, deferred, err := service.reduceSlugUpdates(ctx, model, page.KnowledgeBaseID,
			page.Slug, updates, page.TenantID, batch, nil)
		require.NoError(t, err)
		require.False(t, deferred)
	}
	stored, err := pageService.GetPageBySlug(ctx, page.KnowledgeBaseID, page.Slug)
	require.NoError(t, err)
	require.Empty(t, model.messages, "the reducer must use the source-scoped extraction")
	require.Contains(t, stored.Content, "Keep this editorial note.")
	require.Contains(t, stored.Content, "Both sources require certification.")
	require.Contains(t, stored.Content, "The new renewal period is two years.")
	require.NotContains(t, stored.Content, "The old renewal period is one year.")
	require.Equal(t, page.Version+1, stored.Version, "retry must not create a new revision")
	require.Equal(t, types.StringArray{"chunk-shared"}, stored.ChunkRefs)

	_, _, _, deferred, err := service.reduceSlugUpdates(ctx, model, page.KnowledgeBaseID,
		page.Slug, []SlugUpdate{updates[0]}, page.TenantID, batch, nil)
	require.NoError(t, err)
	require.False(t, deferred)
	stored, err = pageService.GetPageBySlug(ctx, page.KnowledgeBaseID, page.Slug)
	require.NoError(t, err)
	require.NotContains(t, stored.Content, "The new renewal period is two years.")
	require.Contains(t, stored.Content, "Both sources require certification.")
	require.Contains(t, stored.Content, "Keep this editorial note.")
	require.Equal(t, types.StringArray{"other-source|Handbook"}, stored.SourceRefs)
}

func TestWikiSourceRebuildPreviewsBeforeApplyingAndRejectsStaleBody(t *testing.T) {
	ctx := context.Background()
	const original = "# PMP\n\nOriginal synthesis and notes."
	svc, page := newWikiWriteGuardStore(t, original)
	page.SourceRefs = types.StringArray{"source-a", "source-b"}
	require.NoError(t, svc.UpdatePageMeta(ctx, page))

	summary := "Editorial overview."
	req := types.WikiSourceRebuildRequest{
		Version: page.Version, ManualContent: "# PMP\n\nOriginal editorial note.", ManualSummary: &summary,
		Sections: []types.WikiSourceSection{{
			Content: "Both documents require certification.",
			Sources: map[string][]string{"source-a": nil, "source-b": nil},
		}},
	}
	preview, err := svc.RebuildSourceSections(ctx, page.KnowledgeBaseID, page.Slug, req)
	require.NoError(t, err)
	require.False(t, preview.Applied)
	require.Equal(t, original, preview.OriginalContent)
	require.Contains(t, preview.Content, "Original editorial note.")
	require.Contains(t, preview.Content, "Both documents require certification.")
	stored, err := svc.GetPageBySlug(ctx, page.KnowledgeBaseID, page.Slug)
	require.NoError(t, err)
	require.Equal(t, original, stored.Content)

	req.Apply, req.OriginalContent = true, &preview.OriginalContent
	stored.Content += "\n\nA background link refresh changed this revision."
	require.NoError(t, svc.UpdateAutoLinkedContent(ctx, stored))
	_, err = svc.RebuildSourceSections(ctx, page.KnowledgeBaseID, page.Slug, req)
	require.ErrorIs(t, err, repository.ErrWikiPageConflict)

	req.OriginalContent = &stored.Content
	applied, err := svc.RebuildSourceSections(ctx, page.KnowledgeBaseID, page.Slug, req)
	require.NoError(t, err)
	require.True(t, applied.Applied)
	require.Equal(t, page.Version+1, applied.Version)
	require.Equal(t, preview.Content, applied.Content)
	require.Equal(t, summary, applied.Summary)
	require.ErrorIs(t, svc.UpdateAutoLinkedContent(ctx, stored), repository.ErrWikiPageConflict)

	saved, err := svc.GetPageBySlug(ctx, page.KnowledgeBaseID, page.Slug)
	require.NoError(t, err)
	saved.Content = strings.Replace(saved.Content, "Original editorial note.", "Edited note.", 1)
	saved, err = svc.UpdatePage(ctx, saved)
	require.NoError(t, err)
	require.Contains(t, saved.Content, "Edited note.")
	saved.Content = strings.Replace(saved.Content, "Both documents require certification.", "Replaced evidence.", 1)
	_, err = svc.UpdatePage(ctx, saved)
	require.ErrorIs(t, err, ErrWikiSourceSectionsEdited)

	reverted, err := svc.RevertPageToVersion(ctx, page.KnowledgeBaseID, page.Slug, page.Version)
	require.NoError(t, err)
	require.Equal(t, stored.Content, reverted.Content)
}

func TestWikiSourceDeletionKeepsEditorTextWhenTheLastSourceGoesAway(t *testing.T) {
	ctx := context.Background()
	const body = `<!-- weknora-wiki-sources -->
# PMP

An editor's note.

<!-- weknora-source {"kn-1":["chunk-1"]} -->
Text from the removed document.
<!-- /weknora-source -->
`
	pageService, page := newWikiWriteGuardStore(t, body)
	page.SourceRefs = types.StringArray{"kn-1"}
	page.ChunkRefs = types.StringArray{"chunk-1"}
	require.NoError(t, setWikiSourceSectionMode(page, true))
	require.NoError(t, pageService.UpdatePageMeta(ctx, page))

	knowledgeService, repo, _, _ := newMoveWikiService(t)
	knowledgeService.wikiService = pageService
	repo.pages = []*types.WikiPage{page}
	require.NoError(t, knowledgeService.cleanupWikiReferences(ctx, &types.Knowledge{
		ID: "kn-1", KnowledgeBaseID: page.KnowledgeBaseID,
	}, nil))
	saved, err := pageService.GetPageBySlug(ctx, page.KnowledgeBaseID, page.Slug)
	require.NoError(t, err)
	require.Contains(t, saved.Content, "An editor's note.")
	require.NotContains(t, saved.Content, "Text from the removed document.")
	require.Empty(t, saved.SourceRefs)
	require.Empty(t, saved.ChunkRefs)
}

func TestWikiSourceMarkersAloneDoNotChangeLegacyEditing(t *testing.T) {
	ctx := context.Background()
	const original = wikiSourcePageMarker + wikiSourceOpen + "literal documentation"
	pageService, page := newWikiWriteGuardStore(t, original)
	page.Content = "Ordinary wiki edit"
	saved, err := pageService.UpdatePage(ctx, page)
	require.NoError(t, err)
	require.Equal(t, "Ordinary wiki edit", saved.Content)
	reverted, err := pageService.RevertPageToVersion(ctx, page.KnowledgeBaseID, page.Slug, page.Version)
	require.NoError(t, err)
	require.Equal(t, original, reverted.Content)
}
