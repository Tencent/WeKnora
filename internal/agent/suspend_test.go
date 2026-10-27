package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

const suspendingToolName = "mcp_svc_danger"

// With operator prompt suspension off, only collaboration waits may checkpoint.
func TestCollaborationOnlySuspensionKeepsPromptsInProcess(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	engine.EnableSuspension(json.RawMessage(`{}`))
	require.NotNil(t, engine.snapshotFunc(0, suspendingToolName))

	engine.RestrictSuspensionToCollaboration()
	require.Nil(t, engine.snapshotFunc(0, suspendingToolName))
	require.Nil(t, engine.snapshotFunc(0, "ask_user"))
	require.NotNil(t, engine.snapshotFunc(0, "wait_agent"))
}

// suspendingTool stands in for an approval-gated MCP tool whose approval
// outlived the hot wait: it saves the run state and suspends.
type suspendingTool struct {
	tools.BaseTool
	snapshot []byte
	err      error
	answered *types.AgentPrompt
}

func (t *suspendingTool) Execute(ctx context.Context, _ json.RawMessage) (*types.ToolResult, error) {
	meta, ok := tools.ToolExecFromContext(ctx)
	if ok && meta.Answered != nil {
		t.answered = meta.Answered
		return &types.ToolResult{Success: true, Output: "approved and ran"}, nil
	}
	if !ok || meta.Snapshot == nil {
		return &types.ToolResult{Success: true, Output: "ran without suspension"}, nil
	}
	t.snapshot, _, t.err = meta.Snapshot()
	return &types.ToolResult{
		Error:   "waiting for the user to approve this tool call",
		Suspend: &types.PromptSuspension{PromptID: "p1", Kind: types.AgentPromptKindToolApproval},
	}, nil
}

type suspendFixture struct {
	engine    *AgentEngine
	first     *countingTool
	blocked   *suspendingTool
	after     *countingTool
	completed *event.AgentCompleteData
}

func newSuspendFixture(t *testing.T, firstName string, parallel bool) *suspendFixture {
	t.Helper()
	model := &mockChat{responses: []mockResponse{{chunks: []types.StreamResponse{{
		ResponseType: types.ResponseTypeAnswer,
		Content:      "checking first",
		ToolCalls: []types.LLMToolCall{
			{ID: "call-a", Type: "function", Function: types.FunctionCall{Name: firstName, Arguments: `{"q":"a"}`}},
			{
				ID: "call-b", Type: "function",
				Function: types.FunctionCall{Name: suspendingToolName, Arguments: `{"path":"/etc"}`},
			},
			{ID: "call-c", Type: "function", Function: types.FunctionCall{Name: firstName + "_after", Arguments: `{}`}},
		},
		Done:         true,
		FinishReason: "tool_calls",
	}}}}}
	f := &suspendFixture{
		engine: newTestEngine(t, model),
		first:  newCountingTool(firstName),
		blocked: &suspendingTool{
			BaseTool: tools.NewBaseTool(suspendingToolName, "", json.RawMessage(`{"type":"object"}`)),
		},
		after: newCountingTool(firstName + "_after"),
	}
	f.engine.config.ParallelToolCalls = parallel
	f.engine.toolRegistry = tools.NewToolRegistry()
	f.engine.toolRegistry.RegisterTool(f.first)
	f.engine.toolRegistry.RegisterTool(f.blocked)
	f.engine.toolRegistry.RegisterTool(f.after)
	f.engine.eventBus.On(event.EventAgentComplete, func(_ context.Context, evt event.Event) error {
		data := evt.Data.(event.AgentCompleteData)
		f.completed = &data
		return nil
	})
	return f
}

func (f *suspendFixture) run(t *testing.T) *types.AgentState {
	t.Helper()
	state := &types.AgentState{}
	f.engine.beginTurn("test query", []string{"https://img/1.png"}, state)
	_, err := f.engine.executeLoop(context.Background(), state, "test query",
		emptyMessages(), emptyTools(), "sess-1", "msg-1")
	require.NoError(t, err)
	return state
}

func TestSuspendedToolCallEndsTheRun(t *testing.T) {
	for _, tc := range []struct {
		name      string
		firstTool string
		parallel  bool
	}{
		{name: "sequential", firstTool: "first_tool"},
		{name: "parallel", firstTool: tools.ToolSearchKnowledge, parallel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSuspendFixture(t, tc.firstTool, tc.parallel)
			f.engine.EnableSuspension(json.RawMessage(`{"agent_id":"a1"}`))
			state := f.run(t)

			require.NotNil(t, state.Suspended)
			require.Equal(t, "p1", state.Suspended.PromptID)
			require.False(t, state.IsComplete)
			require.Empty(t, state.FinalAnswer, "a suspended run synthesizes no answer")
			require.Empty(t, state.RoundSteps, "the interrupted round finishes on resume")
			require.Equal(t, 1, f.first.calls)
			require.Zero(t, f.after.calls, "calls after the blocked one must not run")

			require.NotNil(t, f.completed)
			require.Equal(t, state.Suspended, f.completed.Suspended)
			steps := f.completed.AgentSteps.([]types.AgentStep)
			require.Len(t, steps, 1)
			require.Len(t, steps[0].ToolCalls, 1)
			require.Equal(t, tc.firstTool, steps[0].ToolCalls[0].Name)

			require.NoError(t, f.blocked.err)
			snap, err := DecodeRunSnapshot(f.blocked.snapshot)
			require.NoError(t, err)
			require.JSONEq(t, `{"agent_id":"a1"}`, string(snap.Request))
			require.Equal(t, "test query", snap.Query)
			require.Equal(t, []string{"https://img/1.png"}, snap.ImageURLs)
			require.Equal(t, 1, snap.PendingRound.BlockedIndex)
			require.Len(t, snap.PendingRound.Calls, 3)
			require.Equal(t, suspendingToolName, snap.PendingRound.Calls[1].Name)
			require.JSONEq(t, `{"path":"/etc"}`, snap.PendingRound.Calls[1].Arguments)
			require.Len(t, snap.PendingRound.Step.ToolCalls, 1)
			require.Equal(t, tc.firstTool, snap.PendingRound.Step.ToolCalls[0].Name)
			require.Len(t, snap.DisplaySteps(), 1)
		})
	}
}

func TestRunWithoutSuspensionHasNoSnapshotHook(t *testing.T) {
	f := newSuspendFixture(t, "first_tool", false)
	// One more response: the run continues after the tools.
	model := f.engine.chatModel.(*mockChat)
	model.responses = append(model.responses, mockResponse{chunks: []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: "done", Done: true, FinishReason: "stop"},
	}})

	state := f.run(t)
	require.Nil(t, state.Suspended)
	require.True(t, state.IsComplete)
	require.Nil(t, f.blocked.snapshot)
	require.Equal(t, 1, f.after.calls)
}

func TestStripJSONNUL(t *testing.T) {
	data, err := json.Marshal(map[string]string{"a": "x\x00y", "b": `lit\u0000`})
	require.NoError(t, err)
	out := stripJSONNUL(data)
	var got map[string]string
	require.NoError(t, json.Unmarshal(out, &got))
	require.Equal(t, "xy", got["a"])
	require.Equal(t, `lit\u0000`, got["b"], "an escaped backslash is not a NUL escape")

	clean := []byte(`{"a":"b"}`)
	require.Equal(t, clean, stripJSONNUL(clean))
}
