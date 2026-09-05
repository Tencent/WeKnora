package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type outcomeTool struct {
	BaseTool
	result *types.ToolResult
	err    error
}

func (t *outcomeTool) Execute(context.Context, json.RawMessage) (*types.ToolResult, error) {
	return t.result, t.err
}

func TestRegistryPreservesFailureWithoutEncouragingToolHopping(t *testing.T) {
	registry := NewToolRegistry()
	registry.RegisterTool(&outcomeTool{
		BaseTool: NewBaseTool("denied", "", json.RawMessage(`{"type":"object"}`)),
		result:   &types.ToolResult{Success: false, Error: "permission denied"},
	})
	result, err := registry.ExecuteTool(context.Background(), "denied", json.RawMessage(`{}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Equal(t, "permission denied", result.Error)
}

func TestRegistryRedirectsRetiredSkillTools(t *testing.T) {
	registry := NewToolRegistry()
	for _, name := range []string{LegacyToolExecuteSkillScript, LegacyToolReadSkill, LegacyToolReadSandboxFile} {
		result, err := registry.ExecuteTool(context.Background(), name, json.RawMessage(`{}`))
		require.NoError(t, err)
		require.False(t, result.Success)
		require.Equal(t, RetiredToolReplacement(name), result.Error)
		require.NotContains(t, result.Error, "tool not found")
	}
	result, err := registry.ExecuteTool(context.Background(), "not_a_tool", json.RawMessage(`{}`))
	require.Error(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "tool not found")
}

func TestRegistryNormalizesMissingAndContradictoryResults(t *testing.T) {
	for _, outcome := range []*outcomeTool{
		{result: nil},
		{result: &types.ToolResult{Success: true}, err: errors.New("transport failed")},
	} {
		outcome.BaseTool = NewBaseTool("test", "", json.RawMessage(`{"type":"object"}`))
		registry := NewToolRegistry()
		registry.RegisterTool(outcome)
		result, _ := registry.ExecuteTool(context.Background(), "test", json.RawMessage(`{}`))
		require.NotNil(t, result)
		require.False(t, result.Success)
		require.NotEmpty(t, result.Error)
	}
}
