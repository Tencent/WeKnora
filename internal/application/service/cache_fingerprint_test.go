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
	base := calculateWikiMapCacheFingerprint("content", "zh-CN", "model-a", types.WikiExtractionStandard, "content-instr", "extract-instr")
	if base == "" {
		t.Fatal("wiki map cache fingerprint is empty")
	}
	if got := calculateWikiMapCacheFingerprint("content", "zh-CN", "model-a", types.WikiExtractionStandard, "content-instr", "extract-instr"); got != base {
		t.Fatalf("same content/model/config should reuse fingerprint: %s != %s", got, base)
	}
	if got := calculateWikiMapCacheFingerprint("changed", "zh-CN", "model-a", types.WikiExtractionStandard, "content-instr", "extract-instr"); got == base {
		t.Fatal("content changes must invalidate wiki map cache")
	}
	if got := calculateWikiMapCacheFingerprint("content", "zh-CN", "model-b", types.WikiExtractionStandard, "content-instr", "extract-instr"); got == base {
		t.Fatal("synthesis model changes must invalidate wiki map cache")
	}
	if got := calculateWikiMapCacheFingerprint("content", "zh-CN", "model-a", types.WikiExtractionFocused, "content-instr", "extract-instr"); got == base {
		t.Fatal("extraction granularity changes must invalidate wiki map cache")
	}
	if got := calculateWikiMapCacheFingerprint("content", "zh-CN", "model-a", types.WikiExtractionStandard, "other-content", "extract-instr"); got == base {
		t.Fatal("content instructions changes must invalidate wiki map cache")
	}
	if got := calculateWikiMapCacheFingerprint("content", "zh-CN", "model-a", types.WikiExtractionStandard, "content-instr", "other-extract"); got == base {
		t.Fatal("extraction instructions changes must invalidate wiki map cache")
	}
}

func TestSummaryQuestionGraphParseFingerprints_InvalidateIndependently(t *testing.T) {
	sum := calculateSummaryCacheFingerprint("chunks", "model-a", "prompt-a", 2048, "zh")
	if calculateSummaryCacheFingerprint("chunks", "model-a", "prompt-a", 2048, "zh") != sum {
		t.Fatal("summary fingerprint unstable")
	}
	if calculateSummaryCacheFingerprint("chunks", "model-b", "prompt-a", 2048, "zh") == sum {
		t.Fatal("summary model change must invalidate")
	}
	if calculateSummaryCacheFingerprint("chunks", "model-a", "prompt-b", 2048, "zh") == sum {
		t.Fatal("summary prompt change must invalidate")
	}

	q := calculateQuestionCacheFingerprint("c", "p", "n", "title", 3, "m", "prompt", "instr", "zh")
	if calculateQuestionCacheFingerprint("c", "p", "n", "title", 3, "m", "prompt", "instr", "zh") != q {
		t.Fatal("question fingerprint unstable")
	}
	if calculateQuestionCacheFingerprint("c2", "p", "n", "title", 3, "m", "prompt", "instr", "zh") == q {
		t.Fatal("question content change must invalidate")
	}
	if calculateQuestionCacheFingerprint("c", "p", "n", "title", 3, "m", "prompt", "other", "zh") == q {
		t.Fatal("question instructions change must invalidate")
	}

	g := calculateGraphExtractFingerprint("chunk", "model", "desc", "instr", []string{"b", "a"})
	if calculateGraphExtractFingerprint("chunk", "model", "desc", "instr", []string{"a", "b"}) != g {
		t.Fatal("graph tags order must not matter")
	}
	if calculateGraphExtractFingerprint("chunk", "model", "desc", "other", []string{"a", "b"}) == g {
		t.Fatal("graph instructions change must invalidate")
	}

	p := calculateParseCacheFingerprint("filehash", "engine", "pdf", "", map[string]string{"k": "v"})
	if calculateParseCacheFingerprint("filehash", "engine", "pdf", "", map[string]string{"k": "v"}) != p {
		t.Fatal("parse fingerprint unstable")
	}
	if calculateParseCacheFingerprint("filehash2", "engine", "pdf", "", map[string]string{"k": "v"}) == p {
		t.Fatal("file hash change must invalidate parse cache")
	}
	if calculateParseCacheFingerprint("filehash", "other-engine", "pdf", "", map[string]string{"k": "v"}) == p {
		t.Fatal("parser engine change must invalidate parse cache")
	}
}
