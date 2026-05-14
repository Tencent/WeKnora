package tools

import (
	"context"
	"encoding/json"
	"fmt"
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
	description: `Execute a command or script from a skill in an isolated workspace environment.

## Architecture (Workspace Mode)
- Creates an isolated workspace with standard layout: skills/, work/, runs/, out/
- Stages skill files into the workspace (read-only)
- Injects environment variables: $WORKSPACE_DIR, $SKILLS_DIR, $WORK_DIR, $OUTPUT_DIR, $RUN_DIR, $SKILL_NAME
- Supports both command mode (bash -c) and script mode
- Automatically collects output artifacts from the out/ directory

## Usage
- **Command mode** (preferred): Use the 'command' parameter to run any shell command
  - Example: "python3 scripts/generate_report.py --format pdf"
  - Example: "pip install -r requirements.txt && python3 main.py"
  - Example: "bash scripts/setup.sh && python3 scripts/run.py"
- **Script mode** (legacy): Use 'script_path' to run a specific script file
  - Example: script_path="scripts/analyze.py"

## Output Files (Artifacts)
- Scripts should write output files to the $OUTPUT_DIR directory
- All files in $OUTPUT_DIR are automatically collected after execution
- Text files are returned inline; binary files include metadata only
- Use output_files parameter to specify glob patterns for selective collection

## Environment Variables Available to Scripts
- $WORKSPACE_DIR: Root of the isolated workspace
- $SKILLS_DIR: Directory containing staged skill files
- $WORK_DIR: Writable directory for intermediate files
- $OUTPUT_DIR: Directory for output artifacts (write here!)
- $RUN_DIR: Per-run working directory
- $SKILL_NAME: Name of the current skill

## Security
- Scripts run in a sandboxed environment (local process or Docker container)
- Skill files are staged read-only in the workspace
- Output directory is writable for artifact collection

## Returns
- Script stdout and stderr output
- Exit code indicating success (0) or failure (non-zero)
- Collected output files from $OUTPUT_DIR as artifacts`,
	schema: utils.GenerateSchema[ExecuteSkillScriptInput](),
}

// ExecuteSkillScriptInput defines the input parameters for the execute_skill_script tool
type ExecuteSkillScriptInput struct {
	SkillName   string            `json:"skill_name" jsonschema:"Name of the skill containing the script"`
	Command     string            `json:"command,omitempty" jsonschema:"Shell command to execute via bash -c (preferred over script_path). Example: 'python3 scripts/generate.py --output $OUTPUT_DIR/report.pdf'. The command runs in the skill directory within the workspace."`
	ScriptPath  string            `json:"script_path,omitempty" jsonschema:"Relative path to the script within the skill directory (e.g. scripts/analyze.py). Used when command is empty."`
	Args        []string          `json:"args,omitempty" jsonschema:"Optional command-line arguments to pass to the script (only used with script_path). Note: if using --file flag, you must provide an actual file path that exists in the skill directory. If you have data in memory (not a file), use the 'input' parameter instead."`
	Input       string            `json:"input,omitempty" jsonschema:"Optional input data to pass to the script via stdin. Use this when you have data in memory (e.g. JSON string) that the script should process. This is equivalent to piping data: echo 'data' | python script.py"`
	Env         map[string]string `json:"env,omitempty" jsonschema:"Optional environment variables to inject into the execution environment"`
	OutputFiles []string          `json:"output_files,omitempty" jsonschema:"Optional glob patterns for output files to collect from the out/ directory (e.g. ['*.csv', 'report.*']). If empty, all files in out/ are collected."`
}

// ExecuteSkillScriptTool allows the agent to execute skill scripts in a sandbox
type ExecuteSkillScriptTool struct {
	BaseTool
	skillManager    *skills.Manager
	artifactService skills.ArtifactService
}

// NewExecuteSkillScriptTool creates a new execute_skill_script tool instance
func NewExecuteSkillScriptTool(skillManager *skills.Manager, artifactService skills.ArtifactService) *ExecuteSkillScriptTool {
	return &ExecuteSkillScriptTool{
		BaseTool:        executeSkillScriptTool,
		skillManager:    skillManager,
		artifactService: artifactService,
	}
}

