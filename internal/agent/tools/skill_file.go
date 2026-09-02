// Package tools — write_skill_file / edit_skill_file.
//
// The installer agent's only writer used to be `shell_exec` with a heredoc.
// That caps every file at the shell's command-length limit and puts the
// content through two levels of quoting, so a requirements.json or a small
// patch script routinely arrived truncated or mangled. These two tools write
// the same bytes over the file API instead.
//
// They are separate from write_sandbox_file / edit_sandbox_file rather than a
// loosening of them. Those write /workspace, which is wiped before the
// snapshot; the skill tree lives under /opt/weknora/tenant/skills and is what
// the snapshot keeps. Sharing one tool would mean one path guard covering both
// a per-session scratch area and the shared image.
//
// Scope: one install writes one skill. The tool is constructed with that
// skill's directory and refuses everything outside it, so an installer cannot
// reach a neighbouring skill in the shared image even though its shell runs as
// root. The prompt asks for the same thing; this enforces it.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// SkillFileStore is the write surface these tools need. Production uses
// *sandbox.SessionBoundManager, whose WriteSessionFile already refuses any
// path outside the skills image root; the per-skill scope below narrows that
// to the one directory this install owns.
type SkillFileStore interface {
	StatSessionFile(ctx context.Context, sessionID, filePath string) (*sandbox.RemoteStatEntry, error)
	ReadSessionFile(ctx context.Context, sessionID, filePath string) ([]byte, error)
	WriteSessionFile(ctx context.Context, sessionID, filePath string, content []byte) error
}

var writeSkillFileTool = BaseTool{
	name: ToolWriteSkillFile,
	description: `Create or overwrite a text file inside the skill directory being installed.

## Usage
- This is the way to write a file into the skill tree. Do NOT use
  ` + "`shell_exec`" + ` with ` + "`cat`" + `, a heredoc, or ` + "`python -c`" + `:
  those hit a command-length cap and mangle quoting.
- Use it for ` + "`.weknora/requirements.json`" + `, a small wrapper script, or a
  patch to a shipped file.
- ` + pythonQuoteGuidance + `

## When NOT to Use
- To change a few lines of an existing file, call ` + "`edit_skill_file`" + `.
- To write scratch files — the skill directory is snapshotted; keep it clean.
- Binary content. Have a script produce binary files instead.

## Path Rules
- ` + "`path`" + ` MUST be absolute and inside this install's skill directory.
  A relative path is resolved against that directory.
- Any path outside it is refused, including another skill's directory.

## Size Handling
- Content is capped at 262144 bytes per call.

## Returns
- The absolute path and byte count. File contents are not echoed back.`,
	schema: utils.GenerateSchema[WriteSkillFileInput](),
}

// WriteSkillFileInput defines the input parameters for write_skill_file.
type WriteSkillFileInput struct {
	Path    string `json:"path" jsonschema:"Path of the file to write, inside the skill directory being installed. Absolute, or relative to that directory."`
	Content string `json:"content" jsonschema:"Full text contents of the file. Overwrites any existing file at path. Maximum 262144 bytes. Do not send binary bytes."`
}

// WriteSkillFileTool writes a text file into the skill directory under install.
type WriteSkillFileTool struct {
	BaseTool
	store    SkillFileStore
	skillDir string
}

// NewWriteSkillFileTool constructs the tool. `store` MUST NOT be nil and
// `skillDir` MUST be the directory of the skill this install owns.
func NewWriteSkillFileTool(store SkillFileStore, skillDir string) *WriteSkillFileTool {
	return &WriteSkillFileTool{
		BaseTool: writeSkillFileTool,
		store:    store,
		skillDir: skillDir,
	}
}

