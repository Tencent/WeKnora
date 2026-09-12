package modelcontext

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestLearningAndMCPPoliciesCoexist(t *testing.T) {
	registry := NewRegistry(true)
	require.Equal(t, "b1", registry.RegisterKnowledgeBase("learning-kb"))
	registry.EncodeTools(mcpTestDefinitions())
	registry.ModelToolResultForTool("discover_mcp_tools", &types.ToolResult{
		Success: true,
		Output:  `{"server_id":"` + mcpTestServer + `","tool_ref":"` + mcpTestRef + `"}`,
	})
	for _, name := range []string{"get_learning_profile", "recommend_learning_topics", "prepare_learning_quiz"} {
		t.Run(name, func(t *testing.T) {
			calls := []types.LLMToolCall{
				{Function: types.FunctionCall{Name: name, Arguments: `{"knowledge_base_id":"b1"}`}},
				{Function: types.FunctionCall{Name: "discover_mcp_tools", Arguments: `{"server_id":"ms1"}`}},
				{Function: types.FunctionCall{
					Name:      "call_mcp_tool",
					Arguments: `{"tool_ref":"mt1","arguments":{"knowledge_base_id":"b1"}}`,
				}},
			}
			registry.DecodeToolCalls(calls)
			require.JSONEq(t, `{"knowledge_base_id":"learning-kb"}`, calls[0].Function.Arguments)
			require.JSONEq(t, `{"server_id":"`+mcpTestServer+`"}`, calls[1].Function.Arguments)
			require.JSONEq(t,
				`{"tool_ref":"`+mcpTestRef+`","arguments":{"knowledge_base_id":"b1"}}`,
				calls[2].Function.Arguments,
			)
			for _, call := range calls {
				require.Equal(t, ArgumentResolutionResolved, call.ArgumentResolution)
				require.Empty(t, call.UnresolvedHandles)
			}

			result := &types.ToolResult{Success: true, Output: `{"knowledge_base_id":"learning-kb"}`}
			require.JSONEq(t, `{"knowledge_base_id":"b1"}`, registry.ModelToolResultForTool(name, result))
			require.Equal(t, result.Output, registry.ModelToolResultForTool("call_mcp_tool", result))
		})
	}
}
