package searchutil

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// MergeImageInfoJSON iterates a map of per-chunk JSON, and
// collectChildEvidence iterates a map keyed by URL, so without sorting the
// merged array order changed from call to call.
func TestMergeImageInfoJSON_IsDeterministicAndSorted(t *testing.T) {
	perChunk := map[string]string{
		"c1": imageInfoJSON(t, types.ImageInfo{URL: "c.png", Caption: "third"}),
		"c2": imageInfoJSON(t, types.ImageInfo{URL: "a.png", Caption: "first"}),
		"c3": imageInfoJSON(t, types.ImageInfo{URL: "b.png", Caption: "second"}),
	}
	want := imageInfoJSON(t,
		types.ImageInfo{URL: "a.png", Caption: "first"},
		types.ImageInfo{URL: "b.png", Caption: "second"},
		types.ImageInfo{URL: "c.png", Caption: "third"},
	)

	for i := 0; i < 50; i++ {
		if got := MergeImageInfoJSON(perChunk); got != want {
			t.Fatalf("call %d: got %s, want %s", i, got, want)
		}
	}
}

func TestCollectImageInfoByChunkIDs_MergedOrderIsDeterministic(t *testing.T) {
	repo := childrenByParentRepo{byParent: map[string][]*types.Chunk{
		"text-1": {
			imageChild("ocr-1", "text-1", types.ChunkTypeImageOCR, "c.png", true),
			imageChild("ocr-2", "text-1", types.ChunkTypeImageOCR, "a.png", true),
			imageChild("ocr-3", "text-1", types.ChunkTypeImageOCR, "b.png", true),
		},
	}}
	want := imageInfoJSON(t,
		types.ImageInfo{URL: "a.png", Caption: "a screenshot"},
		types.ImageInfo{URL: "b.png", Caption: "a screenshot"},
		types.ImageInfo{URL: "c.png", Caption: "a screenshot"},
	)

	for i := 0; i < 50; i++ {
		got := CollectImageInfoByChunkIDs(context.Background(), repo, 1, []string{"text-1"})
		if got["text-1"] != want {
			t.Fatalf("call %d: got %s, want %s", i, got["text-1"], want)
		}
	}
}
