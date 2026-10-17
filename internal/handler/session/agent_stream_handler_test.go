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

func TestAgentStreamHandlerForwardsOperationDataChange(t *testing.T) {
	streams := &capturingStreamManager{}
	h := NewAgentStreamHandler(
		context.Background(), "sess-1", "msg-1", "req-1", 1, time.Now(),
		&types.Message{}, streams, nil, nil, nil, nil,
	)
	marker := map[string]any{"operation": "agent.delete", "area": "agent"}
	err := h.handleToolResult(context.Background(), event.Event{
		Type: event.EventAgentToolResult,
		Data: event.AgentToolResultData{
			ToolName: "run_operation", Success: true, Data: map[string]any{"data_change": marker},
		},
	})
	require.NoError(t, err)
	require.Len(t, streams.events, 1)
	require.Equal(t, types.ResponseTypeToolResult, streams.events[0].Type)
	require.Equal(t, marker, streams.events[0].Data["data_change"])
}

func TestAgentStreamHandlerProgressIsSeparateFromReasoningAndFinalAnswer(t *testing.T) {
	streams := &capturingStreamManager{}
	bus := event.NewEventBus()
	h := NewAgentStreamHandler(
		context.Background(),
		"s",
		"m",
		"r",
		1,
		time.Now(),
		&types.Message{},
		streams,
		bus,
		nil,
		nil,
		nil,
	)
	h.Subscribe()
	require.NoError(
		t,
		bus.Emit(
			t.Context(),
			event.Event{
				ID:   "progress-1",
				Type: event.EventAgentProgress,
				Data: event.AgentProgressData{Content: "Two tasks finished; one awaits approval.", Iteration: 3},
			},
		),
	)
	require.Len(t, streams.events, 1)
	e := streams.events[0]
	require.Equal(t, types.ResponseTypeProgress, e.Type)
	require.Equal(t, "progress-1", e.Data["event_id"])
	require.Equal(t, 3, e.Data["iteration"])
	require.True(t, e.Done)
	require.Empty(t, h.finalAnswer)
	require.Empty(t, h.answerSegments)
	require.Empty(t, h.eventStartTimes)
}

func TestProtocolAnswerReplacementOnlyResetsMatchingSegment(t *testing.T) {
	streams := &capturingStreamManager{}
	h := NewAgentStreamHandler(
		context.Background(),
		"s",
		"m",
		"r",
		1,
		time.Now(),
		&types.Message{},
		streams,
		nil,
		nil,
		nil,
		nil,
	)
	send := func(id, content string, replace bool) {
		require.NoError(
			t,
			h.handleFinalAnswer(context.Background(), event.Event{
				ID: id, Type: event.EventAgentFinalAnswer,
				Data: event.AgentFinalAnswerData{Content: content, Replace: replace},
			}),
		)
	}
	send("prior", "Earlier legitimate answer.", false)
	send("current", "refs\nprivate prefix", false)
	send("current", "", true)
	send("current", "## 进展\ncomplete report", false)
	require.Equal(t, "Earlier legitimate answer.", h.findAnswerSegment("prior").content)
	require.Equal(t, "## 进展\ncomplete report", h.findAnswerSegment("current").content)
	require.NotContains(t, h.finalAnswer, "private prefix")
	require.Equal(t, true, streams.events[2].Data["replace"])
	require.Equal(t, "current", streams.events[2].Data["event_id"])
}
