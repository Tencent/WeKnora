package service

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildImageMultimodalChunksRebindsToCurrentParent(t *testing.T) {
	imageInfo := types.ImageInfo{
		URL:     "provider://bucket/image.png",
		OCRText: "canonical OCR",
		Caption: "canonical caption",
	}
	now := time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC)

	first := buildImageMultimodalChunks(types.ImageMultimodalPayload{
		TenantID:        7,
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		ChunkID:         "parent-v1",
	}, imageInfo, now)
	second := buildImageMultimodalChunks(types.ImageMultimodalPayload{
		TenantID:        7,
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		ChunkID:         "parent-v2",
	}, imageInfo, now)

	require.Len(t, first, 2)
	require.Len(t, second, 2)
	for _, chunk := range first {
		assert.Equal(t, "parent-v1", chunk.ParentChunkID)
		assert.Equal(t, now, chunk.CreatedAt)
		assert.Equal(t, now, chunk.UpdatedAt)
	}
	for _, chunk := range second {
		assert.Equal(t, "parent-v2", chunk.ParentChunkID)
		assert.NotEqual(t, first[0].ID, chunk.ID)
	}
	assert.Equal(t, types.ChunkTypeImageOCR, first[0].ChunkType)
	assert.Equal(t, types.ChunkTypeImageCaption, first[1].ChunkType)
}

func TestBuildImageMultimodalChunksSkipsEmptyResults(t *testing.T) {
	chunks := buildImageMultimodalChunks(types.ImageMultimodalPayload{
		TenantID: 7,
		ChunkID:  "parent",
	}, types.ImageInfo{}, time.Now())

	assert.Empty(t, chunks)
}
