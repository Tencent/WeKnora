package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func wikiContributionTestUpdate(slug, details string) SlugUpdate {
	return SlugUpdate{
		Slug:        slug,
		Type:        types.WikiPageTypeEntity,
		DocTitle:    "Doc",
		KnowledgeID: "kid-1",
		SourceRef:   "kid-1",
		Language:    "Chinese",
		Item: extractedItem{
			Name:         "Entity",
			Description:  "Description",
			Details:      details,
			Aliases:      []string{"B", "A"},
			SourceChunks: []string{"c2", "c1"},
		},
		SourceChunks: []string{"c2", "c1"},
		DocSummary:   "Document summary",
	}
}

func TestWikiContributionFingerprintIsStable(t *testing.T) {
	first := wikiContributionTestUpdate("entity/a", "same   details")
	second := wikiContributionTestUpdate("entity/a", "same details")
	second.Item.Aliases = []string{"A", "B"}
	second.SourceChunks = []string{"c1", "c2"}

	assert.Equal(t, wikiContributionFingerprint(first), wikiContributionFingerprint(second))
	second.Item.Details = "changed"
	assert.NotEqual(t, wikiContributionFingerprint(first), wikiContributionFingerprint(second))
}

func TestFilterUnchangedWikiContributionsKeepsOnlyChangedSlugs(t *testing.T) {
	unchanged := wikiContributionTestUpdate("entity/a", "same")
	changed := wikiContributionTestUpdate("entity/b", "new")
	retractA := SlugUpdate{Slug: "entity/a", Type: "retract", KnowledgeID: "kid-1"}
	retractB := SlugUpdate{Slug: "entity/b", Type: "retract", KnowledgeID: "kid-1"}
	updates := []SlugUpdate{unchanged, changed, retractA, retractB}

	oldManifest := buildWikiContributionManifest([]SlugUpdate{
		unchanged,
		wikiContributionTestUpdate("entity/b", "old"),
	})
	newManifest := buildWikiContributionManifest([]SlugUpdate{unchanged, changed})

	filtered, changedSlugs := filterUnchangedWikiContributions(updates, oldManifest, newManifest, true)
	require.Len(t, filtered, 2)
	assert.Equal(t, []string{"entity/b"}, changedSlugs)
	for _, update := range filtered {
		assert.Equal(t, "entity/b", update.Slug)
	}
}

func TestFilterUnchangedWikiContributionsKeepsRemovedSlugRetraction(t *testing.T) {
	oldUpdate := wikiContributionTestUpdate("entity/removed", "old")
	oldManifest := buildWikiContributionManifest([]SlugUpdate{oldUpdate})
	updates := []SlugUpdate{{Slug: "entity/removed", Type: "retractStale", KnowledgeID: "kid-1"}}

	filtered, changedSlugs := filterUnchangedWikiContributions(updates, oldManifest, map[string]string{}, true)

	require.Len(t, filtered, 1)
	assert.Equal(t, "retractStale", filtered[0].Type)
	assert.Equal(t, []string{"entity/removed"}, changedSlugs)
}

func TestFilterUnchangedWikiContributionsWithoutPriorStateIsConservative(t *testing.T) {
	update := wikiContributionTestUpdate("entity/a", "same")
	manifest := buildWikiContributionManifest([]SlugUpdate{update})

	filtered, changedSlugs := filterUnchangedWikiContributions([]SlugUpdate{update}, nil, manifest, false)

	require.Len(t, filtered, 1)
	assert.Equal(t, []string{"entity/a"}, changedSlugs)
}

func TestRebuildConfigFingerprintExcludesEmbeddingAndVLM(t *testing.T) {
	baseKB := &types.KnowledgeBase{EmbeddingModelID: "embedding-a", SummaryModelID: "summary-a"}
	baseEff := types.EffectiveProcessConfig{EnableMultimodel: true, VLMConfig: types.VLMConfig{Enabled: true, ModelID: "vlm-a"}}

	changedEmbedding := *baseKB
	changedEmbedding.EmbeddingModelID = "embedding-b"
	changedVLM := baseEff
	changedVLM.VLMConfig.ModelID = "vlm-b"

	base := rebuildConfigFingerprint(baseKB, baseEff, nil, &baseKB.EmbeddingModelID)
	assert.Equal(t, base, rebuildConfigFingerprint(&changedEmbedding, baseEff, nil, nil))
	assert.Equal(t, base, rebuildConfigFingerprint(baseKB, changedVLM, nil, nil))
	changedSummary := *baseKB
	changedSummary.SummaryModelID = "summary-b"
	assert.NotEqual(t, base, rebuildConfigFingerprint(&changedSummary, baseEff, nil, nil))
	changedQuestion := baseEff
	changedQuestion.QuestionGenerationConfig.QuestionCount = 5
	assert.NotEqual(t, base, rebuildConfigFingerprint(baseKB, changedQuestion, nil, nil))
}

func TestWikiContributionStateRoundTripIncludingEmptyManifest(t *testing.T) {
	ctx := context.Background()
	cache := newMemoryProcessingCache()
	svc := &wikiIngestService{cacheRepo: cache}
	manifest := map[string]string{"entity/a": "fp-a"}

	svc.putWikiContributionState(ctx, 7, "kb-1", "kid-1", manifest)
	got, found := svc.getWikiContributionState(ctx, 7, "kb-1", "kid-1")
	assert.True(t, found)
	assert.Equal(t, manifest, got)

	svc.putWikiContributionState(ctx, 7, "kb-1", "kid-1", map[string]string{})
	got, found = svc.getWikiContributionState(ctx, 7, "kb-1", "kid-1")
	assert.True(t, found)
	assert.Empty(t, got)
}
