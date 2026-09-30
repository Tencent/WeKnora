package chatpipeline

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestRemovePartialOverlapsKeepsHighOverlapChunksFromSameKnowledge(t *testing.T) {
	results := []*types.SearchResult{
		{
			ID:          "chunk-a",
			KnowledgeID: "knowledge-1",
			Content:     tableLikeContent("row-alpha station-one"),
			Score:       0.91,
		},
		{
			ID:          "chunk-b",
			KnowledgeID: "knowledge-1",
			Content:     tableLikeContent("row-beta station-two"),
			Score:       0.83,
		},
	}

	got := removePartialOverlaps(context.Background(), results)

	if len(got) != 2 {
		t.Fatalf("expected same-knowledge chunks to be preserved, got %d", len(got))
	}
}

func TestRemovePartialOverlapsDropsHighOverlapChunksFromDifferentKnowledge(t *testing.T) {
	results := []*types.SearchResult{
		{
			ID:          "chunk-a",
			KnowledgeID: "knowledge-1",
			Content:     tableLikeContent("row-alpha station-one"),
			Score:       0.91,
		},
		{
			ID:          "chunk-b",
			KnowledgeID: "knowledge-2",
			Content:     tableLikeContent("row-beta station-two"),
			Score:       0.83,
		},
	}

	got := removePartialOverlaps(context.Background(), results)

	if len(got) != 1 {
		t.Fatalf("expected cross-knowledge overlap to be deduplicated, got %d", len(got))
	}
	if got[0].ID != "chunk-a" {
		t.Fatalf("expected higher-scored chunk to be retained, got %s", got[0].ID)
	}
}

func TestRemoveDuplicateResultsStillDropsExactSameKnowledgeContent(t *testing.T) {
	results := []*types.SearchResult{
		{
			ID:          "chunk-a",
			KnowledgeID: "knowledge-1",
			Content:     "same normalized content",
			Score:       0.91,
		},
		{
			ID:          "chunk-b",
			KnowledgeID: "knowledge-1",
			Content:     "same   normalized\ncontent",
			Score:       0.83,
		},
	}

	got := removeDuplicateResults(results)

	if len(got) != 1 {
		t.Fatalf("expected exact same-knowledge duplicate content to be removed, got %d", len(got))
	}
	if got[0].ID != "chunk-a" {
		t.Fatalf("expected first duplicate content chunk to be retained, got %s", got[0].ID)
	}
}

func TestMergeOverlappingChunksStillMergesSameKnowledgeContainedRanges(t *testing.T) {
	plugin := &PluginMerge{}
	results := []*types.SearchResult{
		{
			ID:          "chunk-a",
			KnowledgeID: "knowledge-1",
			ChunkType:   string(types.ChunkTypeText),
			Content:     "abcdef",
			StartAt:     0,
			EndAt:       6,
			Score:       0.91,
		},
		{
			ID:          "chunk-b",
			KnowledgeID: "knowledge-1",
			ChunkType:   string(types.ChunkTypeText),
			Content:     "cde",
			StartAt:     2,
			EndAt:       5,
			Score:       0.83,
		},
	}

	got := plugin.mergeOverlappingChunks(context.Background(), "knowledge-1", results)

	if len(got) != 1 {
		t.Fatalf("expected contained same-knowledge range to be merged, got %d", len(got))
	}
	if len(got[0].SubChunkID) != 1 || got[0].SubChunkID[0] != "chunk-b" {
		t.Fatalf("expected contained chunk to be tracked as sub chunk, got %#v", got[0].SubChunkID)
	}
}

func tableLikeContent(unique string) string {
	return "process step failure mode cause effect severity occurrence detection control action owner station operation risk material inspection " + unique
}
