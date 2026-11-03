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

func toolCallResponse(name, args string) mockResponse {
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
		rounds[i] = toolCallResponse("shell_exec", `{"command":"grep -n x f | awk 'NR>10'"}`)
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
	assert.Equal(t, repeatedToolRoundsStoppedAnswer, state.FinalAnswer,
		"the user is told why the turn stopped")

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
		rounds = append(rounds, toolCallResponse("probe", `{}`))
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

// Output is not the only thing the model sees. Retrieval results ride in
// Data and screenshots in Images; a change there is progress even when the
// text output stays the same.
func TestTrackRepeatedToolRound_ModelVisibleResultFields(t *testing.T) {
	args := map[string]interface{}{"query": "q"}
	for _, tc := range []struct {
		name string
		with func(r *types.ToolResult, i int)
	}{
		{"data", func(r *types.ToolResult, i int) { r.Data = map[string]interface{}{"doc": i} }},
		{"images", func(r *types.ToolResult, i int) {
			r.Images = []string{fmt.Sprintf("data:image/png;base64,%d", i)}
		}},
		{"output files", func(r *types.ToolResult, i int) { r.OutputFiles = []string{fmt.Sprintf("f%d", i)} }},
		{"image descriptions", func(r *types.ToolResult, i int) {
			r.Images = []string{"data:image/png;base64,same"}
			r.ImageDescriptions = []string{fmt.Sprintf("description %d", i)}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &loopGuards{}
			for i := 0; i < maxRepeatedToolRounds+1; i++ {
				r := &types.ToolResult{Success: true, Output: "ok"}
				tc.with(r, i)
				got := g.trackRepeatedToolRound([]types.ToolCall{{Name: "search", Args: args, Result: r}})
				assert.Equal(t, 1, got, "a changed %s is a new result (round %d)", tc.name, i)
			}
		})
	}
}

// In a round with several calls, one changed call makes the round new.
func TestTrackRepeatedToolRound_MultipleCalls(t *testing.T) {
	round := func(second string) []types.ToolCall {
		return []types.ToolCall{
			{Name: "a", Args: map[string]interface{}{}, Result: &types.ToolResult{Success: true, Output: "same"}},
			{Name: "b", Args: map[string]interface{}{}, Result: &types.ToolResult{Success: true, Output: second}},
		}
	}
	g := &loopGuards{}
	assert.Equal(t, 1, g.trackRepeatedToolRound(round("x")))
	assert.Equal(t, 2, g.trackRepeatedToolRound(round("x")))
	assert.Equal(t, 1, g.trackRepeatedToolRound(round("y")), "one changed call restarts the streak")
}

// A nil OutputFiles (not inspected) and an empty one (inspected, none found)
// tell the model different things, so they are different results.
func TestTrackRepeatedToolRound_OutputFilesNilVersusEmpty(t *testing.T) {
	round := func(files []string) []types.ToolCall {
		return []types.ToolCall{{
			Name:   "shell_exec",
			Args:   map[string]interface{}{},
			Result: &types.ToolResult{Success: true, OutputFiles: files},
		}}
	}
	g := &loopGuards{}
	assert.Equal(t, 1, g.trackRepeatedToolRound(round(nil)))
	assert.Equal(t, 1, g.trackRepeatedToolRound(round([]string{})), "empty is not the same as nil")
	assert.Equal(t, 2, g.trackRepeatedToolRound(round([]string{})))
}

// An identical failing call trips both guards. The failed-call guidance from
// #4334 speaks at the 3rd attempt, so the repeated-round notice stays quiet
// there; if the model still does not change course, the turn stops at the
// hard limit instead of running forever.
func TestExecuteLoop_RepeatedFailedCallsGetOneNoticeThenStop(t *testing.T) {
	rounds := make([]mockResponse, maxRepeatedToolRounds)
	for i := range rounds {
		rounds[i] = failedToolResponse(fmt.Sprintf("call-%d", i))
	}
	mock := &mockChat{responses: rounds}
	engine := newTestEngine(t, mock, func(cfg *types.AgentConfig) {
		cfg.MaxIterations = types.UnlimitedMaxIterations
	})
	tool := &alwaysFailingTool{BaseTool: agenttools.NewBaseTool(
		"failing_browser", "always fails in this test", json.RawMessage(`{"type":"object"}`),
	)}
	engine.toolRegistry = agenttools.NewToolRegistry()
	engine.toolRegistry.RegisterTool(tool)

	state := &types.AgentState{}
	_, err := engine.executeLoop(context.Background(), state, "q", emptyMessages(), nil, "sess", "msg")

	require.NoError(t, err)
	assert.Equal(t, maxRepeatedToolRounds, tool.calls)
	assert.Equal(t, repeatedToolRoundsStoppedAnswer, state.FinalAnswer)
	for i, opts := range mock.opts {
		assert.False(t, hasRepeatedToolNotice(opts),
			"the failed-call guidance already covers this streak (call %d)", i)
	}
}

// A parent waiting on a long child task calls wait_agent again and again and
// gets the same timeout flags back each time, while the child keeps running.
// That is legitimate waiting, bounded by the collaboration runtime, so the
// repeated-round guard must neither nudge nor stop it.
func TestExecuteLoop_RepeatedWaitAgentTimeoutsKeepRunning(t *testing.T) {
	n := maxRepeatedToolRounds + 4
	rounds := make([]mockResponse, 0, n+1)
	for i := 0; i < n; i++ {
		rounds = append(rounds, toolCallResponse("wait_agent", `{"timeout_ms":30000}`))
	}
	rounds = append(rounds, answer("children finished"))
	mock := &mockChat{responses: rounds}
	engine := newTestEngine(t, mock, func(cfg *types.AgentConfig) {
		cfg.MaxIterations = types.UnlimitedMaxIterations
	})
	engine.toolRegistry = agenttools.NewToolRegistry()
	tool := newSequenceTool("wait_agent", `{"interrupted":false,"timed_out":true,"updated":false}`)
	engine.toolRegistry.RegisterTool(tool)

	state := &types.AgentState{}
	_, err := engine.executeLoop(context.Background(), state, "q", emptyMessages(), nil, "sess", "msg")

	require.NoError(t, err)
	assert.Equal(t, n, tool.calls)
	assert.Equal(t, n+1, mock.callCount)
	assert.Equal(t, "children finished", state.FinalAnswer)
	for i, opts := range mock.opts {
		assert.False(t, hasRepeatedToolNotice(opts), "no notice expected while waiting (call %d)", i)
	}
}

// A wait_agent round breaks a repeat streak, even mixed with other calls.
func TestTrackRepeatedToolRound_WaitAgentBreaksTheStreak(t *testing.T) {
	shell := types.ToolCall{
		Name: "shell_exec", Args: map[string]interface{}{"command": "ls"},
		Result: &types.ToolResult{Success: true},
	}
	wait := types.ToolCall{
		Name: "wait_agent", Args: map[string]interface{}{},
		Result: &types.ToolResult{Success: true, Output: `{"timed_out":true}`},
	}
	g := &loopGuards{}
	assert.Equal(t, 1, g.trackRepeatedToolRound([]types.ToolCall{shell}))
	assert.Equal(t, 2, g.trackRepeatedToolRound([]types.ToolCall{shell}))
	assert.Equal(t, 0, g.trackRepeatedToolRound([]types.ToolCall{wait}))
	assert.Equal(t, 0, g.trackRepeatedToolRound([]types.ToolCall{shell, wait}))
	assert.Equal(t, 1, g.trackRepeatedToolRound([]types.ToolCall{shell}), "the streak restarts after waiting")
}
