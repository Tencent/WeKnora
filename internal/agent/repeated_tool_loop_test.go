package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sequenceTool succeeds every time and returns outputs[i] on the i-th call,
// repeating the last one once the list runs out.
type sequenceTool struct {
	agenttools.BaseTool
	outputs []string
	calls   int
}

func newSequenceTool(name string, outputs ...string) *sequenceTool {
	return &sequenceTool{
		BaseTool: agenttools.NewBaseTool(name, "test", json.RawMessage(`{"type":"object"}`)),
		outputs:  outputs,
	}
}

func (t *sequenceTool) Execute(context.Context, json.RawMessage) (*types.ToolResult, error) {
	out := t.outputs[len(t.outputs)-1]
	if t.calls < len(t.outputs) {
		out = t.outputs[t.calls]
	}
	t.calls++
	return &types.ToolResult{Success: true, Output: out}, nil
}

func toolRound(name, args string) mockResponse {
	return mockResponse{chunks: []types.StreamResponse{{
		ResponseType: types.ResponseTypeAnswer,
		ToolCalls: []types.LLMToolCall{{
			ID: "call", Type: "function",
			Function: types.FunctionCall{Name: name, Arguments: args},
		}},
		Done: true, FinishReason: "tool_calls",
	}}}
}

// The runtime notice reaches the model through ChatOptions, not the history.
func hasRepeatedToolNotice(opts *chat.ChatOptions) bool {
	return opts != nil && strings.Contains(opts.RuntimeNotice, `"type":"repeated_tool_calls"`)
}

// The loop from the report: unlimited iterations, and the model keeps issuing
// the same shell command whose result is a successful empty match. The model
// is told after the notice threshold and the turn stops at the hard limit.
// mockChat fails the test if the loop asks for one round more than that.
func TestExecuteLoop_RepeatedSuccessfulToolCallsStopAtTheGuard(t *testing.T) {
	rounds := make([]mockResponse, maxRepeatedToolRounds)
	for i := range rounds {
		rounds[i] = toolRound("shell_exec", `{"command":"grep -n x f | awk 'NR>10'"}`)
	}
	mock := &mockChat{responses: rounds}
	engine := newTestEngine(t, mock, func(cfg *types.AgentConfig) {
		cfg.MaxIterations = types.UnlimitedMaxIterations
	})
	engine.toolRegistry = agenttools.NewToolRegistry()
	tool := newSequenceTool("shell_exec", "")
	engine.toolRegistry.RegisterTool(tool)

	state := &types.AgentState{}
	_, err := engine.executeLoop(context.Background(), state, "q", emptyMessages(), nil, "sess", "msg")

	require.NoError(t, err)
	assert.Equal(t, maxRepeatedToolRounds, mock.callCount,
		"the guard must stop the turn, not MaxIterations")
	assert.Equal(t, maxRepeatedToolRounds, tool.calls)
	assert.True(t, state.IsComplete)
	assert.Equal(t, stalledAnswerFallback, state.FinalAnswer)

	for i, opts := range mock.opts {
		want := i == repeatedToolRoundNoticeAt
		assert.Equal(t, want, hasRepeatedToolNotice(opts),
			"notice must be sent once, on the round right after the threshold (call %d)", i)
	}
}

// Polling the same call is fine while its result keeps changing.
func TestExecuteLoop_RepeatedCallsWithChangingResultsKeepRunning(t *testing.T) {
	n := maxRepeatedToolRounds + 2
	rounds := make([]mockResponse, 0, n+1)
	outputs := make([]string, 0, n)
	for i := 0; i < n; i++ {
		rounds = append(rounds, toolRound("probe", `{}`))
		outputs = append(outputs, fmt.Sprintf("progress %d", i))
	}
	rounds = append(rounds, answer("done"))
	mock := &mockChat{responses: rounds}
	engine := newTestEngine(t, mock, func(cfg *types.AgentConfig) {
		cfg.MaxIterations = types.UnlimitedMaxIterations
	})
	engine.toolRegistry = agenttools.NewToolRegistry()
	engine.toolRegistry.RegisterTool(newSequenceTool("probe", outputs...))

	state := &types.AgentState{}
	_, err := engine.executeLoop(context.Background(), state, "q", emptyMessages(), nil, "sess", "msg")

	require.NoError(t, err)
	assert.Equal(t, n+1, mock.callCount)
	assert.Equal(t, "done", state.FinalAnswer)
	for i, opts := range mock.opts {
		assert.False(t, hasRepeatedToolNotice(opts), "no notice expected (call %d)", i)
	}
}

func TestTrackRepeatedToolRound(t *testing.T) {
	call := func(args map[string]interface{}, out string) []types.ToolCall {
		return []types.ToolCall{{Name: "shell_exec", Args: args, Result: &types.ToolResult{Success: true, Output: out}}}
	}
	a := map[string]interface{}{"command": "ls"}
	b := map[string]interface{}{"command": "pwd"}

	g := &loopGuards{}
	assert.Equal(t, 1, g.trackRepeatedToolRound(call(a, "")))
	assert.Equal(t, 2, g.trackRepeatedToolRound(call(a, "")))
	assert.Equal(t, 1, g.trackRepeatedToolRound(call(b, "")), "different arguments restart the streak")
	assert.Equal(t, 1, g.trackRepeatedToolRound(call(b, "x")), "a different result restarts the streak")
	assert.Equal(t, 0, g.trackRepeatedToolRound(nil), "a round without calls breaks the streak")
	assert.Equal(t, 1, g.trackRepeatedToolRound(call(b, "x")))
}
