package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func withRunBudget(budget RunBudget) testEngineOption {
	return func(cfg *types.AgentConfig) {
		cfg.MaxRunTokens = budget.MaxTotalTokens
		cfg.MaxRunSeconds = budget.MaxWallTimeSec
		cfg.MaxWriteActions = budget.MaxWriteActions
	}
}

func toolCallResponse(name, args string, tokens int) mockResponse {
	return mockResponse{chunks: []types.StreamResponse{{
		ResponseType: types.ResponseTypeAnswer,
		Content:      "working",
		ToolCalls: []types.LLMToolCall{{
			ID: "call-1", Function: types.FunctionCall{Name: name, Arguments: args},
		}},
		Done: true, FinishReason: "tool_calls", Usage: &types.TokenUsage{TotalTokens: tokens},
	}}}
}

func stopResponse(content string) mockResponse {
	return mockResponse{chunks: []types.StreamResponse{{
		ResponseType: types.ResponseTypeAnswer,
		Content:      content,
		Done:         true, FinishReason: "stop",
	}}}
}

func TestRunContractStopsBeforeToolsWhenTokenBudgetIsSpent(t *testing.T) {
	tool := newCountingTool(agenttools.ToolWriteSandboxFile)
	model := &mockChat{responses: []mockResponse{toolCallResponse(
		agenttools.ToolWriteSandboxFile, `{"path":"a.txt","content":"a"}`, 20,
	)}}
	engine := newTestEngine(t, model, withRunBudget(RunBudget{MaxTotalTokens: 10}))
	engine.toolRegistry = agenttools.NewToolRegistry()
	engine.toolRegistry.RegisterTool(tool)

	state, err := engine.Execute(context.Background(), "s", "m", "write a file", nil)
	require.NoError(t, err)
	require.NotNil(t, state.Budget)
	require.Equal(t, RunStopTokenBudget, state.Budget.StopReason)
	require.Zero(t, tool.calls, "no tool may run after the token budget is exhausted")
	require.Contains(t, state.FinalAnswer, "token budget")
	require.Len(t, model.calls, 1, "no synthesis request may follow a token-budget stop")
}

func TestRunContractWriteBudgetIsAtomicAcrossParallelTools(t *testing.T) {
	const writes = 8
	executed := make(chan struct{}, writes)
	engine := newTestEngine(t, &mockChat{}, withRunBudget(RunBudget{MaxWriteActions: 2}))
	engine.config.ParallelToolCalls = true
	engine.toolRegistry = agenttools.NewToolRegistry()
	engine.toolRegistry.RegisterTool(&orderedTestTool{
		BaseTool: agenttools.NewBaseTool(agenttools.ToolWriteSandboxFile, "", json.RawMessage(`{"type":"object"}`)),
		run: func(context.Context) *types.ToolResult {
			executed <- struct{}{}
			return &types.ToolResult{Success: true}
		},
	})
	response := &types.ChatResponse{}
	for i := 0; i < writes; i++ {
		response.ToolCalls = append(response.ToolCalls, types.LLMToolCall{
			ID: "w",
			Function: types.FunctionCall{
				Name:      agenttools.ToolWriteSandboxFile,
				Arguments: `{"path":"a","content":"a"}`,
			},
		})
	}
	engine.runBudget = newRunBudgetState(RunBudget{MaxWriteActions: 2}, time.Now())
	step := &types.AgentStep{}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		engine.executeToolCallsParallel(context.Background(), response, step, 0, "s", "m")
	}()
	wg.Wait()
	close(executed)
	count := 0
	for range executed {
		count++
	}
	require.Equal(t, 2, count)
	require.Equal(t, RunStopWriteBudget, engine.runBudget.snapshot(0).StopReason)
}

func TestRunContractTokenBudgetStopsWithoutAnotherModelCall(t *testing.T) {
	tool := newCountingTool(agenttools.ToolWriteSandboxFile)
	model := &mockChat{responses: []mockResponse{
		toolCallResponse(agenttools.ToolWriteSandboxFile, `{"path":"a","content":"a"}`, 20),
	}}
	engine := newTestEngine(t, model, withRunBudget(RunBudget{MaxTotalTokens: 10}))
	engine.toolRegistry = agenttools.NewToolRegistry()
	engine.toolRegistry.RegisterTool(tool)
	state, err := engine.Execute(context.Background(), "s", "m", "write", nil)
	require.NoError(t, err)
	require.Equal(t, RunStopTokenBudget, state.Budget.StopReason)
	require.Zero(t, tool.calls)
	require.Len(t, model.calls, 1)
	require.Contains(t, state.FinalAnswer, "token budget")
}
func TestRunContractTimeBudgetStopsBeforeNextRound(t *testing.T) {
	engine := newTestEngine(t, &mockChat{}, withRunBudget(RunBudget{MaxWallTimeSec: 1}))
	engine.runBudget = newRunBudgetState(RunBudget{MaxWallTimeSec: 1}, time.Now().Add(-2*time.Second))
	require.Equal(t, RunStopTimeBudget, engine.budgetStopReason(0))
}
func TestRunContractZeroValuesPreserveLegacyExecution(t *testing.T) {
	tool := newCountingTool(agenttools.ToolWriteSandboxFile)
	model := &mockChat{responses: []mockResponse{
		toolCallResponse(agenttools.ToolWriteSandboxFile, `{"path":"a","content":"a"}`, 1),
		stopResponse("done"),
	}}
	engine := newTestEngine(t, model, withRunBudget(RunBudget{}))
	engine.toolRegistry = agenttools.NewToolRegistry()
	engine.toolRegistry.RegisterTool(tool)

	state, err := engine.Execute(context.Background(), "s", "m", "write", nil)
	require.NoError(t, err)
	require.Nil(t, state.Budget)
	require.Equal(t, 1, tool.calls)
}
