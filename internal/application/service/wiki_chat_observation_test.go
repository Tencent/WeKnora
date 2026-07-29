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

func TestWikiChatObservation_ClassifiesOperationAndCountsRequest(t *testing.T) {
	counting := &modelcount.CountingChat{Response: "wiki result", ModelID: "chat-test"}
	observed := newIngestionObservedChat(counting, types.IngestionOperationWikiSummary)

	response, err := observed.Chat(
		context.Background(),
		[]chat.Message{{Role: "user", Content: "wiki input"}},
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, "wiki result", response.Content)

	providerSnapshot := counting.Snapshot()
	requestSnapshot := observed.Snapshot()
	require.Equal(t, 1, providerSnapshot.RequestCount)
	require.Equal(t, types.IngestionOperationWikiSummary, providerSnapshot.Calls[0].Operation)
	require.Equal(t, providerSnapshot.RequestCount, requestSnapshot.RequestCount)
	require.Equal(t, providerSnapshot.InputChars, requestSnapshot.InputChars)
	require.Equal(t, providerSnapshot.OutputChars, requestSnapshot.OutputChars)

	output := chatOperationOutput(
		types.IngestionOperationWikiSummary,
		"postprocess.wiki.summary",
		observed,
		requestSnapshot,
		true,
	)
	require.Equal(t, string(types.IngestionOperationWikiSummary), output["operation"])
	require.Equal(t, "postprocess.wiki.summary", output["stage"])
	require.Equal(t, 1, output["request_count"])
	require.Equal(t, "not_supported", output["cache_status"])
}

func TestWikiChatObservation_NestedOperationDoesNotPolluteOuterCount(t *testing.T) {
	counting := &modelcount.CountingChat{Response: "{}"}
	extract := newIngestionObservedChat(counting, types.IngestionOperationWikiExtract)
	deduplicate := newIngestionObservedChat(extract, types.IngestionOperationWikiDeduplicate)

	_, err := deduplicate.Chat(
		context.Background(),
		[]chat.Message{{Role: "user", Content: "deduplicate"}},
		nil,
	)
	require.NoError(t, err)

	require.Equal(t, 0, extract.Snapshot().RequestCount)
	require.Equal(t, 1, deduplicate.Snapshot().RequestCount)
	providerSnapshot := counting.Snapshot()
	require.Equal(t, types.IngestionOperationWikiDeduplicate, providerSnapshot.Calls[0].Operation)
}

func TestWikiChatObservation_FailureStillCountsRequest(t *testing.T) {
	counting := &modelcount.CountingChat{Err: errors.New("provider failed")}
	observed := newIngestionObservedChat(counting, types.IngestionOperationWikiReduce)

	_, err := observed.Chat(
		context.Background(),
		[]chat.Message{{Role: "user", Content: "reduce"}},
		nil,
	)
	require.EqualError(t, err, "provider failed")

	snapshot := observed.Snapshot()
	require.Equal(t, 1, snapshot.RequestCount)
	require.Equal(t, 1, snapshot.ErrorCount)
	output := chatOperationOutput(
		types.IngestionOperationWikiReduce,
		"postprocess.wiki.page[test]",
		observed,
		snapshot,
		false,
	)
	require.Equal(t, 1, output["request_count"])
	require.Equal(t, 0, output["computed_items"])
	require.Equal(t, false, output["success"])
}

func TestWikiChatObservation_RecomputesSameInputBeforeCache(t *testing.T) {
	counting := &modelcount.CountingChat{Response: "same result"}
	for range 2 {
		observed := newIngestionObservedChat(counting, types.IngestionOperationWikiSummary)
		_, err := observed.Chat(
			context.Background(),
			[]chat.Message{{Role: "user", Content: "unchanged wiki document"}},
			nil,
		)
		require.NoError(t, err)
		require.Equal(t, 1, observed.Snapshot().RequestCount)
	}

	snapshot := counting.Snapshot()
	require.Equal(t, 2, snapshot.RequestCount)
	require.Equal(t, types.IngestionOperationWikiSummary, snapshot.Calls[0].Operation)
	require.Equal(t, types.IngestionOperationWikiSummary, snapshot.Calls[1].Operation)
}
