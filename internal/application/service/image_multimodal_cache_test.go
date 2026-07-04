package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestMultimodalResultCacheKeyInvalidatesByImageModelPromptAndType(t *testing.T) {
	imageHash := multimodalImageHash([]byte("image-a"))
	promptHash := multimodalPromptHash("prompt-a")
	base := multimodalResultCacheKey(imageHash, "vlm-a", promptHash, types.MultimodalOutputOCR)
	if base == "" {
		t.Fatal("cache key is empty")
	}
	if got := multimodalResultCacheKey(imageHash, "vlm-a", promptHash, types.MultimodalOutputOCR); got != base {
		t.Fatalf("same multimodal inputs should reuse cache key: %s != %s", got, base)
	}
	if got := multimodalResultCacheKey(multimodalImageHash([]byte("image-b")), "vlm-a", promptHash, types.MultimodalOutputOCR); got == base {
		t.Fatal("image bytes changes must invalidate multimodal cache")
	}
	if got := multimodalResultCacheKey(imageHash, "vlm-b", promptHash, types.MultimodalOutputOCR); got == base {
		t.Fatal("VLM model changes must invalidate multimodal cache")
	}
	if got := multimodalResultCacheKey(imageHash, "vlm-a", multimodalPromptHash("prompt-b"), types.MultimodalOutputOCR); got == base {
		t.Fatal("prompt changes must invalidate multimodal cache")
	}
	if got := multimodalResultCacheKey(imageHash, "vlm-a", promptHash, types.MultimodalOutputCaption); got == base {
		t.Fatal("output type changes must invalidate multimodal cache")
	}
}
