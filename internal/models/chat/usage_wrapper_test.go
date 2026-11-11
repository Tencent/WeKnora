package chat

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type usageTestChat struct {
	fakeChat
	responses <-chan types.StreamResponse
}

func (c *usageTestChat) ChatStream(context.Context, []Message, *ChatOptions) (<-chan types.StreamResponse, error) {
	return c.responses, nil
}

func TestUsageChatRecordsFinalSnapshotForInterruptedStream(t *testing.T) {
	stream := make(chan types.StreamResponse, 2)
	stream <- types.StreamResponse{Usage: &types.TokenUsage{PromptTokens: 12, TotalTokens: 12}}
	stream <- types.StreamResponse{
		ResponseType: types.ResponseTypeError, Done: true, FinishReason: types.FinishReasonIncomplete,
		Usage: &types.TokenUsage{PromptTokens: 12, CompletionTokens: 3, TotalTokens: 15},
	}
	close(stream)
	records := make(chan types.ModelTokenUsage, 2)
	model := &usageChat{
		inner: &usageTestChat{fakeChat: fakeChat{id: "test-model"}, responses: stream},
		record: func(_ context.Context, entry types.ModelTokenUsage) error {
			records <- entry
			return nil
		},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10))
	ctx = context.WithValue(ctx, types.SessionTenantIDContextKey, uint64(20))
	out, err := model.ChatStream(ctx, nil, nil)
	require.NoError(t, err)
	var forwarded []types.StreamResponse
	for chunk := range out {
		forwarded = append(forwarded, chunk)
	}
	require.Len(t, forwarded, 2)
	require.Equal(t, types.ResponseTypeError, forwarded[1].ResponseType)
	require.Len(t, records, 1)
	entry := <-records
	require.NotEmpty(t, entry.ID)
	require.Equal(t, uint64(20), entry.TenantID)
	require.Equal(t, "test-model", entry.ModelID)
	require.Equal(t, 15, entry.TotalTokens)
	require.True(t, entry.UsageKnown)
	require.False(t, entry.Completed)
}

func TestUsageChatRecordsCanceledCallWithLiveWriteContext(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10))
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	var recorded types.ModelTokenUsage
	model := &usageChat{
		inner: &fakeChat{},
		record: func(writeCtx context.Context, entry types.ModelTokenUsage) error {
			require.NoError(t, writeCtx.Err())
			recorded = entry
			return errors.New("write unavailable")
		},
	}
	_, err := model.Chat(ctx, nil, nil)
	require.NoError(t, err)
	require.NotEmpty(t, recorded.ID)
	require.False(t, recorded.UsageKnown)
}
