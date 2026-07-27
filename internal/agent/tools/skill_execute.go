package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// Tool name constant for execute_skill_script

var executeSkillScriptTool = BaseTool{
	name: ToolExecuteSkillScript,
	description: `Execute a script from a skill in a sandboxed environment.

## Usage
- Use this tool to run utility scripts bundled with a skill
- Scripts are executed in an isolated sandbox for security
- Only scripts from loaded skills can be executed

## When to Use
- When a skill's instructions reference a utility script (e.g., "Run scripts/analyze_form.py")
- When automation or data processing is needed as part of skill workflow
- For deterministic operations where script execution is more reliable than generating code

## Security
- Scripts run in a sandboxed environment with limited permissions
- Network access is disabled by default
- File access is restricted to the skill directory

## Returns
- Script stdout and stderr output
- Exit code indicating success (0) or failure (non-zero)`,
	schema: utils.GenerateSchema[ExecuteSkillScriptInput](),
}

// ExecuteSkillScriptInput defines the input parameters for the execute_skill_script tool
type ExecuteSkillScriptInput struct {
	SkillRef   *SkillRefInput `json:"skill_ref,omitempty" jsonschema:"Canonical skill reference"`
	SkillName  string         `json:"skill_name,omitempty" jsonschema:"Legacy preloaded skill name"`
	ScriptPath string         `json:"script_path" jsonschema:"Relative path to the script within the skill directory (e.g. scripts/analyze.py)"`
	Args       []string       `json:"args,omitempty" jsonschema:"Optional command-line arguments to pass to the script. Note: if using --file flag, you must provide an actual file path that exists in the skill directory. If you have data in memory (not a file), use the 'input' parameter instead."`
	Input      string         `json:"input,omitempty" jsonschema:"Optional input data to pass to the script via stdin. Use this when you have data in memory (e.g. JSON string) that the script should process. This is equivalent to piping data: echo 'data' | python script.py"`
}

// ExecuteSkillScriptTool allows the agent to execute skill scripts in a sandbox
type ExecuteSkillScriptTool struct {
	BaseTool
	skillManager *skills.Manager
}

// NewExecuteSkillScriptTool creates a new execute_skill_script tool instance
func NewExecuteSkillScriptTool(skillManager *skills.Manager) *ExecuteSkillScriptTool {
	return &ExecuteSkillScriptTool{
		BaseTool:     executeSkillScriptTool,
		skillManager: skillManager,
	}
}

