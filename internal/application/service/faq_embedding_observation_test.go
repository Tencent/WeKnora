package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/testutil/modelcount"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type faqEmbeddingObservationIndexer struct{}

func (faqEmbeddingObservationIndexer) BatchIndex(
	ctx context.Context,
	embedder embedding.Embedder,
	indexInfoList []*types.IndexInfo,
) error {
	texts := make([]string, 0, len(indexInfoList))
	for _, indexInfo := range indexInfoList {
		texts = append(texts, indexInfo.Content)
	}
	_, err := embedder.BatchEmbedWithPool(ctx, embedder, texts)
	return err
}

func TestFAQEmbeddingObservation_RecordsStructuredOperation(
	t *testing.T,
) {
	countingEmbedder := modelcount.NewCountingEmbedder(
		modelcount.CountingEmbedderOptions{
			ModelID:    "faq-embedding-test",
			ModelName:  "faq-embedding-model",
			Dimensions: 4,
		},
	)
	indexInfoList := faqEmbeddingObservationIndexInfoList(3)

	output, err := observeUnspannedEmbeddingBatch(
		context.Background(),
		types.IngestionOperationEmbeddingFAQ,
		countingEmbedder,
		faqEmbeddingObservationIndexer{},
		indexInfoList,
		"FAQ_EMBEDDING_FAILED",
	)
	require.NoError(t, err)

	modelSnapshot := countingEmbedder.Snapshot()
	require.Equal(t, 1, modelSnapshot.RequestCount)
	require.Equal(t, 3, modelSnapshot.TotalInputItems)
	require.Len(t, modelSnapshot.Calls, 1)
	require.Equal(
		t,
		types.IngestionOperationEmbeddingFAQ,
		modelSnapshot.Calls[0].Operation,
	)

	require.Equal(
		t,
		string(types.IngestionOperationEmbeddingFAQ),
		output["operation"],
	)
	require.Equal(t, types.StageEmbedding, output["stage"])
	require.Equal(t, "structured_log", output["observation_sink"])
	require.EqualValues(
		t,
		modelSnapshot.RequestCount,
		output["request_count"],
	)
	require.EqualValues(
		t,
		modelSnapshot.TotalInputItems,
		output["total_items"],
	)
	require.EqualValues(t, 3, output["computed_items"])
	require.EqualValues(t, 0, output["reused_items"])
	require.EqualValues(t, 3, output["vectors_written"])
	require.Equal(
		t,
		string(types.IngestionCacheStatusNotSupported),
		output["cache_status"],
	)
	require.Equal(t, true, output["success"])
}

func TestFAQEmbeddingObservation_FailurePreservesRequestCount(
	t *testing.T,
) {
	expectedError := errors.New("FAQ embedding provider failed")
	countingEmbedder := modelcount.NewCountingEmbedder(
		modelcount.CountingEmbedderOptions{
			ModelID:      "faq-embedding-test",
			ModelName:    "faq-embedding-model",
			Dimensions:   4,
			DefaultError: expectedError,
		},
	)

	output, err := observeUnspannedEmbeddingBatch(
		context.Background(),
		types.IngestionOperationEmbeddingFAQ,
		countingEmbedder,
		faqEmbeddingObservationIndexer{},
		faqEmbeddingObservationIndexInfoList(2),
		"FAQ_EMBEDDING_FAILED",
	)
	require.ErrorIs(t, err, expectedError)

	modelSnapshot := countingEmbedder.Snapshot()
	require.Equal(t, 1, modelSnapshot.RequestCount)
	require.Equal(t, 2, modelSnapshot.TotalInputItems)
	require.EqualValues(
		t,
		modelSnapshot.RequestCount,
		output["request_count"],
	)
	require.EqualValues(
		t,
		modelSnapshot.TotalInputItems,
		output["total_items"],
	)
	require.EqualValues(t, 0, output["computed_items"])
	require.EqualValues(t, 0, output["vectors_written"])
	require.Equal(t, false, output["success"])
	require.Equal(
		t,
		"FAQ_EMBEDDING_FAILED",
		output["error_code"],
	)
}

func faqEmbeddingObservationIndexInfoList(
	count int,
) []*types.IndexInfo {
	indexInfoList := make([]*types.IndexInfo, 0, count)
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("faq-%02d", i)
		indexInfoList = append(indexInfoList, &types.IndexInfo{
			Content:         fmt.Sprintf("stable FAQ question %02d", i),
			SourceID:        id,
			SourceType:      types.ChunkSourceType,
			ChunkID:         id,
			KnowledgeID:     "knowledge-faq-test",
			KnowledgeBaseID: "kb-faq-test",
			KnowledgeType:   types.KnowledgeTypeFAQ,
			IsEnabled:       true,
		})
	}
	return indexInfoList
}
