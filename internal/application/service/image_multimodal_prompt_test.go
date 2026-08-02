package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildVLMCaptionPrompt(t *testing.T) {
	t.Run("uses configured language and custom instructions", func(t *testing.T) {
		got := buildVLMCaptionPrompt(context.Background(), types.VLMConfig{
			DescriptionLanguage: "English",
			CustomInstructions:  "Focus on alarm codes.",
		})
		if !strings.Contains(got, "in English") || !strings.Contains(got, "Focus on alarm codes.") {
			t.Fatalf("unexpected prompt: %s", got)
		}
	})

	t.Run("defaults to context language", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), types.LanguageContextKey, "ko-KR")
		got := buildVLMCaptionPrompt(ctx, types.VLMConfig{})
		if !strings.Contains(got, "in Korean") {
			t.Fatalf("unexpected prompt: %s", got)
		}
	})
}

func TestBuildStableMultimodalChunkUsesExactInputsAndUniqueLegacyMatch(t *testing.T) {
	payload := types.ImageMultimodalPayload{
		TenantID:        1,
		KnowledgeID:     uuid.New().String(),
		KnowledgeBaseID: uuid.New().String(),
		ChunkID:         uuid.New().String(),
	}
	first, err := buildStableMultimodalChunk(
		payload,
		types.ChunkTypeImageOCR,
		"exact OCR",
		"[]",
		[]byte{0, 1, 2},
		nil,
	)
	require.NoError(t, err)
	second, err := buildStableMultimodalChunk(
		payload,
		types.ChunkTypeImageOCR,
		"exact OCR",
		"[]",
		[]byte{0, 1, 2},
		nil,
	)
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)

	signedURLChanged := payload
	signedURLChanged.ImageURL = "https://objects.example/image.png?signature=rotated"
	sameBytes, err := buildStableMultimodalChunk(
		signedURLChanged,
		types.ChunkTypeImageOCR,
		"exact OCR",
		"[]",
		[]byte{0, 1, 2},
		nil,
	)
	require.NoError(t, err)
	assert.Equal(t, first.ID, sameBytes.ID, "signed URL rotation must not change byte-addressed identity")

	changed, err := buildStableMultimodalChunk(
		payload,
		types.ChunkTypeImageOCR,
		"exact OCR",
		"[]",
		[]byte{0, 1, 3},
		nil,
	)
	require.NoError(t, err)
	assert.NotEqual(t, first.ID, changed.ID)

	changedOutput, err := buildStableMultimodalChunk(
		payload,
		types.ChunkTypeImageOCR,
		"changed OCR",
		"[]",
		[]byte{0, 1, 2},
		nil,
	)
	require.NoError(t, err)
	assert.NotEqual(t, first.ID, changedOutput.ID)

	legacyID := uuid.New().String()
	reused, err := buildStableMultimodalChunk(
		payload,
		types.ChunkTypeImageOCR,
		"exact OCR",
		"[]",
		[]byte{0, 1, 2},
		[]*types.Chunk{{
			ID:        legacyID,
			ChunkType: types.ChunkTypeImageOCR,
			Content:   "exact OCR",
		}},
	)
	require.NoError(t, err)
	assert.Equal(t, legacyID, reused.ID)
}

func TestMultimodalPendingKeyIsAttemptScoped(t *testing.T) {
	assert.Equal(t, "multimodal:pending:k:7", multimodalPendingKey("k", 7))
	assert.Equal(t, "multimodal:pending:k", multimodalPendingKey("k", 0))
}
