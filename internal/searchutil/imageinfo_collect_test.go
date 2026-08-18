package searchutil

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// stubChunkRepo embeds the ChunkRepository interface so only the method under
// test needs a concrete implementation. CollectImageInfoByChunkIDs only calls
// ListChunksByParentIDs.
type stubChunkRepo struct {
	interfaces.ChunkRepository
	children map[string][]*types.Chunk
}

func (s stubChunkRepo) ListChunksByParentIDs(
	_ context.Context, _ uint64, parentIDs []string,
) ([]*types.Chunk, error) {
	var out []*types.Chunk
	for _, id := range parentIDs {
		out = append(out, s.children[id]...)
	}
	return out, nil
}

func collectImageInfoJSON(t *testing.T, url string) string {
	t.Helper()
	raw, err := json.Marshal([]types.ImageInfo{{URL: url, OCRText: "ocr", Caption: "caption"}})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestCollectImageInfoByChunkIDs_ExcludesDisabledImages(t *testing.T) {
	repo := stubChunkRepo{children: map[string][]*types.Chunk{
		"text-1": {
			{
				ID:            "ocr-enabled",
				ChunkType:     types.ChunkTypeImageOCR,
				ParentChunkID: "text-1",
				ImageInfo:     collectImageInfoJSON(t, "enabled.jpg"),
				IsEnabled:     true,
			},
			{
				ID:            "ocr-disabled",
				ChunkType:     types.ChunkTypeImageOCR,
				ParentChunkID: "text-1",
				ImageInfo:     collectImageInfoJSON(t, "disabled.jpg"),
				IsEnabled:     false,
			},
		},
	}}

	got := CollectImageInfoByChunkIDs(context.Background(), repo, 1, []string{"text-1"})
	if len(got) != 1 {
		t.Fatalf("expected one chunk with merged image info, got %d", len(got))
	}
	raw, ok := got["text-1"]
	if !ok || raw == "" {
		t.Fatal("expected image info for text-1")
	}
	var infos []types.ImageInfo
	if err := json.Unmarshal([]byte(raw), &infos); err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].URL != "enabled.jpg" {
		t.Fatalf("disabled image leaked into merged info: %+v", infos)
	}
}
