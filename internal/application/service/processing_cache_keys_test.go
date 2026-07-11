package service

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestStableChunkID(t *testing.T) {
	id1 := stableChunkID("kid-1", types.ChunkTypeText, 1, 10, 20, "hello world", "# intro")
	id2 := stableChunkID("kid-1", types.ChunkTypeText, 1, 10, 20, "hello world", "# intro")
	if id1 != id2 {
		t.Fatalf("same input should produce same chunk ID: %s != %s", id1, id2)
	}

	id3 := stableChunkID("kid-1", types.ChunkTypeText, 1, 10, 20, "hello changed", "# intro")
	if id1 == id3 {
		t.Fatalf("content changes must change chunk ID: %s", id1)
	}

	id4 := stableChunkID("kid-1", types.ChunkTypeImageOCR, 1, 10, 20, "hello world", "# intro")
	if id1 == id4 {
		t.Fatalf("chunk type changes must change chunk ID: %s", id1)
	}
}

func TestStableQuestionID(t *testing.T) {
	id1 := stableQuestionID("chunk-1", "What is WeKnora?", 0)
	id2 := stableQuestionID("chunk-1", "What is WeKnora?", 0)
	if id1 != id2 {
		t.Fatalf("same question input should produce same ID: %s != %s", id1, id2)
	}
	if id1 == stableQuestionID("chunk-1", "What is WeKnora?", 1) {
		t.Fatal("question ordinal should be part of the stable ID")
	}
	sourceID := strings.Repeat("c", 36) + "-" + stableQuestionID(strings.Repeat("c", 36), "question", 0)
	if len(sourceID) > 128 {
		t.Fatalf("question source ID must fit VARCHAR(128), got %d characters", len(sourceID))
	}
	if len(sourceID) <= 64 {
		t.Fatalf("regression fixture must demonstrate why VARCHAR(64) was insufficient, got %d", len(sourceID))
	}
}

func TestNormalizedContentHash(t *testing.T) {
	h1 := normalizedContentHash(" hello   world \n\nsecond line ")
	h2 := normalizedContentHash("hello world\n\nsecond line")
	if h1 != h2 {
		t.Fatalf("whitespace-only changes should normalize to same hash: %s != %s", h1, h2)
	}
	if h1 == normalizedContentHash("hello world\n\nchanged") {
		t.Fatal("semantic content changes must change normalized hash")
	}
}

func TestVLMCacheKeysInvalidateOnlyOnVLMInputs(t *testing.T) {
	image := []byte("rendered image bytes")
	ocrKey := vlmOCRCacheKey(image, "vlm-a", "scanned_pdf", "ocr prompt")
	if ocrKey != vlmOCRCacheKey([]byte("rendered image bytes"), "vlm-a", "scanned_pdf", "ocr prompt") {
		t.Fatal("same OCR inputs should produce same cache key")
	}
	if ocrKey == vlmOCRCacheKey([]byte("changed image"), "vlm-a", "scanned_pdf", "ocr prompt") {
		t.Fatal("image byte changes must invalidate OCR cache")
	}
	if ocrKey == vlmOCRCacheKey(image, "vlm-b", "scanned_pdf", "ocr prompt") {
		t.Fatal("VLM model changes must invalidate OCR cache")
	}
	if ocrKey == vlmOCRCacheKey(image, "vlm-a", "inline_image", "ocr prompt") {
		t.Fatal("image source type changes must invalidate OCR cache")
	}
	if ocrKey == vlmOCRCacheKey(image, "vlm-a", "scanned_pdf", "changed prompt") {
		t.Fatal("OCR prompt changes must invalidate OCR cache")
	}

	captionKey := vlmCaptionCacheKey(image, "vlm-a", "caption prompt")
	if captionKey != vlmCaptionCacheKey([]byte("rendered image bytes"), "vlm-a", "caption prompt") {
		t.Fatal("same caption inputs should produce same cache key")
	}
	if captionKey == vlmCaptionCacheKey(image, "vlm-b", "caption prompt") {
		t.Fatal("VLM model changes must invalidate caption cache")
	}
	if captionKey == vlmCaptionCacheKey(image, "vlm-a", "changed prompt") {
		t.Fatal("caption prompt changes must invalidate caption cache")
	}
}

