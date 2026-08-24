package searchutil

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// childrenByParentRepo serves chunk children from a static parent → children map.
type childrenByParentRepo struct {
	interfaces.ChunkRepository
	byParent map[string][]*types.Chunk
}

func (r childrenByParentRepo) ListChunksByParentIDs(
	_ context.Context, _ uint64, parentIDs []string,
) ([]*types.Chunk, error) {
	var out []*types.Chunk
	for _, id := range parentIDs {
		out = append(out, r.byParent[id]...)
	}
	return out, nil
}

func imageChild(id, parentID string, chunkType types.ChunkType, url string, enabled bool) *types.Chunk {
	return &types.Chunk{
		ID:            id,
		ParentChunkID: parentID,
		ChunkType:     chunkType,
		IsEnabled:     enabled,
		ImageInfo:     `[{"url":"` + url + `","caption":"a screenshot"}]`,
	}
}

// Deleting an image from a chunk disables its image children. Their image_info
// must not come back through enrichment, or the removed image reappears in
// summaries, tool output and model context.
func TestCollectImageInfoByChunkIDsSkipsDisabledImageChildren(t *testing.T) {
	repo := childrenByParentRepo{byParent: map[string][]*types.Chunk{
		"text-1": {
			imageChild("ocr-1", "text-1", types.ChunkTypeImageOCR, "resource://removed", false),
			imageChild("cap-1", "text-1", types.ChunkTypeImageCaption, "resource://removed", false),
		},
		"text-2": {
			imageChild("ocr-2", "text-2", types.ChunkTypeImageOCR, "resource://kept", true),
		},
	}}

	got := CollectImageInfoByChunkIDs(context.Background(), repo, 1, []string{"text-1", "text-2"})

	if _, ok := got["text-1"]; ok {
		t.Fatalf("disabled image children leaked image_info: %q", got["text-1"])
	}
	if got["text-2"] == "" {
		t.Fatal("enabled image child lost its image_info")
	}
}

// The parent_text → text → image path must apply the same rule at both levels.
func TestCollectImageInfoByChunkIDsSkipsDisabledGrandchildrenAndTextChildren(t *testing.T) {
	repo := childrenByParentRepo{byParent: map[string][]*types.Chunk{
		"parent-1": {
			{ID: "text-1", ParentChunkID: "parent-1", ChunkType: types.ChunkTypeText, IsEnabled: true},
		},
		"parent-2": {
			// Disabling a text chunk hides everything under it.
			{ID: "text-2", ParentChunkID: "parent-2", ChunkType: types.ChunkTypeText, IsEnabled: false},
		},
		"text-1": {
			imageChild("ocr-1", "text-1", types.ChunkTypeImageOCR, "resource://removed", false),
		},
		"text-2": {
			imageChild("ocr-2", "text-2", types.ChunkTypeImageOCR, "resource://hidden", true),
		},
	}}

	got := CollectImageInfoByChunkIDs(context.Background(), repo, 1, []string{"parent-1", "parent-2"})

	if len(got) != 0 {
		t.Fatalf("expected no image_info, got %v", got)
	}
}