// Execute executes the execute_skill_script tool
func (t *ExecuteSkillScriptTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][ExecuteSkillScript] Execute started")

	// Parse input
	var input ExecuteSkillScriptInput
	if err := json.Unmarshal(args, &input); err != nil {
		logger.Errorf(ctx, "[Tool][ExecuteSkillScript] Failed to parse args: %v", err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", err),
		}, nil
	}

	ref, refErr := canonicalSkillRef(input.SkillRef, input.SkillName)
	if refErr != nil {
		return &types.ToolResult{
			Success: false,
			Error:   refErr.Error(),
		}, nil
	}

	if input.ScriptPath == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "script_path is required",
		}, nil
	}

	// Check if skill manager is available
	if t.skillManager == nil || !t.skillManager.IsEnabled() {
		return &types.ToolResult{
			Success: false,
			Error:   "Skills are not enabled",
		}, nil
	}
	if recovery, ok := t.missingPreloadedScriptRecovery(ctx, ref, input.ScriptPath); ok {
		logger.Warnf(ctx, "[Tool][ExecuteSkillScript] Recovered missing script: %s/%s", ref.SkillID, input.ScriptPath)
		return recovery, nil
	}

	// Execute the script in sandbox
	logger.Infof(ctx, "[Tool][ExecuteSkillScript] Executing script: %s/%s with args: %v, input length: %d",
		input.SkillName, input.ScriptPath, input.Args, len(input.Input))

	result, err := t.skillManager.ExecuteScriptRef(ctx, ref, input.ScriptPath, input.Args, input.Input)
	if err != nil {
		logger.Errorf(ctx, "[Tool][ExecuteSkillScript] Script execution failed: %v", err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Script execution failed: %v", err),
		}, nil
	}

	// Build output
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("=== Script Execution: %s/%s ===\n\n", input.SkillName, input.ScriptPath))

	if len(input.Args) > 0 {
		builder.WriteString(fmt.Sprintf("**Arguments**: %v\n", input.Args))
	}

	builder.WriteString(fmt.Sprintf("**Exit Code**: %d\n", result.ExitCode))
	builder.WriteString(fmt.Sprintf("**Duration**: %v\n\n", result.Duration))

	if result.Killed {
		builder.WriteString("**Warning**: Script was terminated (timeout or killed)\n\n")
	}

	if result.Stdout != "" {
		builder.WriteString("## Standard Output\n\n")
		builder.WriteString("```\n")
		builder.WriteString(result.Stdout)
		if !strings.HasSuffix(result.Stdout, "\n") {
			builder.WriteString("\n")
		}
		builder.WriteString("```\n\n")
	}

	if result.Stderr != "" {
		builder.WriteString("## Standard Error\n\n")
		builder.WriteString("```\n")
		builder.WriteString(result.Stderr)
		if !strings.HasSuffix(result.Stderr, "\n") {
			builder.WriteString("\n")
		}
		builder.WriteString("```\n\n")
	}

	if result.Error != "" {
		builder.WriteString("## Error\n\n")
		builder.WriteString(result.Error)
		builder.WriteString("\n")
	}

	// Determine success based on exit code
	success := result.IsSuccess()

	resultData := map[string]interface{}{
		"skill_ref":   ref,
		"script_path": input.ScriptPath,
		"args":        input.Args,
		"exit_code":   result.ExitCode,
		"stdout":      result.Stdout,
		"stderr":      result.Stderr,
		"duration_ms": result.Duration.Milliseconds(),
		"killed":      result.Killed,
	}

	logger.Infof(ctx, "[Tool][ExecuteSkillScript] Script completed with exit code: %d", result.ExitCode)

	return &types.ToolResult{
		Success: success,
		Output:  builder.String(),
		Data:    resultData,
		Error: func() string {
			if !success {
				if result.Error != "" {
					return result.Error
				}
				return fmt.Sprintf("Script exited with code %d", result.ExitCode)
			}
			return ""
		}(),
	}, nil
}

func (t *ExecuteSkillScriptTool) missingPreloadedScriptRecovery(
	ctx context.Context,
	ref types.SkillReference,
	requestedPath string,
) (*types.ToolResult, bool) {
	if ref.Source != types.SkillSourcePreloaded {
		return nil, false
	}
	cleanPath := filepath.Clean(requestedPath)
	if filepath.IsAbs(cleanPath) || strings.HasPrefix(cleanPath, "..") {
		return nil, false
	}
	files, err := t.skillManager.ListSkillFilesRef(ctx, ref)
	if err != nil {
		return nil, false
	}
	availableScripts := make([]string, 0)
	for _, file := range files {
		if filepath.Clean(file) == cleanPath {
			return nil, false
		}
		if skills.IsScript(file) {
			availableScripts = append(availableScripts, file)
		}
	}
	return missingScriptRecoveryResult(ref.SkillID, requestedPath, availableScripts), true
}

func missingScriptRecoveryResult(
	skillName string,
	requestedPath string,
	availableScripts []string,
) *types.ToolResult {
	message := fmt.Sprintf(
		"The loaded skill %q is instruction-only and does not contain %q. "+
			"Do not retry execute_skill_script or invent script paths. Continue by following SKILL.md "+
			"with available knowledge-base or web-search tools; otherwise provide a non-real-time analysis and state the limitation.",
		skillName, requestedPath,
	)
	if len(availableScripts) > 0 {
		message = fmt.Sprintf(
			"Script %q does not exist in skill %q. Do not retry that path. Available scripts: %s.",
			requestedPath, skillName, strings.Join(availableScripts, ", "),
		)
	}
	return &types.ToolResult{
		Success: true,
		Output:  message,
		Data: map[string]interface{}{
			"executed": false, "recovery": "missing_script", "available_scripts": availableScripts,
		},
	}
}

// Cleanup releases any resources
func (t *ExecuteSkillScriptTool) Cleanup(ctx context.Context) error {
	return nil
}
