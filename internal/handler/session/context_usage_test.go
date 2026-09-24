package session

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextUsageReachesChatStreamWithoutCompletingTurn(t *testing.T) {
	ctx := context.Background()
	bus := event.NewEventBus()
	streams := &completionEventRecorder{}
	message := &types.Message{ID: "message"}
	h := NewAgentStreamHandler(
		ctx, "session", "message", "request", 1, time.Now(),
		message, streams, bus, nil, nil, nil,
	)
	h.Subscribe()

	usage := types.ContextUsage{SystemPrompt: 20, Conversation: 80, Total: 100, Window: 200000}
	require.NoError(t, bus.Emit(ctx, event.Event{
		Type: event.EventAgentContextUsage,
		Data: usage,
	}))

	require.Len(t, streams.events, 1)
	require.Equal(t, types.ResponseTypeContextUsage, streams.events[0].Type)
	require.False(t, streams.events[0].Done, "a live snapshot is not a completed assistant turn")
	require.NotNil(t, streams.events[0].Usage)
	require.Equal(t, usage, streams.events[0].Usage.Context)
	require.Nil(t, message.Usage, "live snapshots ride SSE only; mutating the in-memory message races the stop path")

	frame := buildStreamResponse(streams.events[0], "request")
	require.Equal(t, types.ResponseTypeContextUsage, frame.ResponseType)
	require.False(t, frame.Done)
	require.Equal(t, 200000, frame.Usage.Context.Window)
}

func TestBuildStreamResponsePromotesUsageOnContextUsageEvents(t *testing.T) {
	usage := &types.TokenUsage{}
	usage.Context = types.ContextUsage{SystemPrompt: 20, Conversation: 80, Total: 100, Window: 200000}

	response := buildStreamResponse(interfaces.StreamEvent{
		Type:  types.ResponseTypeContextUsage,
		Done:  false,
		Data:  map[string]interface{}{"usage": usage},
		Usage: usage,
	}, "req-1")

	require.NotNil(t, response.Usage)
	assert.Equal(t, 100, response.Usage.Context.Total)
	assert.Equal(t, 200000, response.Usage.Context.Window)
	assert.Equal(t, usage, response.Data["usage"])
	assert.False(t, response.Done)
}
