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
