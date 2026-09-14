package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type orderedTestTool struct {
	tools.BaseTool
	run func(context.Context) *types.ToolResult
}

func (t *orderedTestTool) Execute(ctx context.Context, _ json.RawMessage) (*types.ToolResult, error) {
	return t.run(ctx), nil
}

func TestParallelReadsRespectMutationBarriers(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	engine.toolRegistry = tools.NewToolRegistry()
	var mu sync.Mutex
	var order []string
	readStarts := make(chan struct{}, 2)
	allowReads := make(chan struct{})
	register := func(name string, run func(context.Context)) {
		engine.toolRegistry.RegisterTool(&orderedTestTool{
			BaseTool: tools.NewBaseTool(name, "", json.RawMessage(`{"type":"object"}`)),
			run: func(ctx context.Context) *types.ToolResult {
				run(ctx)
				mu.Lock()
				order = append(order, name)
				mu.Unlock()
				return &types.ToolResult{Success: true}
			},
		})
	}
	for _, name := range []string{tools.ToolKnowledgeSearch, tools.ToolGrepChunks} {
		register(name, func(ctx context.Context) {
			readStarts <- struct{}{}
			select {
			case <-allowReads:
			case <-ctx.Done():
			}
		})
	}
	register(tools.ToolWriteSandboxFile, func(context.Context) {})
	register(tools.ToolShellExec, func(context.Context) {})
	register(tools.ToolReadFile, func(context.Context) {})
	register("mcp_unknown_mutation", func(context.Context) {})
	names := []string{tools.ToolKnowledgeSearch, tools.ToolGrepChunks, tools.ToolWriteSandboxFile, tools.ToolShellExec, tools.ToolReadFile, "mcp_unknown_mutation"}
	response := &types.ChatResponse{}
	for _, name := range names {
		response.ToolCalls = append(response.ToolCalls, types.LLMToolCall{ID: name, Function: types.FunctionCall{Name: name, Arguments: `{}`}})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	step := &types.AgentStep{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		engine.executeToolCallsParallel(ctx, response, step, 0, "session", "message")
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-readStarts:
		case <-ctx.Done():
			t.Fatal("independent reads did not run concurrently")
		}
	}
	close(allowReads)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("execution did not finish")
	}
	require.ElementsMatch(t, names[:2], order[:2])
	require.Equal(t, names[2:], order[2:], "write, execute, read and unknown mutation must retain order")
	for i, call := range step.ToolCalls {
		require.Equal(t, names[i], call.Name, "result events retain original order")
	}
}
