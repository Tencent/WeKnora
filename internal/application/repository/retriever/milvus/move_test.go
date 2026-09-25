package milvus

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMoveRowsDrainsPagesAndRetriesPartialUpsert(t *testing.T) {
	remaining := map[string]*MilvusVectorEmbeddingWithScore{}
	moved := map[string]*MilvusVectorEmbedding{}
	for i := range 205 {
		id := fmt.Sprint(i)
		remaining[id] = &MilvusVectorEmbeddingWithScore{MilvusVectorEmbedding: MilvusVectorEmbedding{
			ID:              id,
			KnowledgeID:     "doc",
			KnowledgeBaseID: "source",
			TagID:           "old-tag",
			ChunkID:         "chunk-" + id,
			Content:         "text",
			Embedding:       []float32{1, 2, 3},
			IsEnabled:       i%2 == 0,
		}}
	}
	reads, writes := 0, 0
	failure := errors.New("partial upsert")
	read := func(context.Context) ([]*MilvusVectorEmbeddingWithScore, error) {
		reads++
		var rows []*MilvusVectorEmbeddingWithScore
		for _, row := range remaining {
			rows = append(rows, row)
			if len(rows) == 100 {
				break
			}
		}
		return rows, nil
	}
	write := func(_ context.Context, rows []*MilvusVectorEmbedding) error {
		writes++
		for i, row := range rows {
			original := remaining[row.ID]
			require.NotNil(t, original)
			require.Equal(t, "source", original.KnowledgeBaseID, "read rows must not be mutated before acknowledgement")
			require.Equal(t, "target", row.KnowledgeBaseID)
			require.Empty(t, row.TagID)
			expected := original.MilvusVectorEmbedding
			expected.KnowledgeBaseID = "target"
			expected.TagID = ""
			require.Equal(t, expected, *row)
			moved[row.ID] = row
			delete(remaining, row.ID)
			if writes == 2 && i == 16 {
				return failure
			}
		}
		return nil
	}
	require.ErrorIs(t, drainMoveRows(context.Background(), "target", read, write), failure)
	require.Len(t, remaining, 88)
	require.NoError(t, drainMoveRows(context.Background(), "target", read, write))
	require.Empty(t, remaining)
	require.Len(t, moved, 205)
	require.Equal(t, 4, reads, "retry must verify an empty source predicate")
}

func TestMoveRowsRejectsUnacknowledgedProgress(t *testing.T) {
	row := &MilvusVectorEmbeddingWithScore{
		MilvusVectorEmbedding: MilvusVectorEmbedding{ID: "id", KnowledgeBaseID: "source"},
	}
	err := drainMoveRows(
		context.Background(),
		"target",
		func(context.Context) ([]*MilvusVectorEmbeddingWithScore, error) {
			return []*MilvusVectorEmbeddingWithScore{row}, nil
		},
		func(context.Context, []*MilvusVectorEmbedding) error { return nil },
	)
	require.ErrorContains(t, err, "repeated")
}
