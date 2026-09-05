package handler

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestChunkTypeFilterDefaultsToAllTypes(t *testing.T) {
	got := chunkTypeFilter(nil)
	all := types.AllChunkTypes()
	if len(got) != len(all) {
		t.Fatalf("chunkTypeFilter(nil) = %#v, want all %d types", got, len(all))
	}
	// Multimodal chunks must be visible by default; a text-only default hid
	// them from the chunks page (#2857).
	for _, want := range []types.ChunkType{
		types.ChunkTypeText,
		types.ChunkTypeImageOCR,
		types.ChunkTypeImageCaption,
		types.ChunkTypeFAQ,
	} {
		found := false
		for _, ct := range got {
			if ct == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("chunkTypeFilter(nil) missing %q: %#v", want, got)
		}
	}
}

func TestChunkTypeFilterRespectsExplicitQuery(t *testing.T) {
	got := chunkTypeFilter([]string{"text", "image_ocr"})
	if len(got) != 2 || got[0] != types.ChunkTypeText || got[1] != types.ChunkTypeImageOCR {
		t.Fatalf("chunkTypeFilter([text image_ocr]) = %#v, want [text image_ocr]", got)
	}
}

func TestAllChunkTypesHasNoDuplicates(t *testing.T) {
	seen := make(map[types.ChunkType]bool)
	for _, ct := range types.AllChunkTypes() {
		if seen[ct] {
			t.Fatalf("AllChunkTypes contains duplicate %q", ct)
		}
		seen[ct] = true
	}
}
