package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// ReadFileTool exposes one read operation over capability-scoped sources.
// Skill resources use the manager's allowlist and bundle loader, never an
// arbitrary host path. Workspace files retain the sandbox reader's guards.
type ReadFileTool struct {
	BaseTool
	workspace *workspaceFileReader
	skills    *skills.Manager
	shell     bool
}

type ReadFileInput struct {
	Path     string `json:"path" jsonschema:"File address: an available workspace path or skill://<name>/<relative-file>. Read a skill's SKILL.md before applying it."`
	Offset   int    `json:"offset,omitempty" jsonschema:"1-based line number; omit for the first page. Use the returned next_offset to continue."`
	Limit    int    `json:"limit,omitempty" jsonschema:"Maximum lines to return; defaults to 2000."`
	MaxBytes int64  `json:"max_bytes,omitempty" jsonschema:"Maximum returned text bytes, capped at 65536. The tool output budget also applies."`
}

func NewReadFileTool(source SandboxFileSource) *ReadFileTool {
	t := &ReadFileTool{BaseTool: BaseTool{name: ToolReadFile, schema: utils.GenerateSchema[ReadFileInput]()}}
	if source != nil {
		t.workspace = &workspaceFileReader{source: source}
	}
	t.updateDescription()
	return t
}

func (t *ReadFileTool) WithSkills(manager *skills.Manager, shell bool) *ReadFileTool {
	t.skills, t.shell = manager, shell
	t.updateDescription()
	return t
}

func (t *ReadFileTool) updateDescription() {
	var scopes []string
	if t.workspace != nil {
		scopes = append(scopes, "Workspace files: absolute paths under /workspace or relative paths from /workspace. Known paths can be read directly.")
	}
	if t.skills != nil && t.skills.IsEnabled() {
		scopes = append(scopes, "Skill resources: skill://<name>/SKILL.md loads the allowed skill's instructions, file list and execution guidance; skill://<name>/<relative-file> reads a bundled resource. These are package resources, not shell paths or arbitrary host files.")
	}
	t.description = "Read text from the available file sources.\n" + strings.Join(scopes, "\n") + "\noffset is a 1-based line number; limit defaults to 2000 lines. max_bytes is capped at 65536; the tool output budget also applies. Continue at the returned next_offset when truncated. Binary content is suppressed."
}

func (t *ReadFileTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var input ReadFileInput
	if err := json.Unmarshal(args, &input); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("invalid read_file arguments: %v", err)}, nil
	}
	input.Path = strings.TrimSpace(input.Path)
	if input.Path == "" {
		return &types.ToolResult{Success: false, Error: "path is required; use a known workspace path or a skill resource from the available skills list"}, nil
	}
	if strings.HasPrefix(input.Path, "skill://") {
		return t.readSkillResource(ctx, input), nil
	}
	if t.workspace == nil {
		return &types.ToolResult{Success: false, Error: "workspace file access is unavailable; only listed skill resources can be read"}, nil
	}
	if _, ok := matchingInspectableRoot(sandbox.ResolveWorkspacePath(input.Path)); !ok {
		return &types.ToolResult{Success: false, Error: "path is outside that scope: file access is limited to /workspace and listed skill:// resources; use the skill resource address from the available skills list to read its package"}, nil
	}
	return t.workspace.read(ctx, input)
}

func (t *ReadFileTool) readSkillResource(ctx context.Context, input ReadFileInput) *types.ToolResult {
	fail := func(err error) *types.ToolResult { return &types.ToolResult{Success: false, Error: err.Error()} }
	if t.skills == nil || !t.skills.IsEnabled() {
		return fail(fmt.Errorf("skills are not enabled for this reader"))
	}
	name, rel, ok := strings.Cut(strings.TrimPrefix(input.Path, "skill://"), "/")
	if !ok || name == "" || rel == "" {
		return fail(fmt.Errorf("use skill://<name>/SKILL.md or skill://<name>/<relative-file>"))
	}
	// Reject traversal before normalization; resolving one skill must never
	// authorize another skill or an absolute host path.
	for _, segment := range strings.Split(rel, "/") {
		if segment == ".." || segment == "." || segment == "" {
			return fail(fmt.Errorf("skill resource must have a canonical relative file path without traversal"))
		}
	}
	if strings.ContainsAny(name+rel, "\\\x00") {
		return fail(fmt.Errorf("invalid skill resource path"))
	}
	listed := false
	for _, metadata := range t.skills.GetAllMetadata() {
		if metadata != nil && metadata.Name == name {
			listed = true
			break
		}
	}
	if !listed {
		return fail(fmt.Errorf("skill %q is not available to this agent", name))
	}
	var content string
	if rel == skills.SkillFileName {
		skill, err := t.skills.LoadSkill(ctx, name)
		if err != nil {
			return fail(err)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "# %s\n\n%s\n\n", skill.Name, skill.Description)
		// Keep execution guidance before potentially long instructions so the
		// first page identifies the correct runtime even for large skills.
		if t.shell {
			dir, installed := t.skills.SandboxSkillDir(name)
			if !installed {
				dir = "a session directory prepared automatically from this skill package"
			}
			fmt.Fprintf(&b, "Execution: shell_exec(skill_name=%q, command=...). Use $WEKNORA_SKILL_DIR for bundled scripts (resources: %s); cwd defaults to /workspace. Installed image skills are read-only; host skill resources are staged into the session automatically (host virtualenvs and node_modules are not copied). Deliverables belong in /workspace/output.\n\n", name, dir)
		} else {
			b.WriteString("Execution is unavailable: this agent has no sandbox shell. The skill instructions can be read; configuring a shell-capable sandbox is required to run scripts.\n\n")
		}
		b.WriteString(skill.Instructions)
		files, err := t.skills.ListSkillFiles(ctx, name)
		if err != nil {
			b.WriteString("\n\n[The bundled file list is unavailable; known resource paths may still be read.]\n")
		} else if tree := formatSkillFileTree(files); tree != "" {
			fmt.Fprintf(&b, "\n\n## Bundled files\nRead these relative paths under skill://%s/:\n\n%s", name, tree)
		}
		content = b.String()
	} else {
		var err error
		content, err = t.skills.ReadSkillFile(ctx, name, rel)
		if err != nil {
			return fail(err)
		}
	}
	if int64(len(content)) > maxReadSandboxDownloadBytes {
		return fail(fmt.Errorf("skill resource exceeds the %d-byte read limit; split the package resource into smaller files", maxReadSandboxDownloadBytes))
	}
	result := renderFilePage(ctx, input, []byte(content), resolveSessionID(ctx), input.Path, "skill://"+name)
	if result.Data != nil {
		result.Data["skill_name"] = name
		result.Data["file_path"] = rel
	}
	return result
}
