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
					IsEnabled: true,
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
				IsEnabled: true,
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

// TestResolveImageOCRHit_KeepsRecognizedTextWhenParentIsPlaceholderOnly:
// force-scanned PDFs produce parent/text chunks cut from image placeholders
// alone, while the whole recognized page text lives in the image_ocr child.
// The parent expansion must not discard that recognized text (#3052).
func TestResolveImageOCRHit_KeepsRecognizedTextWhenParentIsPlaceholderOnly(t *testing.T) {
	ocrText := "客户编码 A01，客户名称 示例公司，联系人 张三，电话 13800000000。"
	imageInfo, err := json.Marshal([]types.ImageInfo{{URL: "images/scan_page_1.jpg", OCRText: ocrText}})
	if err != nil {
		t.Fatal(err)
	}
	repo := &expandChunkRepo{
		chunks: map[string]*types.Chunk{
			"text": {
				ID: "text", ParentChunkID: "parent", ChunkType: types.ChunkTypeText, ChunkIndex: 3,
				Content: "![scan_page_1.jpg](images/scan_page_1.jpg)",
			},
			"parent": {
				ID: "parent", ChunkType: types.ChunkTypeParentText,
				Content: "![scan_page_1.jpg](images/scan_page_1.jpg)",
			},
		},
	}
	plugin := &PluginMerge{chunkRepo: repo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	result := &types.SearchResult{
		ID: "ocr-1", KnowledgeID: "doc", ChunkType: string(types.ChunkTypeImageOCR),
		ParentChunkID: "text", Content: ocrText, ImageInfo: string(imageInfo),
	}

	got := plugin.resolveParentChunks(ctx, &types.ChatManage{}, []*types.SearchResult{result})
	if len(got) != 1 {
		t.Fatalf("result count = %d, want 1", len(got))
	}
	if !strings.Contains(got[0].Content, "示例公司") || !strings.Contains(got[0].Content, "13800000000") {
		t.Fatalf("recognized OCR text was dropped by parent expansion: %q", got[0].Content)
	}
}

// TestResolveImageOCRHit_DoesNotDuplicateRecognizedText: when the surrounding
// text already contains the recognized body (normal multimodal mode), the
// re-attachment must not duplicate it.
func TestResolveImageOCRHit_DoesNotDuplicateRecognizedText(t *testing.T) {
	ocrText := "matched image body text"
	imageInfo, err := json.Marshal([]types.ImageInfo{{URL: "u1", OCRText: ocrText}})
	if err != nil {
		t.Fatal(err)
	}
	repo := &expandChunkRepo{
		chunks: map[string]*types.Chunk{
			"text": {
				ID: "text", ParentChunkID: "parent", ChunkType: types.ChunkTypeText, ChunkIndex: 2,
				Content: "intro\n\nmatched image body text\n\noutro\n\n![matched](u1)",
			},
			"parent": {
				ID: "parent", ChunkType: types.ChunkTypeParentText,
				Content: "long grandparent context\n\nintro\n\n" +
					"matched image body text\n\noutro\n\n![matched](u1)\n\nmore context",
			},
		},
	}
	plugin := &PluginMerge{chunkRepo: repo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	result := &types.SearchResult{
		ID: "ocr-1", KnowledgeID: "doc", ChunkType: string(types.ChunkTypeImageOCR),
		ParentChunkID: "text", Content: ocrText, ImageInfo: string(imageInfo),
	}

	got := plugin.resolveParentChunks(ctx, &types.ChatManage{}, []*types.SearchResult{result})
	if len(got) != 1 {
		t.Fatalf("result count = %d, want 1", len(got))
	}
	if got[0].Content == "" {
		t.Fatal("merged content is empty")
	}
	if strings.Count(got[0].Content, ocrText) != 1 {
		t.Fatalf("recognized text duplicated in merged content: %q", got[0].Content)
	}
}

func TestResolveImageOCRHit_ChatEnrichmentDoesNotDuplicateOCR(t *testing.T) {
	ocrText := "客户编码 A01，客户名称 示例公司，联系人 张三，电话 13800000000。"
	caption := "扫描件第一页客户信息表"
	imageInfo, err := json.Marshal([]types.ImageInfo{{
		URL: "images/scan_page_1.jpg", OCRText: ocrText, Caption: caption,
	}})
	if err != nil {
		t.Fatal(err)
	}
	repo := &expandChunkRepo{
		chunks: map[string]*types.Chunk{
			"text": {
				ID: "text", ParentChunkID: "parent", ChunkType: types.ChunkTypeText, ChunkIndex: 3,
				Content: "![scan_page_1.jpg](images/scan_page_1.jpg)",
			},
			"parent": {
				ID: "parent", ChunkType: types.ChunkTypeParentText,
				Content: "![scan_page_1.jpg](images/scan_page_1.jpg)",
			},
		},
		children: map[string][]*types.Chunk{
			"text": {{
				ID: "ocr-1", ParentChunkID: "text", ChunkType: types.ChunkTypeImageOCR,
				ImageInfo: string(imageInfo), IsEnabled: true,
			}},
		},
	}
	plugin := &PluginMerge{chunkRepo: repo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	result := &types.SearchResult{
		ID: "ocr-1", KnowledgeID: "doc", ChunkType: string(types.ChunkTypeImageOCR),
		ParentChunkID: "text", Content: ocrText, ImageInfo: string(imageInfo),
	}

	got := plugin.resolveParentChunks(ctx, &types.ChatManage{}, []*types.SearchResult{result})
	if len(got) != 1 {
		t.Fatalf("result count = %d, want 1", len(got))
	}
	passage := getEnrichedPassageForChat(ctx, got[0])
	if strings.Count(passage, ocrText) != 1 {
		t.Fatalf("OCR duplicated after chat enrichment: %q", passage)
	}
	if !strings.Contains(passage, caption) {
		t.Fatalf("caption was dropped from chat enrichment: %q", passage)
	}
}

func TestResolveImageCaptionHit_KeepsCaptionWhenParentIsPlaceholderOnly(t *testing.T) {
	caption := "扫描合同首页，含甲乙双方签章位置"
	imageInfo, err := json.Marshal([]types.ImageInfo{{URL: "images/scan_page_1.jpg", Caption: caption}})
	if err != nil {
		t.Fatal(err)
	}
	repo := &expandChunkRepo{
		chunks: map[string]*types.Chunk{
			"text": {
				ID: "text", ParentChunkID: "parent", ChunkType: types.ChunkTypeText, ChunkIndex: 1,
				Content: "![scan_page_1.jpg](images/scan_page_1.jpg)",
			},
			"parent": {
				ID: "parent", ChunkType: types.ChunkTypeParentText,
				Content: "![scan_page_1.jpg](images/scan_page_1.jpg)",
			},
		},
	}
	plugin := &PluginMerge{chunkRepo: repo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	result := &types.SearchResult{
		ID: "cap-1", KnowledgeID: "doc", ChunkType: string(types.ChunkTypeImageCaption),
		ParentChunkID: "text", Content: caption, ImageInfo: string(imageInfo),
	}

	got := plugin.resolveParentChunks(ctx, &types.ChatManage{}, []*types.SearchResult{result})
	if len(got) != 1 {
		t.Fatalf("result count = %d, want 1", len(got))
	}
	if !strings.Contains(got[0].Content, "甲乙双方") {
		t.Fatalf("caption was dropped by parent expansion: %q", got[0].Content)
	}
	passage := getEnrichedPassageForChat(ctx, got[0])
	if strings.Count(passage, caption) != 1 {
		t.Fatalf("caption duplicated after chat enrichment: %q", passage)
	}
}

func TestResolveImageOCRHit_KeepsTextWhenPlaceholderIsPruned(t *testing.T) {
	ocrText := "仅存在于 OCR 子块中的扫描页正文，用于核对账号 6222。"
	imageInfo, err := json.Marshal([]types.ImageInfo{{URL: "resource://unmatched-scan", OCRText: ocrText}})
	if err != nil {
		t.Fatal(err)
	}
	repo := &expandChunkRepo{
		chunks: map[string]*types.Chunk{
			"text": {
				ID: "text", ParentChunkID: "parent", ChunkType: types.ChunkTypeText, ChunkIndex: 4,
				Content: "![scan_page_1.jpg](images/scan_page_1.jpg)",
			},
			"parent": {
				ID: "parent", ChunkType: types.ChunkTypeParentText,
				Content: "![scan_page_1.jpg](images/scan_page_1.jpg)",
			},
		},
	}
	plugin := &PluginMerge{chunkRepo: repo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	result := &types.SearchResult{
		ID: "ocr-1", KnowledgeID: "doc", ChunkType: string(types.ChunkTypeImageOCR),
		ParentChunkID: "text", Content: ocrText, ImageInfo: string(imageInfo),
	}

	got := plugin.resolveParentChunks(ctx, &types.ChatManage{}, []*types.SearchResult{result})
	if len(got) != 1 {
		t.Fatalf("result count = %d, want 1", len(got))
	}
	if !strings.Contains(got[0].Content, "6222") {
		t.Fatalf("OCR text was dropped after placeholder prune: %q", got[0].Content)
	}
	passage := getEnrichedPassageForChat(ctx, got[0])
	if strings.Count(passage, ocrText) != 1 {
		t.Fatalf("OCR count after chat enrichment = %d: %q", strings.Count(passage, ocrText), passage)
	}
}
