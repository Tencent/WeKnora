package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type countingTool struct {
	BaseTool
	calls []json.RawMessage
}

func (t *countingTool) Execute(_ context.Context, args json.RawMessage) (*types.ToolResult, error) {
	t.calls = append(t.calls, args)
	return &types.ToolResult{Success: true, Output: "done"}, nil
}

func TestConfirmedToolFollowsAccessLevel(t *testing.T) {
	args := json.RawMessage(`{"slug":"guides/setup"}`)
	run := func(
		level types.AssistantAccessLevel, risk operations.Risk, ctx context.Context,
	) (*countingTool, *fakeApprover, *types.ToolResult) {
		inner := &countingTool{BaseTool: NewBaseTool(ToolWikiDeletePage, "d", json.RawMessage(`{}`))}
		gate := &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p1"}}
		res, err := NewConfirmedTool(inner, risk, operations.Access{Level: level}, gate).Execute(ctx, args)
		require.NoError(t, err)
		return inner, gate, res
	}

	inner, gate, res := run(types.AssistantAccessAutoWrite, operations.RiskWrite, interactiveCtx())
	assert.True(t, res.Success)
	assert.Len(t, inner.calls, 1)
	assert.Empty(t, gate.requests, "auto_write runs a recoverable change directly")

	inner, gate, res = run(types.AssistantAccessAutoWrite, operations.RiskDestructive, interactiveCtx())
	assert.True(t, res.Success)
	assert.Len(t, inner.calls, 1)
	require.Len(t, gate.requests, 1, "deleting always asks")
	assert.Contains(t, gate.requests[0].Description, "guides/setup")
	assert.Equal(t, []string{"p1"}, gate.executed)

	inner, gate, _ = run(types.AssistantAccessConfirm, operations.RiskWrite, interactiveCtx())
	assert.Len(t, gate.requests, 1, "confirm asks before every change")
	assert.Len(t, inner.calls, 1)

	nonInteractive := WithToolExecContext(context.Background(), &ToolExecContext{EventBus: event.NewEventBus()})
	inner, gate, res = run(types.AssistantAccessConfirm, operations.RiskWrite, nonInteractive)
	assert.False(t, res.Success)
	assert.Empty(t, inner.calls, "no way to ask means no change")
	assert.Empty(t, gate.requests)
}

func TestConfirmedToolDeclinedChangeDoesNotRun(t *testing.T) {
	inner := &countingTool{BaseTool: NewBaseTool(ToolWikiWritePage, "d", json.RawMessage(`{}`))}
	gate := &fakeApprover{decision: approval.Decision{Approved: false, Reason: "not now"}}
	tool := NewConfirmedTool(inner, operations.RiskWrite, operations.Access{Level: types.AssistantAccessConfirm}, gate)

	res, err := tool.Execute(interactiveCtx(), json.RawMessage(`{"slug":"a"}`))
	require.NoError(t, err)
	assert.False(t, res.Success)
	assert.Contains(t, res.Error, "not now")
	assert.Empty(t, inner.calls)
}

func TestConfirmedToolHonorsResumedAnswerAtAnyLevel(t *testing.T) {
	inner := &countingTool{BaseTool: NewBaseTool(ToolWikiWritePage, "d", json.RawMessage(`{}`))}
	gate := &fakeApprover{decision: approval.Decision{Approved: false, Reason: "declined earlier"}}
	tool := NewConfirmedTool(inner, operations.RiskWrite,
		operations.Access{Level: types.AssistantAccessAutoWrite}, gate)
	ctx := WithToolExecContext(context.Background(), &ToolExecContext{
		EventBus: event.NewEventBus(),
		Answered: &types.AgentPrompt{Kind: types.AgentPromptKindToolApproval},
	})

	res, err := tool.Execute(ctx, json.RawMessage(`{"slug":"a"}`))
	require.NoError(t, err)
	assert.False(t, res.Success, "a decline given before the level changed still stands")
	assert.Empty(t, inner.calls)
}
