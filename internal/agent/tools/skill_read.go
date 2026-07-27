package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// Tool name constant for read_skill

var readSkillTool = BaseTool{
	name: ToolReadSkill,
	description: `Read skill content on demand to learn specialized capabilities.

## Usage
- Use this tool when a user request matches an available skill's description
- Provide the skill_name to load the skill's full instructions (SKILL.md content)
- Optionally provide file_path to read additional files within the skill directory

## When to Use
- When the system prompt shows an available skill that matches the user's request
- Before performing tasks that match a skill's description
- To read additional documentation or reference files within a skill

## Returns
- Skill instructions and guidance for completing the task
- File content if file_path is specified`,
	schema: utils.GenerateSchema[ReadSkillInput](),
}

// ReadSkillInput defines the input parameters for the read_skill tool
type ReadSkillInput struct {
	SkillRef  *SkillRefInput `json:"skill_ref,omitempty" jsonschema:"Canonical skill reference"`
	SkillName string         `json:"skill_name,omitempty" jsonschema:"Legacy preloaded skill name"`
	FilePath  string         `json:"file_path,omitempty" jsonschema:"Optional relative path to a specific file within the skill directory"`
}

type SkillRefInput struct {
	Source  types.SkillSource `json:"source"`
	SkillID string            `json:"skill_id"`
}

func canonicalSkillRef(ref *SkillRefInput, legacyName string) (types.SkillReference, error) {
	if ref != nil {
		if ref.Source != types.SkillSourcePreloaded && ref.Source != types.SkillSourceTenant {
			return types.SkillReference{}, fmt.Errorf("invalid skill source")
		}
		if ref.SkillID == "" {
			return types.SkillReference{}, fmt.Errorf("skill_id is required")
		}
		return types.SkillReference{Source: ref.Source, SkillID: ref.SkillID}, nil
	}
	if legacyName == "" {
		return types.SkillReference{}, fmt.Errorf("skill_ref or skill_name is required")
	}
	return types.SkillReference{Source: types.SkillSourcePreloaded, SkillID: legacyName}, nil
}

// ReadSkillTool allows the agent to read skill content on demand
type ReadSkillTool struct {
	BaseTool
	skillManager *skills.Manager
}

// NewReadSkillTool creates a new read_skill tool instance
func NewReadSkillTool(skillManager *skills.Manager) *ReadSkillTool {
	return &ReadSkillTool{
		BaseTool:     readSkillTool,
		skillManager: skillManager,
	}
}

// Execute executes the read_skill tool
func (t *ReadSkillTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][ReadSkill] Execute started")

	// Parse input
	var input ReadSkillInput
	if err := json.Unmarshal(args, &input); err != nil {
		logger.Errorf(ctx, "[Tool][ReadSkill] Failed to parse args: %v", err)
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

	// Check if skill manager is available
	if t.skillManager == nil || !t.skillManager.IsEnabled() {
		return &types.ToolResult{
			Success: false,
			Error:   "Skills are not enabled",
		}, nil
	}

	var builder strings.Builder
	var resultData = make(map[string]interface{})

	if input.FilePath != "" {
		// Read a specific file from the skill directory
		content, err := t.skillManager.ReadSkillFileRef(ctx, ref, input.FilePath)
		if err != nil {
			logger.Errorf(ctx, "[Tool][ReadSkill] Failed to read skill file: %v", err)
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("Failed to read skill file: %v", err),
			}, nil
		}

		builder.WriteString(fmt.Sprintf("=== Skill File: %s/%s ===\n\n", ref.SkillID, input.FilePath))
		builder.WriteString(content)

		resultData["skill_ref"] = ref
		resultData["file_path"] = input.FilePath
		resultData["content"] = content
		resultData["content_length"] = len(content)

	} else {
		// Read the main skill instructions (SKILL.md)
		skill, err := t.skillManager.LoadSkillRef(ctx, ref)
		if err != nil {
			logger.Errorf(ctx, "[Tool][ReadSkill] Failed to load skill: %v", err)
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("Failed to load skill: %v", err),
			}, nil
		}

		// List available files in the skill directory
		files, err := t.skillManager.ListSkillFilesRef(ctx, ref)
		if err != nil {
			files = []string{} // Non-fatal error
		}

		builder.WriteString(fmt.Sprintf("=== Skill: %s ===\n\n", skill.Name))
		builder.WriteString(fmt.Sprintf("**Description**: %s\n\n", skill.Description))
		builder.WriteString("## Instructions\n\n")
		builder.WriteString(skill.Instructions)

		// Add available files section
		if len(files) > 1 { // More than just SKILL.md
			builder.WriteString("\n\n## Available Files\n\n")
			builder.WriteString("The following files are available in this skill directory. Use `read_skill` with `file_path` to read them:\n\n")
			for _, file := range files {
				if file != skills.SkillFileName { // Don't list SKILL.md again
					if skills.IsScript(file) {
						builder.WriteString(fmt.Sprintf("- `%s` (script - can be executed)\n", file))
					} else {
						builder.WriteString(fmt.Sprintf("- `%s`\n", file))
					}
				}
			}
		}

		resultData["skill_name"] = skill.Name
		resultData["description"] = skill.Description
		resultData["instructions"] = skill.Instructions
		resultData["instructions_length"] = len(skill.Instructions)
		resultData["files"] = files
	}

	logger.Infof(ctx, "[Tool][ReadSkill] Successfully read skill: %s/%s", ref.Source, ref.SkillID)

	return &types.ToolResult{
		Success: true,
		Output:  builder.String(),
		Data:    resultData,
	}, nil
}

// Cleanup releases any resources (implements Tool interface if needed)
func (t *ReadSkillTool) Cleanup(ctx context.Context) error {
	return nil
}
