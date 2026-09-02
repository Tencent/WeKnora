// Package tools — write_sandbox_file.
//
// Lets the LLM write a text file into the current session's sandbox without
// stuffing the bytes through a shell_exec heredoc. shell_exec keeps an 8 KiB
// command cap; generated scripts (PPT builders, reports) routinely exceed it.
//
// Design notes:
//   - Session-scoped: the sandbox is resolved from ToolExecContext.SessionID.
//   - Path guardrail: writes sit under /workspace and never under
//     /workspace/input (staged attachments stay read-only). Prefer
//     /workspace/output for files the user should download.
//   - Content stays out of ToolResult.Data/Output: the model already has the
//     bytes it just sent. The result is path + size so the next call can
//     shell_exec the file.
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

const maxWriteSandboxBytes = 256 * 1024

// writeSandboxMissingFieldHint is appended when schema validation fails
// (typically a truncated call that only sent `content`).
const writeSandboxMissingFieldHint = "\nIf the previous call was truncated, retry with a complete JSON object: " +
	"put `path` first (e.g. /workspace/output/script.py), then `content`. Split large files."

// SandboxFileSink is the write-side counterpart of SandboxFileSource.
// Production uses *sandbox.SessionBoundManager via SessionFileStore.
type SandboxFileSink interface {
	WriteSessionWorkspaceFile(ctx context.Context, sessionID, filePath string, content []byte) error
}

var writeSandboxFileTool = BaseTool{
	name: ToolWriteSandboxFile,
	description: `Write a text file into the current session's sandbox.

## Usage
- This is the way to create or overwrite a script, report, or other text
  file. Do NOT dump large files through ` + "`shell_exec`" + ` with ` + "`cat`" + `,
  heredocs, or ` + "`python -c`" + ` — those hit a small command-length cap.
- After writing a script that needs a skill's packages, run it with
  ` + "`execute_skill_script(skill_name=..., script_path=<this path>)`" + `
  so the skill's virtualenv is used. Independent scripts: ` + "`shell_exec`" + `,
  e.g. ` + "`python3 /workspace/output/generate_ppt.py`" + `.
- Put user-facing artifacts (pptx, pdf, png, html) under
  ` + "`/workspace/output`" + ` so they can be collected for download. Scratch
  scripts may live anywhere under ` + "`/workspace`" + ` except
  ` + "`/workspace/input`" + `.
- JSON arguments MUST include both ` + "`path`" + ` and ` + "`content`" + `.
  Emit ` + "`path`" + ` first. If a write would be huge, split it across
  multiple files instead of one giant ` + "`content`" + ` string.
- ` + pythonQuoteGuidance + `

## When to Use
- Generating a Python/JS/HTML file the sandbox will execute next.
- Saving a long report or config that does not fit in a shell command.
- Overwriting a file you previously wrote in this session.

## When NOT to Use
- To change a few lines of a file you already wrote, call
  ` + "`edit_sandbox_file`" + ` instead of sending the whole file again.
- Do not write under ` + "`/workspace/input`" + `: that tree is reserved for
  user-uploaded attachments and is read-only.
- Do not write binary bytes. Have a script produce binary artifacts under
  ` + "`/workspace/output`" + `.

## Path Rules
- ` + "`path`" + ` MUST be an absolute path under ` + "`/workspace`" + `.
- ` + "`/workspace`" + `, ` + "`/workspace/output`" + `, and ` + "`/workspace/input`" + `
  themselves are directories and cannot be used as the file path.

## Size Handling
- Content is capped at 262144 bytes per call.

## Returns
- The absolute path and byte count. File contents are not echoed back.`,
	schema: utils.GenerateSchema[WriteSandboxFileInput](),
}

// WriteSandboxFileInput defines the input parameters for write_sandbox_file.
type WriteSandboxFileInput struct {
	Path    string `json:"path" jsonschema:"Absolute sandbox path to write. Must sit under /workspace and must not sit under /workspace/input. Prefer /workspace/output for downloadable artifacts."`
	Content string `json:"content" jsonschema:"Full text contents of the file. Overwrites any existing file at path. Maximum 262144 bytes. Do not send binary bytes."`
}

// WriteSandboxFileTool writes a text file into the session sandbox.
type WriteSandboxFileTool struct {
	BaseTool
	sink SandboxFileSink
}

// NewWriteSandboxFileTool constructs the tool. `sink` MUST NOT be nil.
func NewWriteSandboxFileTool(sink SandboxFileSink) *WriteSandboxFileTool {
	return &WriteSandboxFileTool{
		BaseTool: writeSandboxFileTool,
		sink:     sink,
	}
}

