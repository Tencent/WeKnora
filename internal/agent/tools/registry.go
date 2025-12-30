package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/types"
)

// ToolRegistry manages the registration and retrieval of tools
type ToolRegistry struct {
	tools map[string]types.Tool
}

// NewToolRegistry creates a new tool registry
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]types.Tool),
	}
}

// RegisterTool adds a tool to the registry
func (r *ToolRegistry) RegisterTool(tool types.Tool) {
	r.tools[tool.Name()] = tool
}

// GetTool retrieves a tool by name
func (r *ToolRegistry) GetTool(name string) (types.Tool, error) {
	tool, exists := r.tools[name]
	if !exists {
		return nil, fmt.Errorf("tool not found: %s", name)
	}
	return tool, nil
}

// ListTools returns all registered tool names
func (r *ToolRegistry) ListTools() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// GetFunctionDefinitions returns function definitions for all registered tools
func (r *ToolRegistry) GetFunctionDefinitions() []types.FunctionDefinition {
	definitions := make([]types.FunctionDefinition, 0)
	for _, tool := range r.tools {
		definitions = append(definitions, types.FunctionDefinition{
			Name:        tool.Name(),
			Description: tool.Description(),
			Parameters:  tool.Parameters(),
		})
	}
	return definitions
}

// ExecuteTool executes a tool by name with the given arguments
func (r *ToolRegistry) ExecuteTool(
	ctx context.Context,
	name string,
	args json.RawMessage,
) (*types.ToolResult, error) {
	common.PipelineInfo(ctx, "AgentTool", "execute_start", map[string]interface{}{
		"tool": name,
		"args": args,
	})
	tool, err := r.GetTool(name)
	if err != nil {
		common.PipelineError(ctx, "AgentTool", "execute_failed", map[string]interface{}{
			"tool":  name,
			"error": err.Error(),
		})
		return &types.ToolResult{
			Success: false,
			Error:   err.Error(),
		}, err
	}

	result, execErr := tool.Execute(ctx, args)
	fields := map[string]interface{}{
		"tool": name,
		"args": args,
	}
	if result != nil {
		fields["success"] = result.Success
		if result.Error != "" {
			fields["error"] = result.Error
		}
	}
	if execErr != nil {
		fields["error"] = execErr.Error()
		common.PipelineError(ctx, "AgentTool", "execute_done", fields)
	} else if result != nil && !result.Success {
		common.PipelineWarn(ctx, "AgentTool", "execute_done", fields)
	} else {
		common.PipelineInfo(ctx, "AgentTool", "execute_done", fields)
	}

	return result, execErr
}

// Cleanup cleans up all registered tools that implement the Cleanup method
func (r *ToolRegistry) Cleanup(ctx context.Context) {
	// Check specifically for DataAnalysisTool
	if tool, exists := r.tools[ToolDataAnalysis]; exists {
		if dataAnalysisTool, ok := tool.(*DataAnalysisTool); ok {
			dataAnalysisTool.Cleanup(ctx)
		}
	}
}
