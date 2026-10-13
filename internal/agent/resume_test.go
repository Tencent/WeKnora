package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// suspendedSnapshot runs a turn until it suspends on call-b and returns the
// saved run state.
func suspendedSnapshot(t *testing.T) []byte {
	t.Helper()
	f := newSuspendFixture(t, "first_tool", false)
	f.engine.EnableSuspension(json.RawMessage(`{"agent_id":"a1"}`))
	state := f.run(t)
	require.NotNil(t, state.Suspended)
	require.NoError(t, f.blocked.err)
	return f.blocked.snapshot
}

func answeredPrompt(callID string) *types.AgentPrompt {
	return &types.AgentPrompt{
		ID: "p1", Kind: types.AgentPromptKindToolApproval, ToolCallID: callID,
		Status: types.AgentPromptAnswered, Response: types.JSON(`{"approved":true}`),
	}
}

func TestResumeFinishesInterruptedRoundAndContinues(t *testing.T) {
	snapshot := suspendedSnapshot(t)

	// Another replica: fresh engine, the model only answers.
	f := newSuspendFixture(t, "first_tool", false)
	model := f.engine.chatModel.(*mockChat)
	model.responses = []mockResponse{{chunks: []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: "all done", Done: true, FinishReason: "stop"},
	}}}
	f.engine.EnableSuspension(json.RawMessage(`{"agent_id":"a1"}`))

	state, err := f.engine.Resume(context.Background(), "sess-1", "msg-1", nil, snapshot, answeredPrompt("call-b"))
	require.NoError(t, err)
	require.True(t, state.IsComplete)
	require.Nil(t, state.Suspended)
	require.Equal(t, "all done", state.FinalAnswer)

	require.Zero(t, f.first.calls, "calls that finished before the suspension are not rerun")
	require.NotNil(t, f.blocked.answered)
	require.Equal(t, "p1", f.blocked.answered.ID)
	require.Equal(t, 1, f.after.calls)

	require.Len(t, state.RoundSteps, 2)
	round := state.RoundSteps[0]
	require.Equal(t, "checking first", round.Thought)
	require.Len(t, round.ToolCalls, 3)
	for i, id := range []string{"call-a", "call-b", "call-c"} {
		require.Equal(t, id, round.ToolCalls[i].ID)
		require.True(t, round.ToolCalls[i].Result.Success, id)
	}

	// The model sees the original turn and every tool result of the round.
	require.Len(t, model.calls, 1)
	sent := model.calls[0]
	var toolResults []string
	sawQuery := false
	for _, m := range sent {
		if m.Role == "tool" {
			toolResults = append(toolResults, m.ToolCallID)
		}
		if m.Role == "user" && m.Content != "" {
			sawQuery = true
		}
	}
	require.True(t, sawQuery)
	require.Equal(t, []string{"call-a", "call-b", "call-c"}, toolResults)

	require.NotNil(t, f.completed)
	require.Nil(t, f.completed.Suspended)
}

func TestResumeRejectsPromptForAnotherCall(t *testing.T) {
	snapshot := suspendedSnapshot(t)
	f := newSuspendFixture(t, "first_tool", false)
	_, err := f.engine.Resume(context.Background(), "sess-1", "msg-1", nil, snapshot, answeredPrompt("call-c"))
	require.Error(t, err)
	require.Nil(t, f.blocked.answered)
	require.Zero(t, f.after.calls)
}

func TestResumeSuspendsAgainOnALaterPrompt(t *testing.T) {
	snapshot := suspendedSnapshot(t)
	snap, err := DecodeRunSnapshot(snapshot)
	require.NoError(t, err)
	// The call after the blocked one raises a prompt of its own.
	snap.PendingRound.Calls[2].Name = suspendingToolName
	snap.PendingRound.Calls[2].ID = "call-d"
	snapshot, err = json.Marshal(snap)
	require.NoError(t, err)

	f := newSuspendFixture(t, "first_tool", false)
	f.engine.EnableSuspension(json.RawMessage(`{"agent_id":"a1"}`))
	model := f.engine.chatModel.(*mockChat)
	model.responses = nil

	// The blocked call consumes the answer; the next one suspends.
	state, err := f.engine.Resume(context.Background(), "sess-1", "msg-1", nil, snapshot,
		answeredPrompt("call-b"))
	require.NoError(t, err)
	require.NotNil(t, f.blocked.answered)
	require.NotNil(t, state.Suspended)
	require.Empty(t, model.calls, "a re-suspended run makes no model call")

	again, err := DecodeRunSnapshot(f.blocked.snapshot)
	require.NoError(t, err)
	require.Equal(t, 2, again.PendingRound.BlockedIndex)
	require.Len(t, again.PendingRound.Step.ToolCalls, 2)
	require.Equal(t, "call-b", again.PendingRound.Step.ToolCalls[1].ID)

	require.NotNil(t, f.completed)
	require.Equal(t, state.Suspended, f.completed.Suspended)
}

func TestClosedStepsFailUnfinishedCalls(t *testing.T) {
	snap, err := DecodeRunSnapshot(suspendedSnapshot(t))
	require.NoError(t, err)
	steps := snap.ClosedSteps("expired")
	require.Len(t, steps, 1)
	calls := steps[0].ToolCalls
	require.Len(t, calls, 3)
	require.True(t, calls[0].Result.Success)
	for _, c := range calls[1:] {
		require.False(t, c.Result.Success)
		require.Equal(t, "expired", c.Result.Error)
	}
	require.Equal(t, "/etc", calls[1].Args["path"])
}
