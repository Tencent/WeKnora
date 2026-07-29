package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/testutil/modelcount"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestWikiEmbeddingObservation_RecordsTaxonomyPurpose(
	t *testing.T,
) {
	countingEmbedder := modelcount.NewCountingEmbedder(
		modelcount.CountingEmbedderOptions{
			ModelID:    "wiki-embedding-test",
			ModelName:  "wiki-embedding-model",
			Dimensions: 4,
		},
	)
	texts := []string{
		"Architecture / Storage",
		"Architecture / Retrieval",
		"Operations / Monitoring",
	}

	vectors, output, err := observeUnspannedDirectEmbeddingBatch(
		context.Background(),
		types.IngestionOperationEmbeddingWikiPage,
		countingEmbedder,
		texts,
		"WIKI_EMBEDDING_FAILED",
		types.JSONMap{
			"purpose":           "taxonomy_folder",
			"knowledge_base_id": "kb-wiki-test",
		},
	)
	require.NoError(t, err)
	require.Len(t, vectors, len(texts))

	modelSnapshot := countingEmbedder.Snapshot()
	require.Equal(t, 1, modelSnapshot.RequestCount)
	require.Equal(t, len(texts), modelSnapshot.TotalInputItems)
	require.Len(t, modelSnapshot.Calls, 1)
	require.Equal(
		t,
		types.IngestionOperationEmbeddingWikiPage,
		modelSnapshot.Calls[0].Operation,
	)

	require.Equal(
		t,
		string(types.IngestionOperationEmbeddingWikiPage),
		output["operation"],
	)
	require.Equal(t, types.StagePostProcess, output["stage"])
	require.Equal(t, "taxonomy_folder", output["purpose"])
	require.Equal(t, "kb-wiki-test", output["knowledge_base_id"])
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
	require.EqualValues(t, len(texts), output["computed_items"])
	require.EqualValues(t, len(texts), output["vectors_written"])
	require.EqualValues(t, 0, output["reused_items"])
	require.Equal(t, true, output["success"])
}

func TestWikiEmbeddingObservation_FailurePreservesRequestCount(
	t *testing.T,
) {
	expectedError := errors.New("wiki embedding provider failed")
	countingEmbedder := modelcount.NewCountingEmbedder(
		modelcount.CountingEmbedderOptions{
			ModelID:      "wiki-embedding-test",
			ModelName:    "wiki-embedding-model",
			Dimensions:   4,
			DefaultError: expectedError,
		},
	)

	vectors, output, err := observeUnspannedDirectEmbeddingBatch(
		context.Background(),
		types.IngestionOperationEmbeddingWikiPage,
		countingEmbedder,
		[]string{"entity one", "concept two"},
		"WIKI_EMBEDDING_FAILED",
		types.JSONMap{
			"purpose": "taxonomy_item",
		},
	)
	require.ErrorIs(t, err, expectedError)
	require.Nil(t, vectors)

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
	require.Equal(t, "taxonomy_item", output["purpose"])
	require.EqualValues(t, 0, output["computed_items"])
	require.EqualValues(t, 0, output["vectors_written"])
	require.Equal(t, false, output["success"])
	require.Equal(
		t,
		"WIKI_EMBEDDING_FAILED",
		output["error_code"],
	)
}

func TestWikiEmbeddingObservation_MetadataCannotOverwriteCoreFields(
	t *testing.T,
) {
	countingEmbedder := modelcount.NewCountingEmbedder(
		modelcount.CountingEmbedderOptions{
			Dimensions: 3,
		},
	)

	_, output, err := observeUnspannedDirectEmbeddingBatch(
		context.Background(),
		types.IngestionOperationEmbeddingWikiPage,
		countingEmbedder,
		[]string{"entity"},
		"WIKI_EMBEDDING_FAILED",
		types.JSONMap{
			"operation":     "overwritten.operation",
			"request_count": 999,
			"purpose":       "taxonomy_item",
		},
	)
	require.NoError(t, err)
	require.Equal(
		t,
		string(types.IngestionOperationEmbeddingWikiPage),
		output["operation"],
	)
	require.EqualValues(t, 1, output["request_count"])
	require.Equal(t, "taxonomy_item", output["purpose"])
}
