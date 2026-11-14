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