// Execute writes the file after confirming it lands inside the skill directory.
func (t *WriteSkillFileTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][WriteSkillFile] Execute started")

	var input WriteSkillFileInput
	if err := json.Unmarshal(args, &input); err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", err),
		}, nil
	}
	if t.store == nil {
		return &types.ToolResult{
			Success: false,
			Error:   "skill file writing is not available in this deployment",
		}, nil
	}

	sessionID := resolveSessionID(ctx)
	if sessionID == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "no session ID in context; write_skill_file must run inside an agent turn",
		}, nil
	}

	clean, err := resolveSkillFilePath(t.skillDir, input.Path)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}

	content := []byte(input.Content)
	if len(content) > maxWriteSandboxBytes {
		return &types.ToolResult{
			Success: false,
			Error: fmt.Sprintf(
				"content too large (%d bytes; max %d). Split the file",
				len(content), maxWriteSandboxBytes,
			),
		}, nil
	}
	if isBinaryShellOutput(input.Content) {
		return &types.ToolResult{
			Success: false,
			Error:   "binary content is not accepted; write a text file instead",
		}, nil
	}

	if err := t.store.WriteSessionFile(ctx, sessionID, clean, content); err != nil {
		logger.Warnf(ctx, "[Tool][WriteSkillFile] write failed: session=%s path=%s err=%v",
			sessionID, clean, err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to write %s: %v", clean, err),
		}, nil
	}

	logger.Infof(ctx, "[Tool][WriteSkillFile] session=%s path=%s bytes=%d",
		sessionID, clean, len(content))

	data := map[string]interface{}{
		"display_type": ToolWriteSkillFile,
		"session_id":   sessionID,
		"path":         clean,
		"root":         t.skillDir,
		"name":         path.Base(clean),
		"size":         len(content),
	}
	if hint := pythonScriptSyntaxHint(clean, input.Content, ToolEditSkillFile); hint != "" {
		data["syntax_error"] = true
		return &types.ToolResult{
			Success: false,
			Error:   hint,
			Output:  fmt.Sprintf("=== Wrote skill file with syntax problems: %s ===\n\n%s\n", clean, hint),
			Data:    data,
		}, nil
	}
	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("=== Wrote skill file: %s ===\n\nbytes=%d\n", clean, len(content)),
		Data:    data,
	}, nil
}

// Cleanup releases any resources.
func (t *WriteSkillFileTool) Cleanup(ctx context.Context) error {
	return nil
}

var editSkillFileTool = BaseTool{
	name: ToolEditSkillFile,
	description: `Replace exact text in a file inside the skill directory being installed.

## Usage
- Use this when only a few lines of an existing file need to change — a wrong
  path, an import, a constant.
- ` + "`old_string`" + ` must match the file exactly, including whitespace and
  quotes. Include a few surrounding lines so the match is unique.
- Default: the snippet must occur exactly once. Set ` + "`replace_all=true`" + `
  only when you intentionally want every occurrence changed.
- ` + pythonQuoteGuidance + `

## When NOT to Use
- Creating a new file — use ` + "`write_skill_file`" + `.
- Replacing most of the file — rewrite it with ` + "`write_skill_file`" + `.
- Binary files.

## Path Rules
- ` + "`path`" + ` MUST be inside this install's skill directory. Absolute, or
  relative to that directory.

## Size Handling
- The file (and the result) must stay within 262144 bytes.

## Returns
- The path, how many replacements were made, and the new byte count.`,
	schema: utils.GenerateSchema[EditSkillFileInput](),
}

// EditSkillFileInput defines the input parameters for edit_skill_file.
type EditSkillFileInput struct {
	Path       string `json:"path" jsonschema:"Path of an existing text file inside the skill directory being installed. Absolute, or relative to that directory."`
	OldString  string `json:"old_string" jsonschema:"Exact text to find. Include enough surrounding lines so the match is unique unless replace_all is true."`
	NewString  string `json:"new_string" jsonschema:"Replacement text. Use an empty string to delete the matched text."`
	ReplaceAll bool   `json:"replace_all,omitempty" jsonschema:"If true, replace every occurrence. If false (default), old_string must match exactly once."`
}

// EditSkillFileTool applies an exact string replacement inside the skill tree.
type EditSkillFileTool struct {
	BaseTool
	store    SkillFileStore
	skillDir string
}

// NewEditSkillFileTool constructs the tool. `store` MUST NOT be nil and
// `skillDir` MUST be the directory of the skill this install owns.
func NewEditSkillFileTool(store SkillFileStore, skillDir string) *EditSkillFileTool {
	return &EditSkillFileTool{
		BaseTool: editSkillFileTool,
		store:    store,
		skillDir: skillDir,
	}
}

