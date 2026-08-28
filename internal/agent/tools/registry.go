package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// toolErrorHint is appended to tool error messages to guide the LLM to retry with a different approach.
const toolErrorHint = "\n\n[Analyze the error above and try a different approach.]"

// ToolRegistry manages the registration and retrieval of tools
type ToolRegistry struct {
	tools             map[string]types.Tool
	maxToolOutputSize int // maximum chars for tool output (0 = use DefaultMaxToolOutput)
}

// outputLimitProvider is implemented by tools that expose a caller-configurable
// output budget with their own hard safety cap. It prevents the registry's
// generic limit from undoing that explicit bounded choice.
type outputLimitProvider interface {
	OutputLimitChars(args json.RawMessage) int
}

// NewToolRegistry creates a new tool registry
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]types.Tool),
	}
}

// SetMaxToolOutputSize sets the maximum character length for tool output.
// Values <= 0 will use DefaultMaxToolOutput.
func (r *ToolRegistry) SetMaxToolOutputSize(maxChars int) {
	r.maxToolOutputSize = maxChars
}

// getMaxToolOutput returns the effective max tool output size.
func (r *ToolRegistry) getMaxToolOutput() int {
	if r.maxToolOutputSize > 0 {
		return r.maxToolOutputSize
	}
	return DefaultMaxToolOutput
}

// RegisterTool adds a tool to the registry.
// If a tool with the same name is already registered, the existing one is kept
// (first-wins) to prevent tool execution hijacking via name collision (GHSA-67q9-58vj-32qx).
func (r *ToolRegistry) RegisterTool(tool types.Tool) {
	name := tool.Name()
	if _, exists := r.tools[name]; exists {
		logger.Warnf(context.Background(),
			"[ToolRegistry] Duplicate tool registration rejected: %s (first-wins policy)", name)
		return
	}
	r.tools[name] = tool
}

// GetTool retrieves a tool by name
func (r *ToolRegistry) GetTool(name string) (types.Tool, error) {
	tool, exists := r.tools[name]
	if !exists {
		return nil, fmt.Errorf("tool not found: %s", name)
	}
	return tool, nil
}

// ListTools returns all registered tool names sorted alphabetically.
// Sorting keeps the order stable across calls — Go map iteration is
// intentionally randomized.
func (r *ToolRegistry) ListTools() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// GetFunctionDefinitions returns function definitions for all registered tools.
// The slice is sorted by tool name so the serialized payload sent to the LLM
// is byte-identical across requests. Providers that key prompt caching on a
// byte-level prefix match (e.g. Qwen explicit caching) require this — map
// iteration order would otherwise reshuffle the tools block and break cache
// hits.
func (r *ToolRegistry) GetFunctionDefinitions() []types.FunctionDefinition {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	definitions := make([]types.FunctionDefinition, 0, len(names))
	for _, name := range names {
		tool := r.tools[name]
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
			Error:   err.Error() + toolErrorHint,
		}, err
	}

	// Cast parameters to match expected schema types before execution.
	// This handles common LLM quirks like returning "true" instead of true.
	args = CastParams(args, tool.Parameters())

	// Clamp numeric parameters to the schema-declared [minimum, maximum] range
	// before validation. LLMs occasionally request very large `limit` / `offset`
	// values that exceed the tool's hard ceiling (e.g. list_knowledge_chunks
	// limit <= 100), which would otherwise fail the whole agent chat call and
	// abort downstream skills. We clamp silently on the tool boundary so the
	// call can make forward progress; tools are expected to surface remaining
	// rows via pagination / "remaining" hints in their output.
	if clamped, changed := ClampParams(args, tool.Parameters()); changed {
		logger.Infof(ctx, "[ToolRegistry] clamped params for tool=%s before=%s after=%s", name, string(args), string(clamped))
		args = clamped
	}

	// Validate parameters against the tool's JSON Schema before execution.
	// This catches invalid arguments early, avoiding a wasted tool execution + LLM round.
	if validationErrs := ValidateParams(args, tool.Parameters()); len(validationErrs) > 0 {
		errMsg := FormatValidationErrors(validationErrs) + toolErrorHint
		common.PipelineWarn(ctx, "AgentTool", "validation_failed", map[string]interface{}{
			"tool":   name,
			"errors": errMsg,
		})
		return &types.ToolResult{
			Success: false,
			Error:   errMsg,
		}, nil
	}

	// Publish the ceiling so budget-aware tools can shape a batched result
	// themselves; the truncation below stays as the fallback for the rest.
	maxOutput := r.getMaxToolOutput()
	if provider, ok := tool.(outputLimitProvider); ok {
		if toolLimit := provider.OutputLimitChars(args); toolLimit > maxOutput {
			maxOutput = toolLimit
		}
	}
	result, execErr := tool.Execute(WithOutputBudget(ctx, maxOutput), args)

	// Truncate large tool outputs to prevent context window poisoning. The
	// limit is counted in runes to match TruncateToolOutput; comparing bytes
	// here would leave CJK output effectively uncapped.
	if result != nil && utf8.RuneCountInString(result.Output) > maxOutput {
		result.Output = TruncateToolOutput(result.Output, maxOutput)
	}

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
		// Append error hint to guide LLM to retry with a different approach
		if result.Error != "" {
			result.Error = result.Error + toolErrorHint
		}
		common.PipelineWarn(ctx, "AgentTool", "execute_done", fields)
	} else {
		common.PipelineInfo(ctx, "AgentTool", "execute_done", fields)
	}

	return result, execErr
}

// Cleanup cleans up all registered tools that implement the types.Cleanable interface.
// This is called at the end of agent sessions to release tool-specific resources.
func (r *ToolRegistry) Cleanup(ctx context.Context) {
	for name, tool := range r.tools {
		if cleanable, ok := tool.(types.Cleanable); ok {
			logger.Infof(ctx, "[ToolRegistry] Cleaning up tool: %s", name)
			cleanable.Cleanup(ctx)
		}
	}
}
