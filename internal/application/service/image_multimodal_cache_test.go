package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestImageMultimodalCacheKeys(t *testing.T) {
	cfg := types.VLMConfig{
		Enabled:       true,
		ModelID:       "vlm-model-1",
		BaseURL:       "https://example.test/v1",
		InterfaceType: "openai",
	}
	captionPrompt := buildVLMCaptionPrompt(context.Background(), cfg)

	contentKey, modelID, configHash, cacheKey := imageMultimodalCacheKeys(
		[]byte("image-bytes"), cfg, vlmOCRPrompt, captionPrompt,
	)
	contentKey2, modelID2, configHash2, cacheKey2 := imageMultimodalCacheKeys(
		[]byte("image-bytes"), cfg, vlmOCRPrompt, captionPrompt,
	)

	assert.NotEmpty(t, contentKey)
	assert.Equal(t, "vlm-model-1", modelID)
	assert.Equal(t, contentKey, contentKey2)
	assert.Equal(t, modelID, modelID2)
	assert.Equal(t, configHash, configHash2)
	assert.Equal(t, cacheKey, cacheKey2)

	_, _, _, changedImageKey := imageMultimodalCacheKeys(
		[]byte("different-image-bytes"), cfg, vlmOCRPrompt, captionPrompt,
	)
	assert.NotEqual(t, cacheKey, changedImageKey)

	cfg.ModelID = "vlm-model-2"
	_, _, _, changedModelKey := imageMultimodalCacheKeys(
		[]byte("image-bytes"), cfg, vlmOCRPrompt, captionPrompt,
	)
	assert.NotEqual(t, cacheKey, changedModelKey)

	cfg.ModelID = "vlm-model-1"
	_, _, _, changedPromptKey := imageMultimodalCacheKeys(
		[]byte("image-bytes"), cfg, vlmOCRScannedPDFPrompt, captionPrompt,
	)
	assert.NotEqual(t, cacheKey, changedPromptKey)
}
