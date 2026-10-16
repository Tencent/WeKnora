package session

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func newQuickAnswerFailureHarness(
	t *testing.T, msg *types.Message,
) (*sseStreamContext, *imageCompletionMessages, *imageCompletionStream, *bool) {
	t.Helper()
	messages := &imageCompletionMessages{}
	stream := &imageCompletionStream{}
	h := &Handler{messageService: messages, streamManager: stream}
	bus := event.NewEventBus()
	ctx := types.WithExecutionTenant(context.Background(), 1)
	streamHandler := h.setupStreamHandler(ctx, msg.SessionID, msg.ID, "req", 1, time.Now(), msg, bus)
	released := false
	streamCtx := &sseStreamContext{
		eventBus:         bus,
		streamHandler:    streamHandler,
		assistantMessage: msg,
		releaseTurn:      func() { released = true },
	}
	h.registerQuickAnswerFailureHandler(streamCtx, new(bool), 1)
	return streamCtx, messages, stream, &released
}

const testStreamError = `create chat completion: Post "https://model.example:9999/v1/chat/completions": EOF`

// A failed quick-answer turn must persist the error text, complete the
// assistant row, release the turn lease and close the SSE stream with a
// completion event. Without this the row stayed empty and incomplete: the
// live UI showed the error, but a refresh rendered a blank bubble (issue
// #4145).
func TestQuickAnswerFailurePersistsErrorAndCompletesTurn(t *testing.T) {
	msg := &types.Message{ID: "m", SessionID: "s", Role: "assistant"}
	streamCtx, messages, stream, released := newQuickAnswerFailureHarness(t, msg)

	require.NoError(t, streamCtx.eventBus.Emit(context.Background(), event.Event{
		Type: event.EventError,
		Data: event.ErrorData{
			Error:     testStreamError,
			Stage:     "chat_completion_stream",
			SessionID: "s",
		},
	}))

	require.NotNil(t, messages.saved)
	require.Equal(t, testStreamError, messages.saved.Content)
	require.True(t, messages.saved.IsCompleted)
	require.Equal(t, uint64(1), messages.tenant)
	require.True(t, *released)
	last := stream.events[len(stream.events)-1]
	require.Equal(t, types.ResponseTypeComplete, last.Type)
}

// A stream that produced partial text before failing keeps it; the error is
// appended instead of replacing the answer.
func TestQuickAnswerFailureAppendsErrorToPartialAnswer(t *testing.T) {
	msg := &types.Message{ID: "m", SessionID: "s", Role: "assistant", Content: "partial answer"}
	streamCtx, messages, _, released := newQuickAnswerFailureHarness(t, msg)

	require.NoError(t, streamCtx.eventBus.Emit(context.Background(), event.Event{
		Type: event.EventError,
		Data: event.ErrorData{Error: "stream ended early", Stage: "chat_completion_stream"},
	}))

	require.NotNil(t, messages.saved)
	require.Equal(t, "partial answer\n\nstream ended early", messages.saved.Content)
	require.True(t, messages.saved.IsCompleted)
	require.True(t, *released)
}

// After the turn already completed (e.g. a fallback answer), a late error
// event must not re-complete or overwrite the saved answer.
func TestQuickAnswerFailureAfterCompletionIsIgnored(t *testing.T) {
	msg := &types.Message{ID: "m", SessionID: "s", Role: "assistant"}
	messages := &imageCompletionMessages{}
	stream := &imageCompletionStream{}
	h := &Handler{messageService: messages, streamManager: stream}
	bus := event.NewEventBus()
	ctx := types.WithExecutionTenant(context.Background(), 1)
	h.setupStreamHandler(ctx, "s", "m", "req", 1, time.Now(), msg, bus)
	streamCtx := &sseStreamContext{eventBus: bus, assistantMessage: msg}

	handled := true
	h.registerQuickAnswerFailureHandler(streamCtx, &handled, 1)

	require.NoError(t, bus.Emit(ctx, event.Event{
		Type: event.EventError,
		Data: event.ErrorData{Error: "late failure", Stage: "chat_completion_stream"},
	}))
	require.Nil(t, messages.saved)
	require.Equal(t, "", msg.Content)
}

// An error event carrying no message still completes the turn, with a
// non-empty explanation instead of a blank answer.
func TestQuickAnswerFailureWithEmptyErrorUsesFallbackText(t *testing.T) {
	msg := &types.Message{ID: "m", SessionID: "s", Role: "assistant"}
	streamCtx, messages, _, released := newQuickAnswerFailureHarness(t, msg)

	require.NoError(t, streamCtx.eventBus.Emit(context.Background(), event.Event{
		Type: event.EventError,
		Data: event.ErrorData{Stage: "knowledge_qa_execution"},
	}))

	require.NotNil(t, messages.saved)
	require.Equal(t, quickAnswerFailureContent, messages.saved.Content)
	require.True(t, messages.saved.IsCompleted)
	require.True(t, *released)
}