func TestWikiMapCacheKeyLayeredInvalidation(t *testing.T) {
	key := wikiMapCacheKey(" hello   wiki ", "kb-1", "kid-1", "zh", "standard", "chat-a", "prompt-a")
	if key != wikiMapCacheKey("hello wiki", "kb-1", "kid-1", "zh", "standard", "chat-a", "prompt-a") {
		t.Fatal("same normalized document should produce same wiki map key")
	}
	if key == wikiMapCacheKey("changed wiki", "kb-1", "kid-1", "zh", "standard", "chat-a", "prompt-a") {
		t.Fatal("document content changes must invalidate wiki map cache")
	}
	if key == wikiMapCacheKey("hello wiki", "kb-1", "kid-1", "en", "standard", "chat-a", "prompt-a") {
		t.Fatal("language changes must invalidate wiki map cache")
	}
	if key == wikiMapCacheKey("hello wiki", "kb-1", "kid-1", "zh", "standard", "chat-b", "prompt-a") {
		t.Fatal("synthesis model changes must invalidate wiki map cache")
	}
	if key == wikiMapCacheKey("hello wiki", "kb-1", "kid-1", "zh", "focused", "chat-a", "prompt-a") {
		t.Fatal("extraction granularity changes must invalidate wiki map cache")
	}
	if key == wikiMapCacheKey("hello wiki", "kb-1", "kid-1", "zh", "standard", "chat-a", "prompt-b") {
		t.Fatal("prompt bundle changes must invalidate wiki map cache")
	}
	withOldURL := `<image url="local://old"><image_original>![x](local://old)</image_original><image_ocr>same</image_ocr></image>`
	withNewURL := `<image url="local://new"><image_original>![x](local://new)</image_original><image_ocr>same</image_ocr></image>`
	if wikiMapCacheKey(withOldURL, "kb-1", "kid-1", "zh", "standard", "chat-a", "prompt-a") !=
		wikiMapCacheKey(withNewURL, "kb-1", "kid-1", "zh", "standard", "chat-a", "prompt-a") {
		t.Fatal("image storage URL changes must not invalidate frozen wiki content")
	}
}

func TestSummaryAndQuestionCacheKeysInvalidateOnPromptInputs(t *testing.T) {
	summaryKey := summaryCacheKey("summary content", "chat-a", "prompt-a", 512)
	if summaryKey != summaryCacheKey(" summary   content ", "chat-a", "prompt-a", 512) {
		t.Fatal("same normalized summary content should produce same cache key")
	}
	if summaryKey == summaryCacheKey("changed content", "chat-a", "prompt-a", 512) {
		t.Fatal("summary content changes must invalidate cache")
	}
	if summaryKey == summaryCacheKey("summary content", "chat-b", "prompt-a", 512) {
		t.Fatal("summary model changes must invalidate cache")
	}
	if summaryKey == summaryCacheKey("summary content", "chat-a", "prompt-b", 512) {
		t.Fatal("summary prompt changes must invalidate cache")
	}
	if summaryKey == summaryCacheKey("summary content", "chat-a", "prompt-a", 1024) {
		t.Fatal("summary max token changes must invalidate cache")
	}

	questionKey := questionCacheKey("question prompt", "chat-a")
	if questionKey != questionCacheKey(" question   prompt ", "chat-a") {
		t.Fatal("same normalized question prompt should produce same cache key")
	}
	if questionKey == questionCacheKey("changed prompt", "chat-a") {
		t.Fatal("question prompt changes must invalidate cache")
	}
	if questionKey == questionCacheKey("question prompt", "chat-b") {
		t.Fatal("question model changes must invalidate cache")
	}
}

func TestParseArtifactCacheKeyLayeredInvalidation(t *testing.T) {
	fileBytes := []byte("pdf bytes")
	overrides := map[string]string{"pdf_force_scanned": "true"}
	key := parseArtifactCacheKey(fileBytes, "doc.pdf", "pdf", "mineru", "Doc", overrides)
	if key != parseArtifactCacheKey([]byte("pdf bytes"), "doc.pdf", "pdf", "mineru", "Doc", map[string]string{"pdf_force_scanned": "true"}) {
		t.Fatal("same parser inputs should produce same parse artifact cache key")
	}
	if key == parseArtifactCacheKey([]byte("changed"), "doc.pdf", "pdf", "mineru", "Doc", overrides) {
		t.Fatal("file byte changes must invalidate parse artifact cache")
	}
	if key == parseArtifactCacheKey(fileBytes, "doc.pdf", "pdf", "builtin", "Doc", overrides) {
		t.Fatal("parser engine changes must invalidate parse artifact cache")
	}
	if key == parseArtifactCacheKey(fileBytes, "doc.pdf", "pdf", "mineru", "Doc", map[string]string{"pdf_force_scanned": "false"}) {
		t.Fatal("parser override changes must invalidate parse artifact cache")
	}
	if key == parseArtifactCacheKey(fileBytes, "doc.pdf", "txt", "mineru", "Doc", overrides) {
		t.Fatal("file type changes must invalidate parse artifact cache")
	}
}
