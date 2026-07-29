package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/testutil/modelcount"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type summaryEmbeddingObservationIndexer struct{}

func (summaryEmbeddingObservationIndexer) BatchIndex(
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

func TestSummaryEmbeddingObservation_SpanMatchesCountingEmbedder(
	t *testing.T,
) {
	ctx := context.Background()
	tracker, db := setupSpanTrackerTest(t)
	parent, attempt := beginSummaryEmbeddingObservationParent(
		t,
		ctx,
		tracker,
		"knowledge-summary-embedding-success",
	)

	countingEmbedder := modelcount.NewCountingEmbedder(
		modelcount.CountingEmbedderOptions{
			ModelID:    "summary-embedding-test",
			ModelName:  "summary-embedding-model",
			Dimensions: 4,
		},
	)
	indexInfoList := summaryEmbeddingObservationIndexInfo()

	err := observePostprocessEmbeddingBatch(
		ctx,
		tracker,
		parent,
		"postprocess.summary.embedding",
		types.IngestionOperationEmbeddingSummary,
		countingEmbedder,
		summaryEmbeddingObservationIndexer{},
		indexInfoList,
		"SUMMARY_EMBEDDING_FAILED",
	)
	require.NoError(t, err)

	modelSnapshot := countingEmbedder.Snapshot()
	require.Equal(t, 1, modelSnapshot.RequestCount)
	require.Equal(t, 1, modelSnapshot.TotalInputItems)
	require.Len(t, modelSnapshot.Calls, 1)
	require.Equal(
		t,
		types.IngestionOperationEmbeddingSummary,
		modelSnapshot.Calls[0].Operation,
	)

	storedSpan := loadSummaryEmbeddingObservationSpan(
		t,
		db,
		"knowledge-summary-embedding-success",
		attempt,
	)
	require.Equal(t, types.SpanStatusDone, storedSpan.Status)
	require.Equal(
		t,
		string(types.IngestionOperationEmbeddingSummary),
		storedSpan.Output["operation"],
	)
	require.Equal(
		t,
		types.StagePostProcess,
		storedSpan.Output["stage"],
	)
	require.EqualValues(
		t,
		modelSnapshot.RequestCount,
		storedSpan.Output["request_count"],
	)
	require.EqualValues(
		t,
		modelSnapshot.TotalInputItems,
		storedSpan.Output["total_items"],
	)
	require.EqualValues(t, 1, storedSpan.Output["computed_items"])
	require.EqualValues(t, 0, storedSpan.Output["reused_items"])
	require.EqualValues(t, 1, storedSpan.Output["vectors_written"])
	require.Equal(
		t,
		string(types.IngestionCacheStatusNotSupported),
		storedSpan.Output["cache_status"],
	)
	require.Equal(t, true, storedSpan.Output["success"])
}

func TestSummaryEmbeddingObservation_FailurePreservesRequestCount(
	t *testing.T,
) {
	ctx := context.Background()
	tracker, db := setupSpanTrackerTest(t)
	parent, attempt := beginSummaryEmbeddingObservationParent(
		t,
		ctx,
		tracker,
		"knowledge-summary-embedding-failure",
	)

	expectedError := errors.New("summary embedding provider failed")
	countingEmbedder := modelcount.NewCountingEmbedder(
		modelcount.CountingEmbedderOptions{
			ModelID:      "summary-embedding-test",
			ModelName:    "summary-embedding-model",
			Dimensions:   4,
			DefaultError: expectedError,
		},
	)

	err := observePostprocessEmbeddingBatch(
		ctx,
		tracker,
		parent,
		"postprocess.summary.embedding",
		types.IngestionOperationEmbeddingSummary,
		countingEmbedder,
		summaryEmbeddingObservationIndexer{},
		summaryEmbeddingObservationIndexInfo(),
		"SUMMARY_EMBEDDING_FAILED",
	)
	require.ErrorIs(t, err, expectedError)

	modelSnapshot := countingEmbedder.Snapshot()
	require.Equal(t, 1, modelSnapshot.RequestCount)
	require.Equal(t, 1, modelSnapshot.TotalInputItems)

	storedSpan := loadSummaryEmbeddingObservationSpan(
		t,
		db,
		"knowledge-summary-embedding-failure",
		attempt,
	)
	require.Equal(t, types.SpanStatusFailed, storedSpan.Status)
	require.Equal(t, "SUMMARY_EMBEDDING_FAILED", storedSpan.ErrorCode)
	require.Contains(
		t,
		storedSpan.ErrorDetail,
		"summary embedding provider failed",
	)
	require.EqualValues(
		t,
		modelSnapshot.RequestCount,
		storedSpan.Output["request_count"],
	)
	require.EqualValues(
		t,
		modelSnapshot.TotalInputItems,
		storedSpan.Output["total_items"],
	)
	require.EqualValues(t, 0, storedSpan.Output["computed_items"])
	require.EqualValues(t, 0, storedSpan.Output["vectors_written"])
	require.Equal(t, false, storedSpan.Output["success"])
	require.Equal(
		t,
		"SUMMARY_EMBEDDING_FAILED",
		storedSpan.Output["error_code"],
	)
}

func beginSummaryEmbeddingObservationParent(
	t *testing.T,
	ctx context.Context,
	tracker SpanTracker,
	knowledgeID string,
) (*Span, int) {
	t.Helper()

	_, attempt, err := tracker.OpenAttempt(ctx, knowledgeID, "")
	require.NoError(t, err)

	postprocessSpan := tracker.BeginStage(
		ctx,
		knowledgeID,
		attempt,
		types.StagePostProcess,
		nil,
	)
	require.NotNil(t, postprocessSpan)

	summarySpan := tracker.BeginSubSpan(
		ctx,
		postprocessSpan,
		"postprocess.summary",
		types.SpanKindSubSpan,
		nil,
	)
	require.NotNil(t, summarySpan)

	return summarySpan, attempt
}

func summaryEmbeddingObservationIndexInfo() []*types.IndexInfo {
	return []*types.IndexInfo{{
		Content:         "# Summary\nstable generated summary",
		SourceID:        "summary-chunk-test",
		SourceType:      types.ChunkSourceType,
		ChunkID:         "summary-chunk-test",
		KnowledgeID:     "knowledge-summary-test",
		KnowledgeBaseID: "kb-summary-test",
		IsEnabled:       true,
	}}
}

func loadSummaryEmbeddingObservationSpan(
	t *testing.T,
	db *gorm.DB,
	knowledgeID string,
	attempt int,
) types.KnowledgeProcessingSpan {
	t.Helper()

	var storedSpan types.KnowledgeProcessingSpan
	require.NoError(
		t,
		db.Where(
			"knowledge_id = ? AND attempt = ? AND name = ?",
			knowledgeID,
			attempt,
			"postprocess.summary.embedding",
		).Take(&storedSpan).Error,
	)
	require.NotNil(t, storedSpan.Output)

	return storedSpan
}
