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
	"gorm.io/gorm"
)

type questionEmbeddingObservationIndexer struct{}

func (questionEmbeddingObservationIndexer) BatchIndex(
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

func TestQuestionEmbeddingObservation_SpanMatchesCountingEmbedder(
	t *testing.T,
) {
	ctx := context.Background()
	tracker, db := setupSpanTrackerTest(t)
	parent, attempt := beginQuestionEmbeddingObservationParent(
		t,
		ctx,
		tracker,
		"knowledge-question-embedding-success",
		"postprocess.question",
	)

	countingEmbedder := modelcount.NewCountingEmbedder(
		modelcount.CountingEmbedderOptions{
			ModelID:    "question-embedding-test",
			ModelName:  "question-embedding-model",
			Dimensions: 4,
		},
	)
	indexInfoList := questionEmbeddingObservationIndexInfoList(3)

	err := observePostprocessEmbeddingBatch(
		ctx,
		tracker,
		parent,
		"postprocess.question.embedding",
		types.IngestionOperationEmbeddingQuestion,
		countingEmbedder,
		questionEmbeddingObservationIndexer{},
		indexInfoList,
		"QUESTION_EMBEDDING_FAILED",
	)
	require.NoError(t, err)

	modelSnapshot := countingEmbedder.Snapshot()
	require.Equal(t, 1, modelSnapshot.RequestCount)
	require.Equal(t, 3, modelSnapshot.TotalInputItems)
	require.Len(t, modelSnapshot.Calls, 1)
	require.Equal(
		t,
		types.IngestionOperationEmbeddingQuestion,
		modelSnapshot.Calls[0].Operation,
	)

	storedSpan := loadQuestionEmbeddingObservationSpan(
		t,
		db,
		"knowledge-question-embedding-success",
		attempt,
		"postprocess.question.embedding",
	)
	require.Equal(t, types.SpanStatusDone, storedSpan.Status)
	require.Equal(
		t,
		string(types.IngestionOperationEmbeddingQuestion),
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
	require.EqualValues(t, 3, storedSpan.Output["computed_items"])
	require.EqualValues(t, 0, storedSpan.Output["reused_items"])
	require.EqualValues(t, 3, storedSpan.Output["vectors_written"])
	require.Equal(t, true, storedSpan.Output["success"])
}

func TestQuestionEmbeddingObservation_FailurePreservesRequestCount(
	t *testing.T,
) {
	ctx := context.Background()
	tracker, db := setupSpanTrackerTest(t)
	parent, attempt := beginQuestionEmbeddingObservationParent(
		t,
		ctx,
		tracker,
		"knowledge-question-embedding-failure",
		"postprocess.question",
	)

	expectedError := errors.New("question embedding provider failed")
	countingEmbedder := modelcount.NewCountingEmbedder(
		modelcount.CountingEmbedderOptions{
			ModelID:      "question-embedding-test",
			ModelName:    "question-embedding-model",
			Dimensions:   4,
			DefaultError: expectedError,
		},
	)

	err := observePostprocessEmbeddingBatch(
		ctx,
		tracker,
		parent,
		"postprocess.question.embedding",
		types.IngestionOperationEmbeddingQuestion,
		countingEmbedder,
		questionEmbeddingObservationIndexer{},
		questionEmbeddingObservationIndexInfoList(2),
		"QUESTION_EMBEDDING_FAILED",
	)
	require.ErrorIs(t, err, expectedError)

	modelSnapshot := countingEmbedder.Snapshot()
	require.Equal(t, 1, modelSnapshot.RequestCount)
	require.Equal(t, 2, modelSnapshot.TotalInputItems)

	storedSpan := loadQuestionEmbeddingObservationSpan(
		t,
		db,
		"knowledge-question-embedding-failure",
		attempt,
		"postprocess.question.embedding",
	)
	require.Equal(t, types.SpanStatusFailed, storedSpan.Status)
	require.Equal(t, "QUESTION_EMBEDDING_FAILED", storedSpan.ErrorCode)
	require.Contains(
		t,
		storedSpan.ErrorDetail,
		"question embedding provider failed",
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
}

func TestQuestionEmbeddingObservation_BatchesUseIndependentSpans(
	t *testing.T,
) {
	ctx := context.Background()
	tracker, db := setupSpanTrackerTest(t)
	const knowledgeID = "knowledge-question-embedding-batches"

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
	questionGroup := tracker.BeginSubSpan(
		ctx,
		postprocessSpan,
		postprocessQuestionGroupSpanName,
		types.SpanKindSubSpan,
		nil,
	)
	require.NotNil(t, questionGroup)

	countingEmbedder := modelcount.NewCountingEmbedder(
		modelcount.CountingEmbedderOptions{
			ModelID:    "question-embedding-test",
			ModelName:  "question-embedding-model",
			Dimensions: 4,
		},
	)

	batchSizes := []int{4, 2}
	for batchIndex, itemCount := range batchSizes {
		batchName := fmt.Sprintf(
			"postprocess.question.batch[%d]",
			batchIndex,
		)
		batchSpan := tracker.BeginSubSpan(
			ctx,
			questionGroup,
			batchName,
			types.SpanKindSubSpan,
			nil,
		)
		require.NotNil(t, batchSpan)

		embeddingSpanName := batchName + ".embedding"
		err := observePostprocessEmbeddingBatch(
			ctx,
			tracker,
			batchSpan,
			embeddingSpanName,
			types.IngestionOperationEmbeddingQuestion,
			countingEmbedder,
			questionEmbeddingObservationIndexer{},
			questionEmbeddingObservationIndexInfoList(itemCount),
			"QUESTION_EMBEDDING_FAILED",
		)
		require.NoError(t, err)

		storedSpan := loadQuestionEmbeddingObservationSpan(
			t,
			db,
			knowledgeID,
			attempt,
			embeddingSpanName,
		)
		require.EqualValues(
			t,
			itemCount,
			storedSpan.Output["total_items"],
		)
		require.EqualValues(
			t,
			itemCount,
			storedSpan.Output["vectors_written"],
		)
		require.EqualValues(
			t,
			1,
			storedSpan.Output["request_count"],
		)
	}

	modelSnapshot := countingEmbedder.Snapshot()
	require.Equal(t, 2, modelSnapshot.RequestCount)
	require.Equal(t, 6, modelSnapshot.TotalInputItems)
}

func beginQuestionEmbeddingObservationParent(
	t *testing.T,
	ctx context.Context,
	tracker SpanTracker,
	knowledgeID string,
	name string,
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
	questionSpan := tracker.BeginSubSpan(
		ctx,
		postprocessSpan,
		name,
		types.SpanKindSubSpan,
		nil,
	)
	require.NotNil(t, questionSpan)

	return questionSpan, attempt
}

func questionEmbeddingObservationIndexInfoList(
	count int,
) []*types.IndexInfo {
	indexInfoList := make([]*types.IndexInfo, 0, count)
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("question-%02d", i)
		indexInfoList = append(indexInfoList, &types.IndexInfo{
			Content:         fmt.Sprintf("stable generated question %02d", i),
			SourceID:        id,
			SourceType:      types.ChunkSourceType,
			ChunkID:         "chunk-question-test",
			KnowledgeID:     "knowledge-question-test",
			KnowledgeBaseID: "kb-question-test",
			IsEnabled:       true,
		})
	}
	return indexInfoList
}

func loadQuestionEmbeddingObservationSpan(
	t *testing.T,
	db *gorm.DB,
	knowledgeID string,
	attempt int,
	name string,
) types.KnowledgeProcessingSpan {
	t.Helper()

	var storedSpan types.KnowledgeProcessingSpan
	require.NoError(
		t,
		db.Where(
			"knowledge_id = ? AND attempt = ? AND name = ?",
			knowledgeID,
			attempt,
			name,
		).Take(&storedSpan).Error,
	)
	require.NotNil(t, storedSpan.Output)

	return storedSpan
}
