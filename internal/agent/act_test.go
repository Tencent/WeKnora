package agent

import (
	"context"
	"encoding/json"
	"testing"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type captureArgsTool struct {
	args json.RawMessage
}

func (t *captureArgsTool) Name() string {
	return "grep_chunksknowledge_search"
}

func (t *captureArgsTool) Description() string {
	return "captures tool arguments"
}

func (t *captureArgsTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"queries":{"type":"array","items":{"type":"string"}},
			"knowledge_base_ids":{"type":"array","items":{"type":"string"}},
			"limit":{"type":"number"}
		},
		"required":["queries","knowledge_base_ids"]
	}`)
}

func (t *captureArgsTool) Execute(_ context.Context, args json.RawMessage) (*types.ToolResult, error) {
	t.args = append(t.args[:0], args...)
	return &types.ToolResult{Success: true, Output: "ok"}, nil
}

func TestRunToolCallRecoversConcatenatedJSONArguments(t *testing.T) {
	tool := &captureArgsTool{}
	registry := agenttools.NewToolRegistry()
	registry.RegisterTool(tool)
	engine := &AgentEngine{
		toolRegistry: registry,
		eventBus:     event.NewEventBus(),
	}

	firstArgs := `{"queries":["急救站.{0,20}质量控制系统|质量控制系统|急救站"],"knowledge_base_ids":["kb-1","kb-2"],"limit":10}`
	secondArgs := `{"queries":["介绍急救站质量控制系统的定位、功能和组成"],"knowledge_base_ids":["kb-1","kb-2"]}`
	call := types.LLMToolCall{
		ID:   "call-1",
		Type: "function",
		Function: types.FunctionCall{
			Name:      tool.Name(),
			Arguments: firstArgs + secondArgs,
		},
	}

	result := engine.runToolCall(context.Background(), call, 0, 0, 1, "session-1", "message-1")

	require.NotNil(t, result.Result)
	require.True(t, result.Result.Success, "unexpected tool error: %s", result.Result.Error)
	require.JSONEq(t, firstArgs, string(tool.args))
	require.Equal(t, []any{"急救站.{0,20}质量控制系统|质量控制系统|急救站"}, result.Args["queries"])
}
