package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// Tool name constant for execute_skill_script

var executeSkillScriptTool = BaseTool{
	name: ToolExecuteSkillScript,
	description: `Execute a script with a skill's own interpreter and dependencies.

## Usage
- ` + "`script_path`" + ` is either:
  - a path **inside the skill** (` + "`scripts/analyze.py`" + `), or
  - an absolute session file you just wrote with ` + "`write_sandbox_file`" + `
    (` + "`/workspace/output/generate_ppt.py`" + `). That file still runs with
    this skill's virtualenv / node_modules.
- Do NOT pass ` + "`/workspace/input`" + ` paths as ` + "`script_path`" + `
  (attachments are inputs via ` + "`args`" + `).
- User-uploaded files are listed in the current ` + "`<sandbox_attachments>`" + `
  block. Pass their absolute ` + "`/workspace/input/...`" + ` paths through
  ` + "`args`" + ` when a script accepts an input file.
- Treat ` + "`/workspace/input`" + ` as read-only. Write generated files only to
  ` + "`$WEKNORA_SKILL_OUTPUT_DIR`" + ` so they can be collected for download.
- Scripts reach the dependencies their install put beside them: Python runs
  with the skill's own virtualenv interpreter, Node resolves the skill's
  node_modules from the script's location. A failed import under a bare
  ` + "`python3 -c`" + ` or ` + "`node -e`" + ` says nothing about whether this
  tool can run the script.
- The skill tree is frozen after install. Do not run ` + "`install_deps.py`" + `
  (or ` + "`python -m pip`" + ` / ` + "`ensurepip`" + ` inside the skill ` + "`.venv`" + `) to
  fetch extras at chat time. If a package is missing, install it into
  ` + "`/workspace/.skill-packages/<skill_name>`" + ` with system ` + "`python3 -m pip install --target`" + `
  and call this tool again — PYTHONPATH already includes that directory.

## When to Use
- When a skill's instructions reference a utility script (e.g., "Run scripts/analyze_form.py")
- After ` + "`write_sandbox_file`" + ` / ` + "`edit_sandbox_file`" + ` customizes a skill
  script and you still need that skill's packages (python-pptx, pandas, …)
- When automation or data processing is needed as part of skill workflow
- For deterministic operations where script execution is more reliable than generating code

## When NOT to Use
- Do not use this for a standalone ` + "`/workspace`" + ` script that does not need a
  skill's packages — call ` + "`shell_exec`" + ` instead.

## Security
- Scripts run in a sandboxed environment with limited permissions
- Network access is disabled by default
- Bundled scripts stay inside the skill directory; session files must sit
  under ` + "`/workspace`" + ` and not under ` + "`/workspace/input`" + `

## Returns
- Script stdout and stderr output
- Exit code indicating success (0) or failure (non-zero)`,
	schema: utils.GenerateSchema[ExecuteSkillScriptInput](),
}

// ExecuteSkillScriptInput defines the input parameters for the execute_skill_script tool
type ExecuteSkillScriptInput struct {
	SkillName  string   `json:"skill_name" jsonschema:"Name of the skill containing the script"`
	ScriptPath string   `json:"script_path" jsonschema:"Relative path inside the skill (e.g. scripts/analyze.py), or an absolute /workspace/... file written with write_sandbox_file. Do not pass /workspace/input paths."`
	Args       []string `json:"args,omitempty" jsonschema:"Optional command-line arguments. For file flags, pass an absolute path from the current <sandbox_attachments> block (/workspace/input/...). For in-memory data, use input instead."`
	Input      string   `json:"input,omitempty" jsonschema:"Optional input data to pass to the script via stdin. Use this when you have data in memory (e.g. JSON string) that the script should process. This is equivalent to piping data: echo 'data' | python script.py"`
}

