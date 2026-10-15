package session

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// capturingStreamManager records every appended event so a handler's output
// can be asserted without a real stream backend. Embedding the interface keeps
// everything else nil-panicky so an un-stubbed call fails loudly.
type capturingStreamManager struct {
	interfaces.StreamManager
	events []interfaces.StreamEvent
}

func (s *capturingStreamManager) AppendEvent(
	_ context.Context, _, _ string, evt interfaces.StreamEvent,
) error {
	s.events = append(s.events, evt)
	return nil
}

// A failed tool execution is still a tool result: the SSE event must keep
// response_type=tool_result with success=false in its metadata, because
// response_type=error is reserved for internal agent failures (handleError).
func TestAgentStreamHandlerToolResultFailureKeepsToolResultType(t *testing.T) {
	streams := &capturingStreamManager{}
	h := NewAgentStreamHandler(
		context.Background(), "sess-1", "msg-1", "req-1", 1, time.Now(),
		&types.Message{}, streams, nil, nil, nil, nil,
	)

	err := h.handleToolResult(context.Background(), event.Event{
		ID: "evt-1", Type: event.EventAgentToolResult,
		Data: event.AgentToolResultData{
			ToolName:   "shell_exec",
			ToolCallID: "call-1",
			Success:    false,
			Error:      "exit status 1",
		},
	})
	require.NoError(t, err)
	require.Len(t, streams.events, 1)

	evt := streams.events[0]
	require.Equal(t, types.ResponseTypeToolResult, evt.Type)
	require.Equal(t, false, evt.Data["success"])
	require.Equal(t, "exit status 1", evt.Data["error"])
	require.Equal(t, "call-1", evt.Data["tool_call_id"])
}

// Internal error events keep response_type=error with done=true, so clients
// can still distinguish a crashed run from a failed tool call.
func TestAgentStreamHandlerInternalErrorKeepsErrorType(t *testing.T) {
	streams := &capturingStreamManager{}
	h := NewAgentStreamHandler(
		context.Background(), "sess-1", "msg-1", "req-1", 1, time.Now(),
		&types.Message{}, streams, nil, nil, nil, nil,
	)

	err := h.handleError(context.Background(), event.Event{
		ID: "evt-err", Type: event.EventError,
		Data: event.ErrorData{Stage: "think", Error: "llm unavailable"},
	})
	require.NoError(t, err)
	require.Len(t, streams.events, 1)

	evt := streams.events[0]
	require.Equal(t, types.ResponseTypeError, evt.Type)
	require.True(t, evt.Done)
}

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

	// NewEventBus dispatches synchronously, so Emit returns only after the
	// error handler has appended the SSE event.
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
	require.Len(t, recorder.events, 0,
		"no SSE event should be produced when ErrorData type assertion fails")
}

// Compile-time assertion that completionEventRecorder satisfies
// interfaces.StreamManager. Its embedded interface would make the assertion
// pass trivially anyway, but it also guarantees the recorder's overridden
// AppendEvent is what every stream write goes through, so callers that expect
// recorded events can rely on that one method.
var _ interfaces.StreamManager = (*completionEventRecorder)(nil)
