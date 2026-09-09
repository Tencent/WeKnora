package tools

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestWriteSourceDocumentChunkExposesStableChunkID(t *testing.T) {
	var output strings.Builder
	writeSourceDocumentChunk(&output, &types.Chunk{
		ID:         "chunk-uuid-1",
		ChunkIndex: 6,
		ChunkType:  types.ChunkTypeText,
	}, "range", "source content")

	got := output.String()
	if !strings.Contains(got, `chunk_id="chunk-uuid-1"`) ||
		!strings.Contains(got, `index="7"`) ||
		!strings.Contains(got, `type="range"`) {
		t.Fatalf("source chunk identity is not visible to the model: %s", got)
	}
}
