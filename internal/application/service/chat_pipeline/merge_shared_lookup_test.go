package chatpipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// Hits from an org-shared KB belong to the sharing workspace (tenant 2)
// while the caller is tenant 1. The merge stage must still fill FAQ answers
// and expand neighbors for them, as parent resolution already does.

func TestPopulateFAQAnswersResolvesSharedKBChunks(t *testing.T) {
	faq := &types.Chunk{ID: "faq", TenantID: 2, ChunkType: types.ChunkTypeFAQ, Content: "How do refunds work?"}
	if err := faq.SetFAQMetadata(&types.FAQChunkMetadata{
		StandardQuestion: "How do refunds work?",
		Answers:          []string{"Refunds are issued within 7 days."},
	}); err != nil {
		t.Fatal(err)
	}
	repo := &tenantScopedChunkRepo{expandChunkRepo: &expandChunkRepo{
		chunks: map[string]*types.Chunk{"faq": faq},
	}}
	plugin := &PluginMerge{chunkRepo: repo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	result := &types.SearchResult{ID: "faq", ChunkType: string(types.ChunkTypeFAQ), Content: faq.Content}

	got := plugin.populateFAQAnswers(ctx, []*types.SearchResult{result})
	if !strings.Contains(got[0].Content, "Refunds are issued within 7 days.") {
		t.Fatalf("shared FAQ answer was not filled in: %q", got[0].Content)
	}
}

func TestExpandShortContextResolvesSharedKBNeighbors(t *testing.T) {
	repo := &tenantScopedChunkRepo{expandChunkRepo: &expandChunkRepo{chunks: map[string]*types.Chunk{
		"prev": {
			ID: "prev", TenantID: 2, KnowledgeID: "doc", ChunkType: types.ChunkTypeText,
			Content: "shared previous body", NextChunkID: "base",
		},
		"base": {
			ID: "base", TenantID: 2, KnowledgeID: "doc", ChunkType: types.ChunkTypeText,
			Content: "shared base body", PreChunkID: "prev", NextChunkID: "next",
		},
		"next": {
			ID: "next", TenantID: 2, KnowledgeID: "doc", ChunkType: types.ChunkTypeText,
			Content: "shared next body", PreChunkID: "base",
		},
	}}}
	plugin := &PluginMerge{chunkRepo: repo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	result := &types.SearchResult{
		ID: "base", KnowledgeID: "doc", ChunkType: string(types.ChunkTypeText), Content: "shared base body",
	}

	got := plugin.expandShortContextWithNeighbors(ctx, []*types.SearchResult{result})
	for _, want := range []string{"shared previous body", "shared base body", "shared next body"} {
		if !strings.Contains(got[0].Content, want) {
			t.Fatalf("shared neighbor expansion missing %q: %q", want, got[0].Content)
		}
	}
}

func TestExpandShortContextKeepsHitWhenNeighborIsLong(t *testing.T) {
	longPrev := strings.Repeat("p", 2000)
	longNext := strings.Repeat("n", 2000)
	repo := &expandChunkRepo{chunks: map[string]*types.Chunk{
		"prev": {
			ID: "prev", KnowledgeID: "doc", ChunkType: types.ChunkTypeText,
			Content: longPrev, NextChunkID: "base",
		},
		"base": {
			ID: "base", KnowledgeID: "doc", ChunkType: types.ChunkTypeText,
			Content: "the matched heading", PreChunkID: "prev", NextChunkID: "next",
		},
		"next": {ID: "next", KnowledgeID: "doc", ChunkType: types.ChunkTypeText, Content: longNext, PreChunkID: "base"},
	}}
	plugin := &PluginMerge{chunkRepo: repo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	result := &types.SearchResult{
		ID: "base", KnowledgeID: "doc", ChunkType: string(types.ChunkTypeText), Content: "the matched heading",
	}

	got := plugin.expandShortContextWithNeighbors(ctx, []*types.SearchResult{result})
	content := got[0].Content
	if !strings.Contains(content, "the matched heading") {
		t.Fatalf("the retrieval hit was cut out of the expanded context: %d runes", runeLen(content))
	}
	if !strings.Contains(content, "p\n\nthe matched heading\n\nn") {
		t.Fatalf("expanded context should keep the text adjacent to the hit: %q...", content[:40])
	}
	if n := runeLen(content); n > 850+4 {
		t.Fatalf("expanded context has %d runes, want about 850", n)
	}
}

func TestMergeOrderedContentGivesUnusedBudgetToOtherSide(t *testing.T) {
	got := mergeOrderedContent("short prev", "base", strings.Repeat("n", 100), 60)
	if !strings.HasPrefix(got, "short prev\n\nbase\n\n") {
		t.Fatalf("short prev should be kept whole: %q", got)
	}
	// 60 - len("base") = 56; prev uses 10, so next gets the remaining 46.
	if n := strings.Count(got, "n"); n != 46 {
		t.Fatalf("next kept %d runes, want 46", n)
	}
}
