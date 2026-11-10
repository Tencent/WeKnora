package session

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSuspendedRunIsSavedIncompleteAndAnnounced(t *testing.T) {
	messages := &imageCompletionMessages{}
	stream := &imageCompletionStream{}
	h := &Handler{messageService: messages, streamManager: stream}
	bus := event.NewEventBus()
	message := &types.Message{ID: "m", SessionID: "s", Role: "assistant"}
	ctx := types.WithExecutionTenant(context.Background(), 1)
	streamHandler := h.setupStreamHandler(ctx, "s", "m", "req", 1, time.Now(), message, bus)

	expires := time.Now().Add(time.Hour)
	suspension := &types.PromptSuspension{
		PromptID: "p1", Kind: types.AgentPromptKindToolApproval, ExpiresAt: &expires,
	}
	steps := []types.AgentStep{{Iteration: 0, Thought: "checking first", ToolCalls: []types.ToolCall{
		{ID: "call-a", Name: "first_tool", Result: &types.ToolResult{Success: true, Output: "ok"}},
	}}}
	require.NoError(t, bus.Emit(ctx, event.Event{
		ID:   "complete-1",
		Type: event.EventAgentComplete,
		Data: event.AgentCompleteData{
			MessageID: "m", FinalAnswer: "must not be shown", AgentSteps: steps, Suspended: suspension,
		},
	}))
	require.Equal(t, suspension, streamHandler.Suspension())
	require.Empty(t, stream.events, "nothing is announced before the message is saved")

	h.completeStreamAssistantMessage(ctx, &sseStreamContext{
		eventBus: bus, streamHandler: streamHandler, assistantMessage: message,
	}, "the question", "u1")

	require.NotNil(t, messages.saved)
	require.False(t, messages.saved.IsCompleted)
	require.Equal(t, types.MessageRunStateAwaitingInput, messages.saved.RunState)
	require.Len(t, messages.saved.AgentSteps, 1)

	require.Len(t, stream.events, 2, "no fallback answer for a suspended run")
	announced := stream.events[0]
	require.Equal(t, types.ResponseTypeRunSuspended, announced.Type)
	require.Equal(t, "p1", announced.Data["pending_id"])
	require.Equal(t, types.AgentPromptKindToolApproval, announced.Data["kind"])
	require.Equal(t, &expires, announced.Data["expires_at"])

	complete := stream.events[1]
	require.Equal(t, types.ResponseTypeComplete, complete.Type)
	require.True(t, complete.Done)
	require.Equal(t, suspension, complete.Data["suspended"])
}

func TestDetachedRunDoesNotOverwriteOrAnnounceCompletion(t *testing.T) {
	messages := &imageCompletionMessages{}
	stream := &imageCompletionStream{}
	h := &Handler{messageService: messages, streamManager: stream}
	bus := event.NewEventBus()
	message := &types.Message{ID: "m", SessionID: "s", Role: "assistant", Content: "original"}
	ctx := types.WithExecutionTenant(t.Context(), 1)
	handler := h.setupStreamHandler(ctx, "s", "m", "req", 1, time.Now(), message, bus)
	suspension := &types.PromptSuspension{PromptID: "p", Detached: true}
	require.NoError(t, bus.Emit(ctx, event.Event{
		Type: event.EventAgentComplete,
		Data: event.AgentCompleteData{MessageID: "m", FinalAnswer: "stale result", Suspended: suspension},
	}))
	h.completeStreamAssistantMessage(ctx, &sseStreamContext{
		eventBus: bus, streamHandler: handler, assistantMessage: message,
	}, "question", "u")
	require.Equal(t, "original", message.Content)
	require.False(t, message.IsCompleted)
	require.Nil(t, messages.saved)
	require.Empty(t, stream.events)
}
