// Package tools — edit_sandbox_file.
//
// Surgical text replacement for a file already in the session sandbox.
// write_sandbox_file is the right first step for a new script; this tool
// exists so a one-line path fix (or similar) does not force the model to
// regenerate the whole file — that burns tokens and often truncates.
//
// Design notes:
//   - Same writable roots as write_sandbox_file: /workspace except
//     /workspace/input.
//   - Default is a unique match (Cursor-style). replace_all=true replaces
//     every occurrence. 0 matches or an ambiguous match without replace_all
//     fail without writing.
//   - Result is path + replacement count + size; contents are not echoed.
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

// editSandboxMissingFieldHint is appended when schema validation fails
// (typically a truncated call that omitted path or old_string).
const editSandboxMissingFieldHint = "\nIf the previous call was truncated, retry with a complete JSON object: " +
	"put `path` first, then `old_string` and `new_string`. Do not send the whole file — this tool replaces a snippet."

// SandboxFileEditor reads then writes a session workspace file. Production
// uses *sandbox.SessionBoundManager via SessionFileStore.
type SandboxFileEditor interface {
	StatSessionFile(ctx context.Context, sessionID, filePath string) (*sandbox.RemoteStatEntry, error)
	ReadSessionFile(ctx context.Context, sessionID, filePath string) ([]byte, error)
	WriteSessionWorkspaceFile(ctx context.Context, sessionID, filePath string, content []byte) error
}

var editSandboxFileTool = BaseTool{
	name: ToolEditSandboxFile,
	description: `Replace exact text in an existing sandbox file without rewriting the whole file.

## Usage
- Use this after ` + "`write_sandbox_file`" + ` (or a previous edit) when only a
  few lines need to change — a wrong output path, a typo, a constant.
- ` + "`old_string`" + ` must match the file exactly, including whitespace and
  quotes. Include a few surrounding lines so the match is unique.
- Default: the snippet must occur exactly once. Set ` + "`replace_all=true`" + `
  only when you intentionally want every occurrence changed.
- Do NOT call ` + "`write_sandbox_file`" + ` with the full file to fix one line.
- ` + pythonQuoteGuidance + `

## When to Use
- A script failed because one path, import, or constant is wrong.
- Renaming a variable or output filename that appears once (or everywhere
  with ` + "`replace_all`" + `).
- Deleting a short block by setting ` + "`new_string`" + ` to empty.

## When NOT to Use
- Creating a new file — use ` + "`write_sandbox_file`" + `.
- Replacing most of the file — rewrite with ` + "`write_sandbox_file`" + `.
- Editing under ` + "`/workspace/input`" + ` (attachments are read-only).
- Binary files.

## Path Rules
- ` + "`path`" + ` MUST be an absolute path under ` + "`/workspace`" + `, not under
  ` + "`/workspace/input`" + `, and not ` + "`/workspace`" + ` or ` + "`/workspace/output`" + `
  themselves.

## Size Handling
- The file (and the result) must stay within 262144 bytes.

## Returns
- The path, how many replacements were made, and the new byte count.
  File contents are not echoed back.`,
	schema: utils.GenerateSchema[EditSandboxFileInput](),
}

// EditSandboxFileInput defines the input parameters for edit_sandbox_file.
type EditSandboxFileInput struct {
	Path       string `json:"path" jsonschema:"Absolute sandbox path of an existing text file under /workspace (not /workspace/input)."`
	OldString  string `json:"old_string" jsonschema:"Exact text to find. Include enough surrounding lines so the match is unique unless replace_all is true."`
	NewString  string `json:"new_string" jsonschema:"Replacement text. Use an empty string to delete the matched text."`
	ReplaceAll bool   `json:"replace_all,omitempty" jsonschema:"If true, replace every occurrence. If false (default), old_string must match exactly once."`
}

// EditSandboxFileTool applies an exact string replacement to a sandbox file.
type EditSandboxFileTool struct {
	BaseTool
	editor SandboxFileEditor
}

// NewEditSandboxFileTool constructs the tool. `editor` MUST NOT be nil.
func NewEditSandboxFileTool(editor SandboxFileEditor) *EditSandboxFileTool {
	return &EditSandboxFileTool{
		BaseTool: editSandboxFileTool,
		editor:   editor,
	}
}

