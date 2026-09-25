package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestSortChunksForSummaryUsesChunkIndexAfterEdits(t *testing.T) {
	t.Parallel()
	chunks := []*types.Chunk{
		{ID: "b", ChunkIndex: 2, StartAt: 0, Content: "second"},
		{ID: "a", ChunkIndex: 1, StartAt: 100, Content: "first", ContentRevision: 1},
	}

	sorted := sortChunksForSummary(chunks)

	if len(sorted) != 2 || sorted[0].ID != "a" || sorted[1].ID != "b" {
		t.Fatalf("edited chunks should sort by chunk index, got %#v", sorted)
	}
}

func TestSortChunksForSummaryUsesStartAtBeforeEdits(t *testing.T) {
	t.Parallel()
	chunks := []*types.Chunk{
		{ID: "later", ChunkIndex: 2, StartAt: 50, Content: "later"},
		{ID: "earlier", ChunkIndex: 1, StartAt: 0, Content: "earlier"},
	}

	sorted := sortChunksForSummary(chunks)

	if len(sorted) != 2 || sorted[0].ID != "earlier" || sorted[1].ID != "later" {
		t.Fatalf("unedited chunks should sort by start offset, got %#v", sorted)
	}
}