// Execute reads the file, applies the replacement, and writes it back.
func (t *EditSkillFileTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][EditSkillFile] Execute started")

	var input EditSkillFileInput
	if err := json.Unmarshal(args, &input); err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", err),
		}, nil
	}
	if t.store == nil {
		return &types.ToolResult{
			Success: false,
			Error:   "skill file editing is not available in this deployment",
		}, nil
	}

	sessionID := resolveSessionID(ctx)
	if sessionID == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "no session ID in context; edit_skill_file must run inside an agent turn",
		}, nil
	}

	clean, err := resolveSkillFilePath(t.skillDir, input.Path)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}

	stat, statErr := t.store.StatSessionFile(ctx, sessionID, clean)
	if statErr != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to stat %s: %v", clean, statErr),
		}, nil
	}
	if stat != nil && stat.Type == sandbox.RemoteEntryDir {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("%s is a directory; edit_skill_file only edits files", clean),
		}, nil
	}
	if stat != nil && stat.Size > int64(maxWriteSandboxBytes) {
		return &types.ToolResult{
			Success: false,
			Error: fmt.Sprintf(
				"file too large to edit (%d bytes; max %d). Rewrite a smaller file with write_skill_file",
				stat.Size, maxWriteSandboxBytes,
			),
		}, nil
	}

	raw, err := t.store.ReadSessionFile(ctx, sessionID, clean)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to read %s: %v", clean, err),
		}, nil
	}
	if len(raw) > maxWriteSandboxBytes {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("file too large to edit (%d bytes; max %d)", len(raw), maxWriteSandboxBytes),
		}, nil
	}
	if isBinaryShellOutput(string(raw)) {
		return &types.ToolResult{
			Success: false,
			Error:   "binary files cannot be edited",
		}, nil
	}

	updated, replacements, err := applySandboxEdit(
		string(raw), input.OldString, input.NewString, input.ReplaceAll)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}
	content := []byte(updated)
	if len(content) > maxWriteSandboxBytes {
		return &types.ToolResult{
			Success: false,
			Error: fmt.Sprintf(
				"result too large (%d bytes; max %d). Shrink new_string or split the file",
				len(content), maxWriteSandboxBytes,
			),
		}, nil
	}
	if isBinaryShellOutput(updated) {
		return &types.ToolResult{
			Success: false,
			Error:   "replacement would introduce binary content, which is not accepted",
		}, nil
	}

	if err := t.store.WriteSessionFile(ctx, sessionID, clean, content); err != nil {
		logger.Warnf(ctx, "[Tool][EditSkillFile] write failed: session=%s path=%s err=%v",
			sessionID, clean, err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to write %s: %v", clean, err),
		}, nil
	}

	logger.Infof(ctx, "[Tool][EditSkillFile] session=%s path=%s replacements=%d bytes=%d",
		sessionID, clean, replacements, len(content))

	data := map[string]interface{}{
		"display_type": ToolEditSkillFile,
		"session_id":   sessionID,
		"path":         clean,
		"root":         t.skillDir,
		"name":         path.Base(clean),
		"size":         len(content),
		"replacements": replacements,
	}
	if hint := pythonScriptSyntaxHint(clean, updated, ToolEditSkillFile); hint != "" {
		data["syntax_error"] = true
		return &types.ToolResult{
			Success: false,
			Error:   hint,
			Output:  fmt.Sprintf("=== Edited skill file with syntax problems: %s ===\n\n%s\n", clean, hint),
			Data:    data,
		}, nil
	}
	return &types.ToolResult{
		Success: true,
		Output: fmt.Sprintf("=== Edited skill file: %s ===\n\nreplacements=%d\nbytes=%d\n",
			clean, replacements, len(content)),
		Data: data,
	}, nil
}

// Cleanup releases any resources.
func (t *EditSkillFileTool) Cleanup(ctx context.Context) error {
	return nil
}

// resolveSkillFilePath turns a model-supplied path into an absolute path
// proven to sit inside skillDir.
//
// A relative path is resolved against skillDir, which is what the model
// reaches for after being told the directory once. Everything is then cleaned
// and re-checked against the prefix, so "..", a symlink-looking spelling or an
// absolute path into a neighbouring skill all fail here rather than reaching
// the image. The directory itself is refused: it is not a file.
func resolveSkillFilePath(skillDir, requested string) (string, error) {
	dir := path.Clean(strings.TrimSpace(skillDir))
	if dir == "" || dir == "." || dir == "/" {
		return "", fmt.Errorf("this tool is not bound to a skill directory")
	}
	trimmed := strings.TrimSpace(requested)
	if trimmed == "" {
		return "", fmt.Errorf("path is required; write a file inside %s", dir)
	}
	if strings.ContainsRune(trimmed, 0) {
		return "", fmt.Errorf("path %q is not a valid file path", requested)
	}
	candidate := trimmed
	if !path.IsAbs(candidate) {
		candidate = path.Join(dir, candidate)
	}
	clean := path.Clean(candidate)
	if clean == dir || !strings.HasPrefix(clean, dir+"/") {
		return "", fmt.Errorf(
			"path %q is outside this install's skill directory (%s); "+
				"an install may only write its own skill",
			requested, dir,
		)
	}
	return clean, nil
}
