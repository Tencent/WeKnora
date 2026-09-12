package session

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestCommandOutputReachesChatStreamWithoutCompletingTool(t *testing.T) {
	ctx := context.Background()
	bus := event.NewEventBus()
	streams := &completionEventRecorder{}
	message := &types.Message{ID: "message"}
	h := NewAgentStreamHandler(ctx, "session", "message", "request", 1, time.Now(), message, streams, bus, nil)
	h.Subscribe()
	for _, done := range []bool{false, true} {
		require.NoError(t, bus.Emit(ctx, event.Event{
			Type: event.EventAgentCommandOutput,
			Data: event.CommandOutputData{
				ToolCallID: "call-1", Command: "python analysis.py", Output: "reading CSV", Done: done,
			},
		}))
	}
	require.Len(t, streams.events, 2)
	for _, e := range streams.events {
		require.Equal(t, types.ResponseTypeCommandOutput, e.Type)
		require.False(t, e.Done, "command progress is not a completed assistant turn")
		frame := buildStreamResponse(e, "request")
		require.Equal(t, "call-1", frame.Data["tool_call_id"])
		require.Equal(t, "reading CSV", frame.Data["output"])
	}
	require.Equal(t, true, streams.events[1].Data["done"])
	require.Empty(t, message.Content)
	require.Empty(t, message.AgentSteps)
}
