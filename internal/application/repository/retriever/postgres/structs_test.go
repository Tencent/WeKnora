package postgres

import (
	"reflect"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/pgvector/pgvector-go"
)

// TestToDBVectorEmbeddingPreservesAccessMetadata catches a missing persistence
// mapping that would silently drop retrieval access constraints at write time.
func TestToDBVectorEmbeddingPreservesAccessMetadata(t *testing.T) {
	want := types.JSONMap{
		"department": "research",
		"levels":     []any{"staff", "lead"},
	}

	mapped := toDBVectorEmbedding(&types.IndexInfo{
		SourceID:       "source-1",
		AccessMetadata: want,
	}, nil)

	if !reflect.DeepEqual(mapped.AccessMetadata, want) {
		t.Fatalf("access metadata = %#v, want %#v", mapped.AccessMetadata, want)
	}

	encoded, err := mapped.AccessMetadata.Value()
	if err != nil {
		t.Fatalf("serialize access metadata: %v", err)
	}
	var roundTripped types.JSONMap
	if err := roundTripped.Scan(encoded); err != nil {
		t.Fatalf("deserialize access metadata: %v", err)
	}
	if !reflect.DeepEqual(roundTripped, want) {
		t.Fatalf("round-tripped access metadata = %#v, want %#v", roundTripped, want)
	}
}

// TestCopyIndicesPreservesAccessMetadata catches a copy path that recreates
// embeddings without their access constraints.
func TestCopyIndicesPreservesAccessMetadata(t *testing.T) {
	source := &pgVector{
		SourceID:       "source-chunk",
		SourceType:     int(types.SummarySourceType),
		ChunkID:        "source-chunk",
		KnowledgeID:    "source-knowledge",
		Content:        "source content",
		Dimension:      2,
		Embedding:      pgvector.NewHalfVector([]float32{1, 2}),
		AccessMetadata: types.JSONMap{"department": "research"},
	}

	copied := copiedVector(source, "target-chunk", "target-chunk", "target-knowledge", "target-kb")

	if copied.SourceID != "target-chunk" || copied.ChunkID != "target-chunk" ||
		copied.KnowledgeID != "target-knowledge" || copied.KnowledgeBaseID != "target-kb" {
		t.Fatalf("copied identifiers = %#v", copied)
	}
	if copied.SourceType != source.SourceType || copied.Content != source.Content ||
		copied.Dimension != source.Dimension || !reflect.DeepEqual(copied.Embedding, source.Embedding) ||
		!reflect.DeepEqual(copied.AccessMetadata, source.AccessMetadata) {
		t.Fatalf("copied vector did not preserve non-identifier fields: %#v", copied)
	}
}
