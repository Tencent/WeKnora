package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// emptySuccessTool returns success with no output, modelling a command that ran
// cleanly (e.g. `grep` whose exit code was masked by `awk`) but produced nothing
// the model can act on.
type emptySuccessTool struct {
	agenttools.BaseTool
	calls int
}

func (t *emptySuccessTool) Execute(context.Context, json.RawMessage) (*types.ToolResult, error) {
	t.calls++
	return &types.ToolResult{Success: true, Output: ""}, nil
}

func emptySuccessToolResponse(id string) mockResponse {
	return mockResponse{chunks: []types.StreamResponse{{
		ResponseType: types.ResponseTypeAnswer,
		ToolCalls: []types.LLMToolCall{{
			ID:   id,
			Type: "function",
			Function: types.FunctionCall{
				Name:      "empty_probe",
				Arguments: `{"query":"find the missing line"}`,
			},
		}},
		Done:         true,
		FinishReason: "tool_calls",
	}}}
}

// TestExecuteLoopStopsAfterRepeatedSuccessfulEmptyToolCalls covers #4723: with
// unlimited iterations, a successful tool call that returns no output is
// "success without progress". Six identical calls must end the turn instead of
// letting the prompt grow without bound.
func TestExecuteLoopStopsAfterRepeatedSuccessfulEmptyToolCalls(t *testing.T) {
	model := &mockChat{responses: []mockResponse{
		emptySuccessToolResponse("call-1"),
		emptySuccessToolResponse("call-2"),
		emptySuccessToolResponse("call-3"),
		emptySuccessToolResponse("call-4"),
		emptySuccessToolResponse("call-5"),
		emptySuccessToolResponse("call-6"),
	}}
	engine := newTestEngine(t, model, withMaxIterations(types.UnlimitedMaxIterations))
	tool := &emptySuccessTool{BaseTool: agenttools.NewBaseTool(
		"empty_probe", "succeeds with no output in this test", json.RawMessage(`{"type":"object"}`),
	)}
	engine.toolRegistry = agenttools.NewToolRegistry()
	engine.toolRegistry.RegisterTool(tool)

	state, err := engine.executeLoop(context.Background(), &types.AgentState{}, "probe the document",
		emptyMessages(), emptyTools(), "session", "message")

	require.NoError(t, err)
	require.True(t, state.IsComplete, "the stuck loop must end the turn")
	require.Equal(t, 6, tool.calls, "identical successful no-output calls must stop after the hard limit")
	require.Equal(t, stalledAnswerFallback, state.FinalAnswer,
		"a stopped loop with no text must fall back to the stalled answer")
}

// TestExecuteLoopNudgesAfterRepeatedSuccessfulEmptyToolCalls asserts the one-time
// guidance is injected at the soft limit (3 repeats) before the hard stop.
func TestExecuteLoopNudgesAfterRepeatedSuccessfulEmptyToolCalls(t *testing.T) {
	// 4 rounds: 3 identical successful no-output calls trigger the nudge, then a
	// fourth round's guidance should tell the model to change approach.
	model := &mockChat{responses: []mockResponse{
		emptySuccessToolResponse("call-1"),
		emptySuccessToolResponse("call-2"),
		emptySuccessToolResponse("call-3"),
		// The 4th response is whatever the model would send after the nudge; the
		// assertion below only checks the nudge was injected into the 4th request.
		{chunks: []types.StreamResponse{{
			ResponseType: types.ResponseTypeAnswer,
			Content:      "I will stop here.",
			Done:         true,
			FinishReason: "stop",
		}}},
	}}
	engine := newTestEngine(t, model, withMaxIterations(types.UnlimitedMaxIterations))
	tool := &emptySuccessTool{BaseTool: agenttools.NewBaseTool(
		"empty_probe", "succeeds with no output in this test", json.RawMessage(`{"type":"object"}`),
	)}
	engine.toolRegistry = agenttools.NewToolRegistry()
	engine.toolRegistry.RegisterTool(tool)

	_, err := engine.executeLoop(context.Background(), &types.AgentState{}, "probe the document",
		emptyMessages(), emptyTools(), "session", "message")

	require.NoError(t, err)
	// The nudge must have been injected as a user message before the 4th call.
	found := false
	for _, message := range model.calls[3] {
		if message.Role == "user" && strings.Contains(message.Content, "not making progress") {
			require.Contains(t, message.Content, "Change your approach", "the nudge must ask to change approach")
			found = true
		}
	}
	require.True(t, found, "the soft-limit nudge must be injected after 3 identical successful no-output calls")
}
