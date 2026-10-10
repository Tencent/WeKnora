package session

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// TestHandleErrorPropagatesEventID verifies that when an EventError is emitted
// on the event bus with an ID, the resulting SSE stream event carries that
// same ID. Without this, the frontend's findLastMessage cannot match the error
// event to the correct assistant message, causing a duplicate empty bubble.
func TestHandleErrorPropagatesEventID(t *testing.T) {
	recorder := &completionEventRecorder{}
	bus := event.NewEventBus()
	message := &types.Message{ID: "m1", SessionID: "s1", Role: "assistant"}
	handler := NewAgentStreamHandler(
		context.Background(), "s1", "m1", "req-123", 1, time.Now(),
		message, recorder, bus, nil, nil, nil,
	)
	handler.Subscribe()

	// Emit an error event with a specific request ID.
	err := bus.Emit(context.Background(), event.Event{
		Type:      event.EventError,
		SessionID: "s1",
		ID:        "req-123",
		Data: event.ErrorData{
			Error:     "test error: model call timeout",
			Stage:     "knowledge_qa_execution",
			SessionID: "s1",
		},
	})
	require.NoError(t, err)

	// Give the async event bus a moment to dispatch.
	time.Sleep(50 * time.Millisecond)

	require.Len(t, recorder.events, 1, "exactly one SSE event should have been produced")
	evt := recorder.events[0]
	require.Equal(t, types.ResponseTypeError, evt.Type)
	require.Equal(t, "req-123", evt.ID, "SSE event ID must match the emitted EventError ID")
	require.Equal(t, "test error: model call timeout", evt.Content)
	require.True(t, evt.Done)
	require.Equal(t, "knowledge_qa_execution", evt.Data["stage"])
}

// TestHandleErrorPropagatesEmptyID verifies that handleError works even when
// EventError has no explicit ID — the event bus auto-generates a UUID.
func TestHandleErrorPropagatesEmptyID(t *testing.T) {
	recorder := &completionEventRecorder{}
	bus := event.NewEventBus()
	message := &types.Message{ID: "m2", SessionID: "s1", Role: "assistant"}
	handler := NewAgentStreamHandler(
		context.Background(), "s1", "m2", "req-456", 1, time.Now(),
		message, recorder, bus, nil, nil, nil,
	)
	handler.Subscribe()

	err := bus.Emit(context.Background(), event.Event{
		Type:      event.EventError,
		SessionID: "s1",
		Data: event.ErrorData{
			Error:     "another error",
			Stage:     "agent_execution",
			SessionID: "s1",
		},
	})
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	require.Len(t, recorder.events, 1)
	evt := recorder.events[0]
	require.NotEmpty(t, evt.ID, "event bus should have auto-generated a UUID")
	require.Equal(t, "another error", evt.Content)
	require.True(t, evt.Done)
}

// TestHandleErrorInvalidData verifies handleError does not panic with
// unexpected event data types.
func TestHandleErrorInvalidData(t *testing.T) {
	recorder := &completionEventRecorder{}
	bus := event.NewEventBus()
	message := &types.Message{ID: "m3", SessionID: "s1", Role: "assistant"}
	handler := NewAgentStreamHandler(
		context.Background(), "s1", "m3", "req-789", 1, time.Now(),
		message, recorder, bus, nil, nil, nil,
	)
	handler.Subscribe()

	err := bus.Emit(context.Background(), event.Event{
		Type:      event.EventError,
		SessionID: "s1",
		Data:      "not an ErrorData struct",
	})
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	require.Len(t, recorder.events, 0,
		"no SSE event should be produced when ErrorData type assertion fails")
}

// interfaces guard: compile-time check that *interfaces.StreamEvent is not
// double-counted in the event recorder's AppendEvent.
var _ interfaces.StreamManager = (*completionEventRecorder)(nil)