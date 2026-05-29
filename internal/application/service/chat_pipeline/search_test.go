package chatpipeline

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestRemovePartialOverlapsKeepsSameKnowledgeChunks(t *testing.T) {
	results := []*types.SearchResult{
		overlapSearchResult("chunk-a", "knowledge-1", 0.92, overlapContent("row10")),
		overlapSearchResult("chunk-b", "knowledge-1", 0.88, overlapContent("row11")),
	}

	deduplicated := removePartialOverlaps(context.Background(), results)

	if len(deduplicated) != 2 {
		t.Fatalf("same-knowledge chunks should be preserved, got %d results", len(deduplicated))
	}
}

func TestRemovePartialOverlapsDropsCrossKnowledgeDuplicates(t *testing.T) {
	results := []*types.SearchResult{
		overlapSearchResult("chunk-a", "knowledge-1", 0.92, overlapContent("row10")),
		overlapSearchResult("chunk-b", "knowledge-2", 0.88, overlapContent("row11")),
	}

	deduplicated := removePartialOverlaps(context.Background(), results)

	if len(deduplicated) != 1 {
		t.Fatalf("cross-knowledge near duplicates should be deduplicated, got %d results", len(deduplicated))
	}
	if deduplicated[0].ID != "chunk-a" {
		t.Fatalf("expected higher-scored chunk to be kept, got %s", deduplicated[0].ID)
	}
}

func overlapSearchResult(id, knowledgeID string, score float64, content string) *types.SearchResult {
	return &types.SearchResult{
		ID:          id,
		KnowledgeID: knowledgeID,
		Content:     content,
		Score:       score,
	}
}

func overlapContent(row string) string {
	return "PFMEA process step station operation function failure mode cause effect severity control detection owner " + row
}