// UnmarshalJSON accepts args as either the documented string array or a single
// command-line string. Some model providers emit a string for a single tool
// argument; accepting it here keeps that malformed-but-unambiguous call from
// failing before the script can run.
func (i *ExecuteSkillScriptInput) UnmarshalJSON(data []byte) error {
	var raw struct {
		SkillName  string          `json:"skill_name"`
		ScriptPath string          `json:"script_path"`
		Args       json.RawMessage `json:"args"`
		Input      string          `json:"input"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	i.SkillName = raw.SkillName
	i.ScriptPath = raw.ScriptPath
	i.Input = raw.Input
	i.Args = nil

	if len(raw.Args) == 0 || string(raw.Args) == "null" {
		return nil
	}

	if err := json.Unmarshal(raw.Args, &i.Args); err == nil {
		return nil
	}

	var argsString string
	if err := json.Unmarshal(raw.Args, &argsString); err != nil {
		return fmt.Errorf("args must be a string or an array of strings: %w", err)
	}

	// Some providers emit the array as a stringified JSON payload
	// (e.g. "[\"--project-name\",\"X\"]"). Treat that as an array first so the
	// model's intent is preserved; strings.Fields would otherwise split the
	// brackets/quotes into garbage tokens and the script would see nonsense argv.
	if err := json.Unmarshal([]byte(argsString), &i.Args); err == nil {
		return nil
	}

	// A plain string is interpreted as a conventional space-separated command
	// line. The tool schema continues to advertise []string, so well-formed
	// calls are unaffected; this is only a compatibility fallback for model
	// output.
	i.Args = strings.Fields(argsString)
	return nil
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

	// Validate required fields
	if input.SkillName == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "skill_name is required",
		}, nil
	}

	if input.ScriptPath == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "script_path is required",
		}, nil
	}

	if _, ok := sandbox.RunnableWorkspaceScript(input.ScriptPath); !ok {
		rel, err := skillRelativeFilePath(input.SkillName, input.ScriptPath)
		if err != nil {
			return &types.ToolResult{
				Success: false,
				Error:   err.Error(),
			}, nil
		}
		if rel == "" {
			return &types.ToolResult{
				Success: false,
				Error:   "script_path is the skill directory; pass a relative script such as scripts/generate_ppt.py",
			}, nil
		}
		input.ScriptPath = rel
	}

	// Check if skill manager is available
	if t.skillManager == nil || !t.skillManager.IsEnabled() {
		return &types.ToolResult{
			Success: false,
			Error:   "Skills are not enabled",
		}, nil
	}

	// Execute the script in sandbox
	logger.Infof(ctx, "[Tool][ExecuteSkillScript] Executing script: %s/%s with args: %v, input length: %d",
		input.SkillName, input.ScriptPath, input.Args, len(input.Input))

	result, err := t.skillManager.ExecuteScript(ctx, input.SkillName, input.ScriptPath, input.Args, input.Input)
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

	if hint := skillOnDemandInstallHint(input.SkillName, input.ScriptPath, result.Stdout, result.Stderr); hint != "" {
		builder.WriteString(hint)
		builder.WriteString("\n")
	} else if !result.IsSuccess() {
		if hint := pythonSyntaxErrorHint(result.Stderr); hint != "" {
			builder.WriteString(hint)
			builder.WriteString("\n")
		} else if hint := skillMissingPackageHint(input.SkillName, result.Stderr); hint != "" {
			builder.WriteString(hint)
			builder.WriteString("\n")
		}
	}

	// Determine success based on exit code
	success := result.IsSuccess()

	resultData := map[string]interface{}{
		"display_type": "shell_exec",
		"command":      skillScriptCommand(input),
		"skill_name":   input.SkillName,
		"script_path":  input.ScriptPath,
		"args":         input.Args,
		"exit_code":    result.ExitCode,
		"stdout":       result.Stdout,
		"stderr":       result.Stderr,
		"duration_ms":  result.Duration.Milliseconds(),
		"killed":       result.Killed,
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

func skillScriptCommand(input ExecuteSkillScriptInput) string {
	parts := make([]string, 0, 1+len(input.Args))
	script := strings.TrimSpace(input.ScriptPath)
	switch {
	case strings.HasPrefix(path.Clean(script), "/workspace/"):
		parts = append(parts, script)
	case input.SkillName != "" && script != "":
		parts = append(parts, input.SkillName+"/"+script)
	case script != "":
		parts = append(parts, script)
	}
	parts = append(parts, input.Args...)
	return strings.Join(parts, " ")
}

// Cleanup releases any resources
func (t *ExecuteSkillScriptTool) Cleanup(ctx context.Context) error {
	return nil
}
