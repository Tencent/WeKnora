package agent

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
)

// A tool that hands adapter work to a goroutine of its own must contain the
// panic there: executeRecovered (registry.go) only covers the goroutine that
// calls tool.Execute, so before the child barrier a panic from an MCP client,
// a retrieval adapter or an SDK killed the whole process instead of failing
// one tool call.
type goroutineBarrierPanicTool struct {
	agenttools.BaseTool
}

func (t *goroutineBarrierPanicTool) Execute(ctx context.Context, _ json.RawMessage) (*types.ToolResult, error) {
	var panicked atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		// RecoverGoroutine runs before close(done) and marks the failed unit of
		// work; this is the convention tools use in production.
		defer agenttools.RecoverGoroutine(ctx, "test adapter", func() { panicked.Store(true) })
		panic("test: tool adapter panicked on a child goroutine")
	}()
	<-done
	if panicked.Load() {
		return &types.ToolResult{Success: false, Error: "adapter failed with an internal error"}, nil
	}
	return &types.ToolResult{Success: true}, nil
}

func runAgentToolCall(t *testing.T, tool types.Tool) *types.AgentStep {
	t.Helper()
	engine := newTestEngine(t, &mockChat{})
	engine.toolRegistry = agenttools.NewToolRegistry()
	engine.toolRegistry.RegisterTool(tool)
	response := &types.ChatResponse{ToolCalls: []types.LLMToolCall{
		{ID: "a", Function: types.FunctionCall{Name: agenttools.ToolSearchKnowledge, Arguments: `{}`}},
	}}
	step := &types.AgentStep{}
	engine.executeToolCallsParallel(context.Background(), response, step, 0, "session", "message")
	return step
}

// A child-goroutine panic is degraded into a failed tool result and the test
// process survives; removing the barrier makes this binary exit with the panic.
func TestToolChildGoroutinePanicDegradesToFailedResult(t *testing.T) {
	tool := &goroutineBarrierPanicTool{
		BaseTool: agenttools.NewBaseTool(
			agenttools.ToolSearchKnowledge, "", json.RawMessage(`{"type":"object"}`),
		),
	}
	step := runAgentToolCall(t, tool)
	if len(step.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call record, got %d", len(step.ToolCalls))
	}
	result := step.ToolCalls[0].Result
	if result == nil || result.Success {
		t.Fatalf("expected a failed tool result, got %+v", result)
	}
	if result.Error == "" {
		t.Fatal("failed result must carry an error the model can act on")
	}
}
