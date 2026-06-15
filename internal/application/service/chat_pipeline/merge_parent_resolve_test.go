package chatpipeline

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestCollectScopedTextChildIDs(t *testing.T) {
	parentMap := map[string]*types.Chunk{
		"parent-1": {ID: "parent-1", ChunkType: types.ChunkTypeParentText},
		"text-x":   {ID: "text-x", ChunkType: types.ChunkTypeText},
	}
	results := []*types.SearchResult{
		{ID: "text-1", ChunkType: string(types.ChunkTypeText), ParentChunkID: "parent-1"},
		{ID: "img-1", ChunkType: string(types.ChunkTypeImageOCR), ParentChunkID: "text-2"},
		{ID: "text-3", ChunkType: string(types.ChunkTypeText), ParentChunkID: "text-x"}, // not parent_text
	}
	ids := collectScopedTextChildIDs(results, parentMap)
	if len(ids) != 2 {
		t.Fatalf("ids: %v", ids)
	}
}

func TestAssignScopedImageInfo_FiltersToContentURLs(t *testing.T) {
	all, _ := json.Marshal([]types.ImageInfo{
		{URL: "u1", OCRText: "one"},
		{URL: "u2", OCRText: "two"},
	})
	r := &types.SearchResult{
		Content:   "![p2](u2)",
		ImageInfo: string(all),
	}
	assignScopedImageInfo(r, nil, "missing-child")
	var infos []types.ImageInfo
	if err := json.Unmarshal([]byte(r.ImageInfo), &infos); err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].URL != "u2" {
		t.Fatalf("filtered: %+v", infos)
	}
}

func TestParentChildImageHit_WindowSliceAndFilter(t *testing.T) {
	parentContent := "![p1](u1)\n\n![p2](u2)\n\n![p3](u3)"
	textStart := len([]rune("![p1](u1)\n\n"))
	textEnd := textStart + len([]rune("![p2](u2)"))
	sliced := searchutil.SliceContentByDocumentRange(parentContent, 0, textStart, textEnd)
	if sliced != "![p2](u2)" {
		t.Fatalf("slice: got %q", sliced)
	}

	all, _ := json.Marshal([]types.ImageInfo{
		{URL: "u1"}, {URL: "u2"}, {URL: "u3"},
	})
	filtered := searchutil.FilterImageInfoByContentURLs(sliced, string(all))
	var infos []types.ImageInfo
	if err := json.Unmarshal([]byte(filtered), &infos); err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].URL != "u2" {
		t.Fatalf("infos: %+v", infos)
	}
}