// Execute reads the file, applies the replacement, and writes it back.
func (t *EditSandboxFileTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][EditSandboxFile] Execute started")

	var input EditSandboxFileInput
	if err := json.Unmarshal(args, &input); err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", err),
		}, nil
	}

	if t.editor == nil {
		return &types.ToolResult{
			Success: false,
			Error:   "sandbox file editing is not available in this deployment",
		}, nil
	}

	trimmed := strings.TrimSpace(input.Path)
	if trimmed == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "path is required; edit a file under /workspace (not /workspace/input)",
		}, nil
	}

	sessionID := resolveSessionID(ctx)
	if sessionID == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "no session ID in context; edit_sandbox_file must run inside an agent turn",
		}, nil
	}

	clean := path.Clean(trimmed)
	rootDir, ok := matchingWritableRoot(clean)
	if !ok {
		return &types.ToolResult{
			Success: false,
			Error:   workspaceWriteScopeError(input.Path),
		}, nil
	}

	stat, err := t.editor.StatSessionFile(ctx, sessionID, clean)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to stat %s: %v", clean, err),
		}, nil
	}
	if stat != nil && stat.Type == sandbox.RemoteEntryDir {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("%s is a directory; edit_sandbox_file only edits files", clean),
		}, nil
	}
	if stat != nil && stat.Size > int64(maxWriteSandboxBytes) {
		return &types.ToolResult{
			Success: false,
			Error: fmt.Sprintf(
				"file too large to edit (%d bytes; max %d). Split the work or rewrite a smaller file with write_sandbox_file",
				stat.Size, maxWriteSandboxBytes,
			),
		}, nil
	}

	raw, err := t.editor.ReadSessionFile(ctx, sessionID, clean)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to read %s: %v", clean, err),
		}, nil
	}
	if len(raw) > maxWriteSandboxBytes {
		return &types.ToolResult{
			Success: false,
			Error: fmt.Sprintf(
				"file too large to edit (%d bytes; max %d)",
				len(raw), maxWriteSandboxBytes,
			),
		}, nil
	}
	if isBinaryShellOutput(string(raw)) {
		return &types.ToolResult{
			Success: false,
			Error:   "binary files cannot be edited; write a text script and have it produce binary artifacts under /workspace/output",
		}, nil
	}

	updated, replacements, err := applySandboxEdit(string(raw), input.OldString, input.NewString, input.ReplaceAll)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   err.Error(),
		}, nil
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

	if err := t.editor.WriteSessionWorkspaceFile(ctx, sessionID, clean, content); err != nil {
		logger.Warnf(ctx, "[Tool][EditSandboxFile] write failed: session=%s path=%s err=%v",
			sessionID, clean, err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to write %s: %v", clean, err),
		}, nil
	}

	logger.Infof(ctx, "[Tool][EditSandboxFile] session=%s path=%s replacements=%d bytes=%d",
		sessionID, clean, replacements, len(content))

	if hint := pythonScriptSyntaxHint(clean, updated); hint != "" {
		return &types.ToolResult{
			Success: false,
			Error:   hint,
			Output:  fmt.Sprintf("=== Edited sandbox file with syntax problems: %s ===\n\n%s\n", clean, hint),
			Data: map[string]interface{}{
				"display_type": ToolEditSandboxFile,
				"session_id":   sessionID,
				"path":         clean,
				"root":         rootDir,
				"name":         path.Base(clean),
				"size":         len(content),
				"replacements": replacements,
				"syntax_error": true,
			},
		}, nil
	}

	output := fmt.Sprintf(
		"=== Edited sandbox file: %s ===\n\nreplacements=%d\nbytes=%d\n",
		clean, replacements, len(content),
	)
	return &types.ToolResult{
		Success: true,
		Output:  output,
		Data: map[string]interface{}{
			"display_type": ToolEditSandboxFile,
			"session_id":   sessionID,
			"path":         clean,
			"root":         rootDir,
			"name":         path.Base(clean),
			"size":         len(content),
			"replacements": replacements,
		},
	}, nil
}

// Cleanup releases any resources.
func (t *EditSandboxFileTool) Cleanup(ctx context.Context) error {
	return nil
}

// applySandboxEdit performs an exact string replacement. replaceAll=false
// requires a unique match.
func applySandboxEdit(content, oldString, newString string, replaceAll bool) (string, int, error) {
	if oldString == "" {
		return "", 0, fmt.Errorf("old_string is required; copy the exact text to change, including whitespace")
	}
	if oldString == newString {
		return "", 0, fmt.Errorf("old_string and new_string are identical; no change would be made")
	}
	n := strings.Count(content, oldString)
	if n == 0 {
		return "", 0, fmt.Errorf("old_string was not found in the file. Copy the exact text (including whitespace) from the file")
	}
	if n > 1 && !replaceAll {
		return "", 0, fmt.Errorf(
			"old_string matched %d times. Include more surrounding context so it is unique, or set replace_all=true",
			n,
		)
	}
	if replaceAll {
		return strings.ReplaceAll(content, oldString, newString), n, nil
	}
	return strings.Replace(content, oldString, newString, 1), 1, nil
}