// Execute executes the execute_skill_script tool
func (t *ExecuteSkillScriptTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][ExecuteSkillScript] Execute started")

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

	if input.Command == "" && input.ScriptPath == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "either command or script_path is required",
		}, nil
	}

	// Check if skill manager is available
	if t.skillManager == nil || !t.skillManager.IsEnabled() {
		return &types.ToolResult{
			Success: false,
			Error:   "Skills are not enabled",
		}, nil
	}

	logger.Infof(ctx, "[Tool][ExecuteSkillScript] Executing in workspace mode: skill=%s, command=%q, script=%s, args=%v, input_len=%d, output_files=%v, env=%v",
		input.SkillName, input.Command, input.ScriptPath, input.Args, len(input.Input), input.OutputFiles, input.Env)

	result, err := t.skillManager.ExecuteCommand(
		ctx,
		input.SkillName,
		input.Command,
		input.ScriptPath,
		input.Args,
		input.Input,
		input.Env,
		input.OutputFiles,
	)
	if err != nil {
		logger.Errorf(ctx, "[Tool][ExecuteSkillScript] Execution failed: %v", err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Execution failed: %v", err),
		}, nil
	}

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("=== Skill Execution: %s ===\n\n", input.SkillName))

	if input.Command != "" {
		builder.WriteString(fmt.Sprintf("**Command**: %s\n", input.Command))
	} else {
		builder.WriteString(fmt.Sprintf("**Script**: %s\n", input.ScriptPath))
		if len(input.Args) > 0 {
			builder.WriteString(fmt.Sprintf("**Arguments**: %v\n", input.Args))
		}
	}

	builder.WriteString(fmt.Sprintf("**Exit Code**: %d\n", result.ExitCode))
	builder.WriteString(fmt.Sprintf("**Duration**: %v\n\n", result.Duration))

	if result.Killed {
		builder.WriteString("**Warning**: Process was terminated (timeout or killed)\n\n")
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

	if len(result.OutputFiles) > 0 {
		builder.WriteString("## Output Files (Artifacts)\n\n")
		builder.WriteString(fmt.Sprintf("Collected **%d** output file(s) from $OUTPUT_DIR:\n\n", len(result.OutputFiles)))
		for _, f := range result.OutputFiles {
			sizeStr := formatOutputFileSize(f.SizeBytes)
			if f.IsText {
				builder.WriteString(fmt.Sprintf("### 📄 %s (%s, %s)\n\n", f.Name, f.MIMEType, sizeStr))
				if f.Content != "" {
					if len(f.Content) <= 4096 {
						builder.WriteString("```\n")
						builder.WriteString(f.Content)
						if !strings.HasSuffix(f.Content, "\n") {
							builder.WriteString("\n")
						}
						builder.WriteString("```\n\n")
					} else {
						builder.WriteString("```\n")
						builder.WriteString(f.Content[:2048])
						builder.WriteString("\n... (truncated) ...\n")
						builder.WriteString(f.Content[len(f.Content)-1024:])
						builder.WriteString("\n```\n\n")
					}
				}
			} else {
				builder.WriteString(fmt.Sprintf("### 📦 %s (%s, %s) [binary]\n\n", f.Name, f.MIMEType, sizeStr))
			}
		}

		// save output artifacts
		t.saveOutputArtifacts(ctx, input.SkillName, result.OutputFiles)
	}

	// Determine success based on exit code
	success := result.IsSuccess()

	resultData := map[string]interface{}{
		"display_type": "skill_output",
		"skill_name":   input.SkillName,
		"command":      input.Command,
		"script_path":  input.ScriptPath,
		"args":         input.Args,
		"exit_code":    result.ExitCode,
		"stdout":       result.Stdout,
		"stderr":       result.Stderr,
		"duration_ms":  result.Duration.Milliseconds(),
		"killed":       result.Killed,
	}

	// add output files
	if len(result.OutputFiles) > 0 {
		artifactSessionID := fmt.Sprintf("skill-%s", input.SkillName)
		resultData["artifact_session_id"] = artifactSessionID

		artifactFiles := make([]map[string]interface{}, 0, len(result.OutputFiles))
		for _, f := range result.OutputFiles {
			af := map[string]interface{}{
				"name":       f.Name,
				"mime_type":  f.MIMEType,
				"size_bytes": f.SizeBytes,
				"is_text":    f.IsText,
			}
			if f.IsText && f.Content != "" {
				af["content"] = f.Content
			}
			artifactFiles = append(artifactFiles, af)
		}
		resultData["output_files"] = artifactFiles
	}

	logger.Infof(ctx, "[Tool][ExecuteSkillScript] Execution completed with exit code: %d, output files: %d",
		result.ExitCode, len(result.OutputFiles))

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

// saveOutputArtifacts saves output artifacts to artifact service
func (t *ExecuteSkillScriptTool) saveOutputArtifacts(ctx context.Context, skillName string, outputFiles []sandbox.OutputFile) {
	if t.artifactService == nil || len(outputFiles) == 0 {
		return
	}

	sessionInfo := skills.ArtifactSessionInfo{
		AppName:   "weknora",
		UserID:    "default",
		SessionID: fmt.Sprintf("skill-%s", skillName),
	}

	for _, f := range outputFiles {
		data := f.Data
		if len(data) == 0 && f.Content != "" {
			data = []byte(f.Content)
		}
		if len(data) == 0 {
			continue
		}

		artifact := &skills.Artifact{
			Data:     data,
			MimeType: f.MIMEType,
			Name:     f.Name,
		}

		version, err := t.artifactService.SaveArtifact(ctx, sessionInfo, f.Name, artifact)
		if err != nil {
			logger.Warnf(ctx, "[Tool][ExecuteSkillScript] Failed to save artifact %s: %v", f.Name, err)
		} else {
			logger.Infof(ctx, "[Tool][ExecuteSkillScript] Saved artifact %s (version %d)", f.Name, version)
		}
	}
}

// formatOutputFileSize ...
func formatOutputFileSize(bytes int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
	)
	switch {
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// Cleanup releases any resources
func (t *ExecuteSkillScriptTool) Cleanup(ctx context.Context) error {
	return nil
}
