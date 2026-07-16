package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type embeddingArtifactIndexRepository struct {
	interfaces.RetrieveEngineRepository
}

func (r *embeddingArtifactIndexRepository) BatchSave(
	context.Context,
	[]*types.IndexInfo,
	map[string]any,
) error {
	return nil
}

func TestEmbeddingArtifactBatchIndexReusesTextAcrossKnowledgeItems(t *testing.T) {
	store := newEmbeddingArtifactFakeStore()
	provider := &embeddingArtifactFakeEmbedder{
		modelID: "model-1", modelName: "text-embedding", dimensions: 2,
		batchResults: [][]float32{{1, 2}},
	}
	embedder := newEmbeddingArtifactEmbedder(provider, store, 7, "revision-1")
	engine := retriever.NewKVHybridRetrieveEngine(
		&embeddingArtifactIndexRepository{},
		types.PostgresRetrieverEngineType,
	)

	for _, info := range []*types.IndexInfo{
		{SourceID: "chunk-1", ChunkID: "chunk-1", KnowledgeID: "knowledge-1", Content: " shared\r\ntext "},
		{SourceID: "chunk-2", ChunkID: "chunk-2", KnowledgeID: "knowledge-2", Content: "shared\ntext"},
	} {
		err := engine.BatchIndex(
			context.Background(),
			embedder,
			[]*types.IndexInfo{info},
			[]types.RetrieverType{types.VectorRetrieverType},
		)
		require.NoError(t, err)
	}

	require.Equal(t, [][]string{{"shared\ntext"}}, provider.batchCalls)
	require.Equal(t, 1, store.putCalls)
}
