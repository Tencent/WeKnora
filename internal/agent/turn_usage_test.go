package agent

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestTurnUsageNilWhenNothingReported(t *testing.T) {
	if turnUsage(nil) != nil {
		t.Fatal("nil state must yield no usage")
	}
	if turnUsage(&types.AgentState{}) != nil {
		t.Fatal("a turn whose rounds reported no usage must omit the field entirely")
	}
}

func TestTurnUsageAttachesLastRoundContext(t *testing.T) {
	state := &types.AgentState{}
	state.TurnUsage.Accumulate(types.TokenUsage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120})
	state.ContextUsage = types.ContextUsage{
		SystemPrompt: 40, Tools: 10, Conversation: 50, Total: 100, Window: 200000,
	}

	usage := turnUsage(state)
	if usage == nil {
		t.Fatal("expected usage")
	}
	if usage.Context.Total != 100 || usage.Context.Window != 200000 || usage.Context.Conversation != 50 {
		t.Fatalf("context not attached: %+v", usage.Context)
	}

	state.ContextUsage.Conversation = 1
	if usage.Context.Conversation != 50 {
		t.Fatalf("emitted context must be detached from state: %+v", usage.Context)
	}
}

func TestTurnUsageEmitsContextWithoutBillingTokens(t *testing.T) {
	state := &types.AgentState{
		ContextUsage: types.ContextUsage{SystemPrompt: 20, Total: 20, Window: 128000},
	}
	usage := turnUsage(state)
	if usage == nil || usage.Context.Total != 20 || usage.TotalTokens != 0 {
		t.Fatalf("context-only turns must still be emitted: %+v", usage)
	}
}

func TestTurnUsageCopiesTheAggregate(t *testing.T) {
	state := &types.AgentState{}
	state.TurnUsage.Accumulate(types.TokenUsage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120})

	usage := turnUsage(state)
	if usage == nil || usage.TotalTokens != 120 {
		t.Fatalf("aggregate not propagated: %+v", usage)
	}

	// The returned pointer must be a copy: later state mutation must not
	// reach an event that has already been emitted.
	state.TurnUsage.Accumulate(types.TokenUsage{PromptTokens: 1, TotalTokens: 1})
	if usage.TotalTokens != 120 {
		t.Fatalf("emitted usage must be detached from state: %+v", usage)
	}
}

// A token scale is worth persisting on its own: it calibrates the next turn's
// history loading even when the provider reported no token counts.
func TestTurnUsageKeepsAScaleWithoutTokenCounts(t *testing.T) {
	usage := turnUsage(&types.AgentState{TurnUsage: types.TokenUsage{ContextTokenScale: 0.7}})
	if usage == nil || usage.ContextTokenScale != 0.7 {
		t.Fatalf("scale-only usage must be kept: %+v", usage)
	}
}
