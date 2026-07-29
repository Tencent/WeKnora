package modelcount

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestCountingChatRecordsRequestsWithoutRetainingContent(t *testing.T) {
	model := &CountingChat{Response: "answer"}
	ctx := types.WithIngestionOperation(context.Background(), types.IngestionOperationPostprocessSummary)

	response, err := model.Chat(ctx, []chat.Message{
		{Role: "system", Content: "summarize"},
		{Role: "user", Content: "document"},
	}, nil)
	require.NoError(t, err)
	require.Equal(t, "answer", response.Content)

	snapshot := model.Snapshot()
	require.Equal(t, 1, snapshot.RequestCount)
	require.Equal(t, 0, snapshot.StreamRequestCount)
	require.Equal(t, 2, snapshot.TotalInputMessages)
	require.Equal(t, len("summarizedocument"), snapshot.InputChars)
	require.Equal(t, len("answer"), snapshot.OutputChars)
	require.Equal(t, types.IngestionOperationPostprocessSummary, snapshot.Calls[0].Operation)
	require.Equal(t, 2, snapshot.Calls[0].MessageCount)
	require.False(t, snapshot.Calls[0].Streaming)
}

func TestCountingChatRecordsFailedRequest(t *testing.T) {
	model := &CountingChat{Err: errors.New("provider failed")}
	_, err := model.Chat(context.Background(), []chat.Message{{Content: "input"}}, nil)
	require.EqualError(t, err, "provider failed")
	require.Equal(t, 1, model.Snapshot().RequestCount)
}

func TestCountingChatRecordsStreamRequest(t *testing.T) {
	model := &CountingChat{Response: "streamed"}
	stream, err := model.ChatStream(context.Background(), []chat.Message{{Content: "input"}}, nil)
	require.NoError(t, err)
	require.Equal(t, "streamed", (<-stream).Content)

	snapshot := model.Snapshot()
	require.Equal(t, 1, snapshot.RequestCount)
	require.Equal(t, 1, snapshot.StreamRequestCount)
	require.True(t, snapshot.Calls[0].Streaming)
}

func TestCountingChatSnapshotIsImmutable(t *testing.T) {
	model := &CountingChat{Response: "answer"}
	_, err := model.Chat(context.Background(), []chat.Message{{Content: "input"}}, nil)
	require.NoError(t, err)

	snapshot := model.Snapshot()
	snapshot.Calls[0].InputChars = 0
	require.Equal(t, len("input"), model.Snapshot().Calls[0].InputChars)
}

func TestCountingChatFailsConfiguredRequest(t *testing.T) {
	model := &CountingChat{Response: "answer", FailOn: 2}
	_, err := model.Chat(context.Background(), nil, nil)
	require.NoError(t, err)
	_, err = model.Chat(context.Background(), nil, nil)
	require.EqualError(t, err, "counting chat configured request failure")
}

func TestCountingChatConcurrentRequests(t *testing.T) {
	model := &CountingChat{Response: "answer"}
	const requests = 32

	var wg sync.WaitGroup
	wg.Add(requests)
	for range requests {
		go func() {
			defer wg.Done()
			_, _ = model.Chat(context.Background(), []chat.Message{{Content: "input"}}, nil)
		}()
	}
	wg.Wait()

	snapshot := model.Snapshot()
	require.Equal(t, requests, snapshot.RequestCount)
	require.Equal(t, requests, len(snapshot.Calls))
}
