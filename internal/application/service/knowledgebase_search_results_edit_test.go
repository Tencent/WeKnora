package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestIsSearchableChunkSkipsUnsynchronizedEdits(t *testing.T) {
	service := &knowledgeBaseService{}
	for _, status := range []string{"processing", "failed"} {
		chunk := &types.Chunk{ChunkType: types.ChunkTypeText, IndexStatus: status}
		if service.isSearchableChunk(chunk) {
			t.Fatalf("chunk with index status %q should not be searchable", status)
		}
	}
	for _, status := range []string{"", "ready"} {
		chunk := &types.Chunk{ChunkType: types.ChunkTypeText, IndexStatus: status}
		if !service.isSearchableChunk(chunk) {
			t.Fatalf("chunk with index status %q should be searchable", status)
		}
	}
}
