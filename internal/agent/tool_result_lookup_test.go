package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestToolResultLookupIncludesPriorRoundsAndCurrentStep(t *testing.T) {
	engine := &AgentEngine{}
	state := &types.AgentState{
		RoundSteps: []types.AgentStep{{
			ToolCalls: []types.ToolCall{{
				ID:     "call_1",
				Result: &types.ToolResult{Success: true, Data: map[string]interface{}{"a": 1}},
			}},
		}},
	}
	current := &types.AgentStep{
		ToolCalls: []types.ToolCall{{
			ID:     "call_2",
			Result: &types.ToolResult{Success: true, Output: `{"b":2}`},
		}},
	}

	lookup := engine.toolResultLookup(state, current)
	require.NotNil(t, lookup)

	first, ok := lookup("call_1")
	require.True(t, ok)
	require.JSONEq(t, `{"a":1}`, string(first))

	second, ok := lookup("call_2")
	require.True(t, ok)
	require.JSONEq(t, `{"b":2}`, string(second))

	_, ok = lookup("missing")
	require.False(t, ok)
}

func TestToolResultLookupIsNilWithoutResults(t *testing.T) {
	engine := &AgentEngine{}
	require.Nil(t, engine.toolResultLookup(&types.AgentState{}, &types.AgentStep{}))
}
