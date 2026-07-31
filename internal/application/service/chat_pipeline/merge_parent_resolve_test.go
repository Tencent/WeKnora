package chatpipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestResolveParentChunksUsesCurrentContentAndImageURLs(t *testing.T) {
	parent := &types.Chunk{
		ID: "parent", ChunkType: types.ChunkTypeParentText, ChunkIndex: 7,
		Content: "manually inserted prefix\n\n![one](u1)\n\nparent body\n\n![two](u2)",
	}
	imageInfo, err := json.Marshal([]types.ImageInfo{{URL: "u2", OCRText: "two"}})
	if err != nil {
		t.Fatal(err)
	}
	repo := &expandChunkRepo{
		chunks: map[string]*types.Chunk{"parent": parent},
		children: map[string][]*types.Chunk{
			"child": {
				{
					ID: "image", ParentChunkID: "child", ChunkType: types.ChunkTypeImageOCR,
					ImageInfo: string(imageInfo),
				},
			},
		},
	}
	plugin := &PluginMerge{chunkRepo: repo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	result := &types.SearchResult{
		ID: "child", KnowledgeID: "doc", ChunkType: string(types.ChunkTypeText),
		ParentChunkID: "parent", Content: "current edited child body", StartAt: 999, EndAt: 1001,
	}

	got := plugin.resolveParentChunks(ctx, &types.ChatManage{}, []*types.SearchResult{result})
	if len(got) != 1 {
		t.Fatalf("result count = %d, want 1", len(got))
	}
	if !strings.Contains(got[0].Content, "current edited child body") {
		t.Fatalf("edited child content was lost: %q", got[0].Content)
	}
	if strings.Contains(got[0].Content, "u1") || !strings.Contains(got[0].Content, "u2") {
		t.Fatalf("parent images were not scoped by URL: %q", got[0].Content)
	}
	if got[0].StartAt != 999 || got[0].EndAt != 1001 {
		t.Fatalf("source coordinates changed to [%d,%d)", got[0].StartAt, got[0].EndAt)
	}
}

func TestResolveImageChunkKeepsGrandparentContextWithoutCoordinateSlicing(t *testing.T) {
	imageInfo, err := json.Marshal([]types.ImageInfo{{URL: "u1", OCRText: "matched image"}})
	if err != nil {
		t.Fatal(err)
	}
	repo := &expandChunkRepo{
		chunks: map[string]*types.Chunk{
			"text": {
				ID: "text", ParentChunkID: "parent", ChunkType: types.ChunkTypeText, ChunkIndex: 4,
				Content: "current edited text child\n\n![matched](u1)", StartAt: 900, EndAt: 910,
			},
			"parent": {
				ID: "parent", ChunkType: types.ChunkTypeParentText,
				Content: "grandparent context before\n\n![matched](u1)\n\ngrandparent context after\n\n![sibling](u2)",
				StartAt: 0, EndAt: 100,
			},
		},
		children: map[string][]*types.Chunk{
			"text": {{
				ID: "image", ParentChunkID: "text", ChunkType: types.ChunkTypeImageOCR,
				ImageInfo: string(imageInfo),
			}},
		},
	}
	plugin := &PluginMerge{chunkRepo: repo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	result := &types.SearchResult{
		ID: "image", KnowledgeID: "doc", ChunkType: string(types.ChunkTypeImageOCR),
		ParentChunkID: "text", Content: "matched image", ImageInfo: string(imageInfo),
		StartAt: 500, EndAt: 510,
	}

	got := plugin.resolveParentChunks(ctx, &types.ChatManage{}, []*types.SearchResult{result})
	if len(got) != 1 {
		t.Fatalf("result count = %d, want 1", len(got))
	}
	for _, want := range []string{"grandparent context before", "grandparent context after", "current edited text child", "u1"} {
		if !strings.Contains(got[0].Content, want) {
			t.Fatalf("image result lost %q: %q", want, got[0].Content)
		}
	}
	if strings.Contains(got[0].Content, "u2") {
		t.Fatalf("sibling image leaked into image result: %q", got[0].Content)
	}
	if got[0].StartAt != 500 || got[0].EndAt != 510 {
		t.Fatalf("source coordinates changed to [%d,%d)", got[0].StartAt, got[0].EndAt)
	}
}

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
