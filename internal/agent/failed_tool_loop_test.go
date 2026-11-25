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

type alwaysFailingTool struct {
	agenttools.BaseTool
	calls int
}

func (t *alwaysFailingTool) Execute(context.Context, json.RawMessage) (*types.ToolResult, error) {
	t.calls++
	return &types.ToolResult{Success: false, Error: "browser unavailable"}, nil
}

func failedToolResponse(id string) mockResponse {
	return mockResponse{chunks: []types.StreamResponse{{
		ResponseType: types.ResponseTypeAnswer,
		ToolCalls: []types.LLMToolCall{{
			ID:   id,
			Type: "function",
			Function: types.FunctionCall{
				Name:      "failing_browser",
				Arguments: `{"url":"https://example.com"}`,
			},
		}},
		Done:         true,
		FinishReason: "tool_calls",
	}}}
}

func TestExecuteLoopPromptsForAlternativeAfterRepeatedFailedToolCall(t *testing.T) {
	model := &mockChat{responses: []mockResponse{
		failedToolResponse("call-1"),
		failedToolResponse("call-2"),
		failedToolResponse("call-3"),
		{chunks: []types.StreamResponse{{
			ResponseType: types.ResponseTypeAnswer,
			Content:      "I will try another approach.",
			Done:         true,
			FinishReason: "stop",
		}}},
	}}
	engine := newTestEngine(t, model)
	tool := &alwaysFailingTool{BaseTool: agenttools.NewBaseTool(
		"failing_browser", "always fails in this test", json.RawMessage(`{"type":"object"}`),
	)}
	engine.toolRegistry = agenttools.NewToolRegistry()
	engine.toolRegistry.RegisterTool(tool)

	state, err := engine.executeLoop(context.Background(), &types.AgentState{}, "visit the website",
		emptyMessages(), emptyTools(), "session", "message")

	require.NoError(t, err)
	require.True(t, state.IsComplete)
	require.Equal(t, 3, tool.calls, "identical failed calls must be bounded before trying again")
	require.Len(t, model.calls, 4)

	var retryGuidance strings.Builder
	for _, message := range model.calls[3] {
		if message.Role == "user" {
			retryGuidance.WriteString(message.Content)
		}
	}
	require.Contains(t, retryGuidance.String(), "Do not call it again with those arguments")
	require.Contains(t, retryGuidance.String(), "Switch to a different tool or use another approach")
}

func TestExecuteLoopStopsAfterRepeatedFailedToolCallNudgeIsIgnored(t *testing.T) {
	responses := make([]mockResponse, maxConsecutiveFailedToolCallStop+1)
	for i := range maxConsecutiveFailedToolCallStop {
		responses[i] = failedToolResponse("call-" + string(rune('1'+i)))
	}
	responses[maxConsecutiveFailedToolCallStop] = mockResponse{chunks: []types.StreamResponse{{
		ResponseType: types.ResponseTypeAnswer,
		Content:      "this response must never be requested",
		Done:         true,
		FinishReason: "stop",
	}}}

	model := &mockChat{responses: responses}
	engine := newTestEngine(t, model, func(cfg *types.AgentConfig) { cfg.MaxIterations = -1 })
	tool := &alwaysFailingTool{BaseTool: agenttools.NewBaseTool(
		"failing_browser", "always fails in this test", json.RawMessage(`{"type":"object"}`),
	)}
	engine.toolRegistry = agenttools.NewToolRegistry()
	engine.toolRegistry.RegisterTool(tool)

	state, err := engine.executeLoop(context.Background(), &types.AgentState{}, "visit the website",
		emptyMessages(), emptyTools(), "session", "message")

	require.NoError(t, err)
	require.True(t, state.IsComplete)
	require.Equal(t, stalledAnswerFallback, state.FinalAnswer)
	require.Equal(t, maxConsecutiveFailedToolCallStop, tool.calls)
	require.Len(t, model.calls, maxConsecutiveFailedToolCallStop,
		"the hard stop must end an unlimited turn without another model round")
}

func TestLoopGuardsResetFailedToolCallStreakOnSuccessOrDifferentCall(t *testing.T) {
	failed := types.ToolCall{
		Name:   "wiki_replace_text",
		Args:   map[string]interface{}{"old_text": "missing"},
		Result: &types.ToolResult{Success: false},
	}
	succeeded := failed
	succeeded.Result = &types.ToolResult{Success: true}
	different := failed
	different.Args = map[string]interface{}{"old_text": "other"}

	guards := &loopGuards{}
	guards.recordFailedToolCall(failed)
	guards.recordFailedToolCall(failed)
	_, count, action := guards.recordFailedToolCall(succeeded)
	require.Zero(t, count)
	require.Equal(t, failedToolCallContinue, action)

	guards.recordFailedToolCall(failed)
	_, count, action = guards.recordFailedToolCall(different)
	require.Equal(t, 1, count)
	require.Equal(t, failedToolCallContinue, action)
}
