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

func TestToolCallIDsRemainDistinctThroughExecutionAndHistory(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		name := "sequential"
		if parallel {
			name = "parallel"
		}
		t.Run(name, func(t *testing.T) {
			engine := newTestEngine(t, &mockChat{})
			engine.config.ParallelToolCalls = parallel
			engine.toolRegistry = tools.NewToolRegistry()
			var outcomes []event.Event
			engine.eventBus.On(event.EventAgentToolResult, func(_ context.Context, evt event.Event) error {
				outcomes = append(outcomes, evt)
				return nil
			})
			response := &types.ChatResponse{FinishReason: "tool_calls"}
			for i, toolName := range []string{tools.ToolSearchKnowledge, tools.ToolReadDocument} {
				engine.toolRegistry.RegisterTool(&orderedTestTool{
					BaseTool: tools.NewBaseTool(toolName, "", json.RawMessage(`{"type":"object"}`)),
					run: func(context.Context) *types.ToolResult {
						return &types.ToolResult{Success: true, Output: toolName}
					},
				})
				response.ToolCalls = append(response.ToolCalls, types.LLMToolCall{
					ID:       []string{"call/a", "call?a"}[i],
					Function: types.FunctionCall{Name: toolName, Arguments: `{}`},
				})
			}
			step := &types.AgentStep{}
			require.Nil(t, engine.executeToolCalls(t.Context(), response, step, 0, "session", "message"))
			require.Len(t, step.ToolCalls, 2)
			require.NotEqual(t, step.ToolCalls[0].ID, step.ToolCalls[1].ID)
			require.Len(t, outcomes, 2)
			require.NotEqual(t, outcomes[0].ID, outcomes[1].ID, "result events must not share an identity")

			messages := engine.appendToolResults(nil, *step)
			require.Len(t, messages, 3)
			require.Len(t, messages[0].ToolCalls, 2)
			for i, call := range step.ToolCalls {
				require.True(t, call.Result.Success)
				result := outcomes[i].Data.(event.AgentToolResultData)
				require.Equal(t, call.ID, result.ToolCallID)
				require.Equal(t, call.Name, result.Output)
				require.Equal(t, call.ID, messages[0].ToolCalls[i].ID)
				require.Equal(t, call.ID, messages[i+1].ToolCallID)
				require.Contains(t, messages[i+1].Content, call.Name)
			}
		})
	}
}
