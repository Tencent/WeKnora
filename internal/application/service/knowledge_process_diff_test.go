package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func chunkWithContent(t *testing.T, knowledgeID, content string, stableSeq int) *types.Chunk {
	t.Helper()
	return &types.Chunk{
		ID:          types.StableChunkID(knowledgeID, content, stableSeq),
		KnowledgeID: knowledgeID,
		Content:     content,
	}
}

func TestComputeChunkDiff_AllKept(t *testing.T) {
	knowledgeID := "kb-all-kept"
	newChunks := []*types.Chunk{
		chunkWithContent(t, knowledgeID, "first paragraph of text", 0),
		chunkWithContent(t, knowledgeID, "second paragraph of text", 0),
	}
	oldChunks := []*types.Chunk{
		chunkWithContent(t, knowledgeID, "first paragraph of text", 0),
		chunkWithContent(t, knowledgeID, "second paragraph of text", 0),
	}

	kept, added, removed := computeChunkDiff(newChunks, oldChunks)

	if len(kept) != 2 {
		t.Fatalf("kept = %d, want 2", len(kept))
	}
	if len(added) != 0 {
		t.Fatalf("added = %d, want 0", len(added))
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %d, want 0", len(removed))
	}
	for _, c := range newChunks {
		if _, ok := kept[c.ID]; !ok {
			t.Fatalf("expected chunk %q to be kept", c.ID)
		}
	}
}

func TestComputeChunkDiff_AllAdded(t *testing.T) {
	knowledgeID := "kb-all-added"
	newChunks := []*types.Chunk{
		chunkWithContent(t, knowledgeID, "only chunk", 0),
	}
	oldChunks := []*types.Chunk{}

	kept, added, removed := computeChunkDiff(newChunks, oldChunks)

	if len(kept) != 0 {
		t.Fatalf("kept = %d, want 0", len(kept))
	}
	if len(added) != 1 {
		t.Fatalf("added = %d, want 1", len(added))
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %d, want 0", len(removed))
	}
	if added[0].ID != newChunks[0].ID {
		t.Fatalf("added[0].ID = %q, want %q", added[0].ID, newChunks[0].ID)
	}
}

func TestComputeChunkDiff_AllRemoved(t *testing.T) {
	knowledgeID := "kb-all-removed"
	newChunks := []*types.Chunk{}
	oldChunks := []*types.Chunk{
		chunkWithContent(t, knowledgeID, "old chunk one", 0),
		chunkWithContent(t, knowledgeID, "old chunk two", 0),
	}

	kept, added, removed := computeChunkDiff(newChunks, oldChunks)

	if len(kept) != 0 {
		t.Fatalf("kept = %d, want 0", len(kept))
	}
	if len(added) != 0 {
		t.Fatalf("added = %d, want 0", len(added))
	}
	if len(removed) != 2 {
		t.Fatalf("removed = %d, want 2", len(removed))
	}
	wantRemoved := map[string]struct{}{
		oldChunks[0].ID: {},
		oldChunks[1].ID: {},
	}
	for _, id := range removed {
		if _, ok := wantRemoved[id]; !ok {
			t.Fatalf("unexpected removed id %q", id)
		}
	}
}

func TestComputeChunkDiff_PartialChange(t *testing.T) {
	knowledgeID := "kb-partial-change"
	unchanged := chunkWithContent(t, knowledgeID, "unchanged paragraph", 0)
	oldChanged := chunkWithContent(t, knowledgeID, "original paragraph", 0)
	newChanged := chunkWithContent(t, knowledgeID, "revised paragraph", 0)

	newChunks := []*types.Chunk{unchanged, newChanged}
	oldChunks := []*types.Chunk{unchanged, oldChanged}

	kept, added, removed := computeChunkDiff(newChunks, oldChunks)

	if len(kept) != 1 {
		t.Fatalf("kept = %d, want 1", len(kept))
	}
	if _, ok := kept[unchanged.ID]; !ok {
		t.Fatalf("expected unchanged chunk %q to be kept", unchanged.ID)
	}
	if len(added) != 1 {
		t.Fatalf("added = %d, want 1", len(added))
	}
	if added[0].ID != newChanged.ID {
		t.Fatalf("added[0].ID = %q, want %q", added[0].ID, newChanged.ID)
	}
	if len(removed) != 1 {
		t.Fatalf("removed = %d, want 1", len(removed))
	}
	if removed[0] != oldChanged.ID {
		t.Fatalf("removed[0] = %q, want %q", removed[0], oldChanged.ID)
	}
}

func TestComputeChunkDiff_StableIDDeterminism(t *testing.T) {
	knowledgeID := "kb-stable-id"
	content := "same content must produce the same stable chunk id"

	// Simulate two independent splits producing the same content.
	newChunks := []*types.Chunk{
		chunkWithContent(t, knowledgeID, content, 0),
	}
	oldChunks := []*types.Chunk{
		chunkWithContent(t, knowledgeID, content, 0),
	}

	kept, added, removed := computeChunkDiff(newChunks, oldChunks)

	if len(kept) != 1 {
		t.Fatalf("kept = %d, want 1", len(kept))
	}
	if len(added) != 0 {
		t.Fatalf("added = %d, want 0", len(added))
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %d, want 0", len(removed))
	}
	if _, ok := kept[newChunks[0].ID]; !ok {
		t.Fatalf("expected stable id %q to be kept", newChunks[0].ID)
	}
}

func TestComputeChunkDiff_EmptyBoth(t *testing.T) {
	kept, added, removed := computeChunkDiff(nil, nil)

	if len(kept) != 0 {
		t.Fatalf("kept = %d, want 0", len(kept))
	}
	if len(added) != 0 {
		t.Fatalf("added = %d, want 0", len(added))
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %d, want 0", len(removed))
	}
}
