package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestCalculateMultimodalContentHash_InvalidatesByModelPromptAndImage(t *testing.T) {
	base := calculateMultimodalContentHash([]byte("same-image"), "vlm-a", "prompt-a", types.ChunkTypeImageOCR)
	if base == "" {
		t.Fatal("multimodal hash is empty")
	}
	if got := calculateMultimodalContentHash([]byte("same-image"), "vlm-a", "prompt-a", types.ChunkTypeImageOCR); got != base {
		t.Fatalf("same image/model/prompt should reuse hash: %s != %s", got, base)
	}
	if got := calculateMultimodalContentHash([]byte("changed-image"), "vlm-a", "prompt-a", types.ChunkTypeImageOCR); got == base {
		t.Fatal("image bytes changes must invalidate multimodal cache")
	}
	if got := calculateMultimodalContentHash([]byte("same-image"), "vlm-b", "prompt-a", types.ChunkTypeImageOCR); got == base {
		t.Fatal("VLM model changes must invalidate multimodal cache")
	}
	if got := calculateMultimodalContentHash([]byte("same-image"), "vlm-a", "prompt-b", types.ChunkTypeImageOCR); got == base {
		t.Fatal("prompt changes must invalidate multimodal cache")
	}
	if got := calculateMultimodalContentHash([]byte("same-image"), "vlm-a", "prompt-a", types.ChunkTypeImageCaption); got == base {
		t.Fatal("chunk type changes must invalidate multimodal cache")
	}
}

func TestWikiMapCacheFingerprint_InvalidatesByContentModelAndGranularity(t *testing.T) {
	base := calculateWikiMapCacheFingerprint("content", "zh-CN", "model-a", types.WikiExtractionStandard)
	if base == "" {
		t.Fatal("wiki map cache fingerprint is empty")
	}
	if got := calculateWikiMapCacheFingerprint("content", "zh-CN", "model-a", types.WikiExtractionStandard); got != base {
		t.Fatalf("same content/model/config should reuse fingerprint: %s != %s", got, base)
	}
	if got := calculateWikiMapCacheFingerprint("changed", "zh-CN", "model-a", types.WikiExtractionStandard); got == base {
		t.Fatal("content changes must invalidate wiki map cache")
	}
	if got := calculateWikiMapCacheFingerprint("content", "zh-CN", "model-b", types.WikiExtractionStandard); got == base {
		t.Fatal("synthesis model changes must invalidate wiki map cache")
	}
	if got := calculateWikiMapCacheFingerprint("content", "zh-CN", "model-a", types.WikiExtractionFocused); got == base {
		t.Fatal("extraction granularity changes must invalidate wiki map cache")
	}
}
