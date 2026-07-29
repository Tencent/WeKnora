package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/testutil/modelcount"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestPostprocessSummaryChat_PreCacheBaselineRecomputesSameInput(t *testing.T) {
	counting := &modelcount.CountingChat{Response: "summary"}
	messages := []chat.Message{{Role: "user", Content: "unchanged document"}}

	for range 2 {
		observed := newIngestionObservedChat(counting, types.IngestionOperationPostprocessSummary)
		_, err := observed.Chat(context.Background(), messages, nil)
		require.NoError(t, err)
		require.Equal(t, 1, observed.Snapshot().RequestCount)
	}

	snapshot := counting.Snapshot()
	require.Equal(t, 2, snapshot.RequestCount)
	require.Equal(t, types.IngestionOperationPostprocessSummary, snapshot.Calls[0].Operation)
	require.Equal(t, types.IngestionOperationPostprocessSummary, snapshot.Calls[1].Operation)
}

func TestPostprocessQuestionChat_BatchesUseIndependentCounts(t *testing.T) {
	counting := &modelcount.CountingChat{Response: "question"}
	firstBatch := newIngestionObservedChat(counting, types.IngestionOperationPostprocessQuestion)
	secondBatch := newIngestionObservedChat(counting, types.IngestionOperationPostprocessQuestion)

	_, err := firstBatch.Chat(
		context.Background(),
		[]chat.Message{{Role: "user", Content: "first batch"}},
		nil,
	)
	require.NoError(t, err)
	_, err = secondBatch.Chat(
		context.Background(),
		[]chat.Message{{Role: "user", Content: "second batch"}},
		nil,
	)
	require.NoError(t, err)

	require.Equal(t, 1, firstBatch.Snapshot().RequestCount)
	require.Equal(t, 1, secondBatch.Snapshot().RequestCount)
	require.Equal(t, 2, counting.Snapshot().RequestCount)
}

func TestPostprocessQuestionChat_FailurePreservesRequestCount(t *testing.T) {
	counting := &modelcount.CountingChat{Err: errors.New("provider failed")}
	observed := newIngestionObservedChat(counting, types.IngestionOperationPostprocessQuestion)

	_, err := observed.Chat(
		context.Background(),
		[]chat.Message{{Role: "user", Content: "question input"}},
		nil,
	)
	require.EqualError(t, err, "provider failed")

	snapshot := observed.Snapshot()
	require.Equal(t, 1, snapshot.RequestCount)
	require.Equal(t, 1, snapshot.ErrorCount)
	output := chatOperationOutput(
		types.IngestionOperationPostprocessQuestion,
		"postprocess.question.batch[0]",
		observed,
		snapshot,
		false,
	)
	require.Equal(t, 1, output["request_count"])
	require.Equal(t, 0, output["computed_items"])
	require.Equal(t, false, output["success"])
}