// Execute writes the requested file into the current session's sandbox.
func (t *WriteSandboxFileTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][WriteSandboxFile] Execute started")

	var input WriteSandboxFileInput
	if err := json.Unmarshal(args, &input); err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", err),
		}, nil
	}

	if t.sink == nil {
		return &types.ToolResult{
			Success: false,
			Error:   "sandbox file writing is not available in this deployment",
		}, nil
	}

	trimmed := strings.TrimSpace(input.Path)
	if trimmed == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "path is required; write under /workspace/output for artifacts or /workspace for scratch scripts",
		}, nil
	}

	sessionID := resolveSessionID(ctx)
	if sessionID == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "no session ID in context; write_sandbox_file must run inside an agent turn",
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

	content := []byte(input.Content)
	if len(content) > maxWriteSandboxBytes {
		return &types.ToolResult{
			Success: false,
			Error: fmt.Sprintf(
				"content too large (%d bytes; max %d). Split the work across files or shrink the script",
				len(content), maxWriteSandboxBytes,
			),
		}, nil
	}
	if isBinaryShellOutput(string(content)) {
		return &types.ToolResult{
			Success: false,
			Error:   "binary content is not accepted; write a text script and have it produce binary files under /workspace/output",
		}, nil
	}

	if err := t.sink.WriteSessionWorkspaceFile(ctx, sessionID, clean, content); err != nil {
		logger.Warnf(ctx, "[Tool][WriteSandboxFile] write failed: session=%s path=%s err=%v",
			sessionID, clean, err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to write %s: %v", clean, err),
		}, nil
	}

	logger.Infof(ctx, "[Tool][WriteSandboxFile] session=%s path=%s bytes=%d",
		sessionID, clean, len(content))

	if hint := pythonScriptSyntaxHint(clean, input.Content, ToolEditSandboxFile); hint != "" {
		return &types.ToolResult{
			Success: false,
			Error:   hint,
			Output:  fmt.Sprintf("=== Wrote sandbox file with syntax problems: %s ===\n\n%s\n", clean, hint),
			Data: map[string]interface{}{
				"display_type": ToolWriteSandboxFile,
				"session_id":   sessionID,
				"path":         clean,
				"root":         rootDir,
				"name":         path.Base(clean),
				"size":         len(content),
				"syntax_error": true,
			},
		}, nil
	}

	output := fmt.Sprintf(
		"=== Wrote sandbox file: %s ===\n\nbytes=%d\n\n"+
			"If this script needs a skill's packages, run it with\n"+
			"execute_skill_script(skill_name=<skill>, script_path=%s)\n"+
			"so the skill's virtualenv is used. Independent scripts:\n"+
			"shell_exec python3 %s\n\n"+
			"User-facing artifacts should land under %s.\n",
		clean, len(content), clean, clean, sandbox.SessionOutputRoot,
	)
	return &types.ToolResult{
		Success: true,
		Output:  output,
		Data: map[string]interface{}{
			"display_type": ToolWriteSandboxFile,
			"session_id":   sessionID,
			"path":         clean,
			"root":         rootDir,
			"name":         path.Base(clean),
			"size":         len(content),
		},
	}, nil
}

// Cleanup releases any resources.
func (t *WriteSandboxFileTool) Cleanup(ctx context.Context) error {
	return nil
}

// workspaceWriteScopeError explains a refused write/edit path. This is a
// tool-scope convention (attachments stay out of these tools; scripts go
// under /workspace), not a privilege check — shell_exec can already write
// the same session sandbox.
func workspaceWriteScopeError(requested string) string {
	return fmt.Sprintf(
		"this tool only writes files under %s (not under %s, and not the directory roots themselves). path %q is outside that scope; use shell_exec for other locations",
		sandbox.SessionWorkspaceRoot, sandbox.SessionInputRoot, requested,
	)
}

// matchingWritableRoot returns the workspace root that contains clean, or
// ("", false) when the path is outside /workspace, is /workspace itself, or
// sits under the read-only attachment tree.
func matchingWritableRoot(clean string) (string, bool) {
	if !isUnderRoot(clean, sandbox.SessionWorkspaceRoot) ||
		clean == sandbox.SessionWorkspaceRoot ||
		clean == sandbox.SessionOutputRoot ||
		isUnderRoot(clean, sandbox.SessionInputRoot) {
		return "", false
	}
	if isUnderRoot(clean, sandbox.SessionOutputRoot) {
		return sandbox.SessionOutputRoot, true
	}
	return sandbox.SessionWorkspaceRoot, true
}
