// Package tools — read_sandbox_file.
//
// Read-only tool that lets the LLM read a file under the session's
// inspectable sandbox directories. Pairs with list_sandbox_files: the
// LLM lists first, then reads a specific path.
//
// Design notes:
//   - Session-scoped: path must belong to the current session's sandbox,
//     enforced by delegating to SandboxFileSource.ReadSessionFile which
//     itself takes the session ID from context.
//   - Directory guardrail: path must sit underneath the artifact output
//     directory or /workspace/input. Output is a companion to
//     ArtifactCollector; input is where chat attachments are staged.
//     Skills that need to peek elsewhere should print via stdout.
//   - Stat-first size cap: files over 64 KiB are never downloaded. The
//     model receives metadata and uses shell_exec with sed/head/tail/grep/awk
//     to inspect only the relevant text section.
//   - Binary handling: binary bytes are never returned or base64-encoded into
//     model/client data. ArtifactCollector remains the download path.
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

// A read larger than this is refused before ReadSessionFile is called. The
// caller may request a smaller ceiling, but never raise this hard limit.
const (
	defaultReadSandboxMaxBytes int64 = 64 * 1024
	maxReadSandboxMaxBytes     int64 = 64 * 1024
)

// Tool schema

var readSandboxFileTool = BaseTool{
	name: ToolReadSandboxFile,
	description: `Read the contents of a file in the current session's inspectable sandbox directories.

## Usage
- Use in tandem with ` + "`list_sandbox_files`" + `: first list to find the file,
  then read to inspect its content.
- Handy when the user asks "what did that report say?" or you want to
  quote a section of a skill-generated artifact.
- Also use this to inspect a staged chat attachment under
  ` + "`/workspace/input`" + ` when ` + "`shell_exec`" + ` is not available.

## When to Use
- After a skill claims it wrote something and you want to confirm the
  content matches expectations.
- Before invoking a follow-up skill that consumes a file — you may want
  to inspect a snippet to decide which skill to invoke or what arguments
  to pass.
- When the user asks you to summarise or edit a previously generated
  artifact.
- When ` + "`<sandbox_attachments>`" + ` lists a user-uploaded file you need to
  read without running a shell command.

## When NOT to Use
- Skill files under ` + "`/opt/weknora/tenant/skills`" + `. Use ` + "`read_skill`" + `
  with ` + "`skill_name`" + ` / ` + "`file_path`" + `.

## Path Rules
- ` + "`path`" + ` MUST be an absolute path returned by ` + "`list_sandbox_files`" + `
  or listed in the current ` + "`<sandbox_attachments>`" + ` block.
- ` + "`path`" + ` MUST sit underneath the session's artifact output directory
  (` + "`$WEKNORA_SKILL_OUTPUT_DIR`" + `, typically ` + "`/workspace/output`" + `) or the
  session input directory (` + "`/workspace/input`" + `). Reads outside those
  directories are rejected.

## Size Handling
- Files larger than 64 KiB are NOT downloaded or returned.
- For large text, use ` + "`shell_exec`" + ` with ` + "`sed -n`" + `, ` + "`head`" + `,
  ` + "`tail`" + `, ` + "`grep`" + `, or ` + "`awk`" + ` to inspect a targeted section.
- ` + "`max_bytes`" + ` may lower the refusal threshold but cannot exceed 65536.

## Binary Files
- Binary bytes are never returned to the model or embedded as base64.
- Use the ArtifactCollector download attachment for PDFs, PPTX files, images,
  archives, and other binary artifacts.`,
	schema: utils.GenerateSchema[ReadSandboxFileInput](),
}

// ReadSandboxFileInput defines the input parameters for read_sandbox_file.
type ReadSandboxFileInput struct {
	// Path is the absolute path inside the sandbox to read. Required.
	// Must sit underneath the artifact output directory or /workspace/input.
	Path string `json:"path" jsonschema:"Absolute path inside the sandbox. Must sit under the session's artifact output directory (typically /workspace/output) or /workspace/input. Get valid paths from list_sandbox_files or the current sandbox_attachments block."`
	// MaxBytes is the largest file the tool may download. Zero uses 64 KiB;
	// callers may lower but never raise the hard 64 KiB ceiling.
	MaxBytes int64 `json:"max_bytes,omitempty" jsonschema:"Optional maximum file size to read. Defaults to 65536 bytes and is hard-capped at 65536. Larger files are not downloaded; use shell_exec with sed/head/tail/grep/awk."`
}

// ReadSandboxFileTool exposes SandboxFileSource.ReadSessionFile as a
// safe, session-scoped read primitive.
type ReadSandboxFileTool struct {
	BaseTool
	source SandboxFileSource
}

// NewReadSandboxFileTool constructs the tool. `source` MUST NOT be nil:
// callers should feature-gate registration when the sandbox backend
// does not support per-session file inspection.
func NewReadSandboxFileTool(source SandboxFileSource) *ReadSandboxFileTool {
	return &ReadSandboxFileTool{
		BaseTool: readSandboxFileTool,
		source:   source,
	}
}

// Execute reads the requested file (bounded by size cap) from the
// current session's sandbox.
func (t *ReadSandboxFileTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][ReadSandboxFile] Execute started")

	var input ReadSandboxFileInput
	if err := json.Unmarshal(args, &input); err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", err),
		}, nil
	}

	if t.source == nil {
		return &types.ToolResult{
			Success: false,
			Error:   "sandbox file inspection is not available in this deployment",
		}, nil
	}

	trimmed := strings.TrimSpace(input.Path)
	if trimmed == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "path is required; call list_sandbox_files first to discover valid paths",
		}, nil
	}

	sessionID := resolveSessionID(ctx)
	if sessionID == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "no session ID in context; read_sandbox_file must run inside an agent turn",
		}, nil
	}

	// Enforce that the path sits underneath an inspectable root. This
	// mirrors list_sandbox_files so the LLM sees a consistent reachable
	// surface covering both skill output and staged attachments.
	clean := path.Clean(trimmed)
	rootDir, ok := matchingInspectableRoot(clean)
	if !ok {
		return &types.ToolResult{
			Success: false,
			Error:   inspectablePathError(input.Path),
		}, nil
	}

	// Resolve the byte cap for this call.
	maxBytes := input.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultReadSandboxMaxBytes
	}
	if maxBytes > maxReadSandboxMaxBytes {
		maxBytes = maxReadSandboxMaxBytes
	}

	stat, err := t.source.StatSessionFile(ctx, sessionID, clean)
	if err != nil {
		logger.Warnf(ctx, "[Tool][ReadSandboxFile] stat failed: session=%s path=%s err=%v",
			sessionID, clean, err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to inspect %s before reading: %v", clean, err),
		}, nil
	}
	if stat == nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("file not found: %s", clean),
		}, nil
	}
	if stat.Type == sandbox.RemoteEntryDir {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("path is a directory, not a file: %s", clean),
		}, nil
	}
	// The directory guard above is a string prefix test, so it cannot tell that
	// a symlink under the output directory points somewhere else entirely. The
	// backends stat the final component without following it, so this refuses a
	// path that names a link.
	//
	// A link in the MIDDLE of the path is still resolved by the kernel and is
	// not caught here. That leaves the artifact-directory convention evadable,
	// but not the privilege boundary: the read runs as the sandbox account, so
	// it can only return what that account could already have read via
	// shell_exec.
	if stat.Type != sandbox.RemoteEntryFile {
		return &types.ToolResult{
			Success: false,
			Error: fmt.Sprintf(
				"path is not a regular file: %s; only files under %s can be read",
				clean, inspectableRootsDescription(),
			),
		}, nil
	}
	if stat.Size > maxBytes {
		logger.Infof(ctx, "[Tool][ReadSandboxFile] refused oversize file: session=%s path=%s size=%d limit=%d",
			sessionID, clean, stat.Size, maxBytes)
		return oversizedSandboxFileResult(sessionID, clean, rootDir, stat.Size, maxBytes), nil
	}

	data, err := t.source.ReadSessionFile(ctx, sessionID, clean)
	if err != nil {
		logger.Warnf(ctx, "[Tool][ReadSandboxFile] read failed: session=%s path=%s err=%v",
			sessionID, clean, err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to read %s: %v", clean, err),
		}, nil
	}

	total := int64(len(data))
	// The file may have grown between Stat and Read. Do not return a prefix
	// from a full oversized download as if it were an intentional partial read.
	if total > maxBytes {
		logger.Warnf(ctx, "[Tool][ReadSandboxFile] file grew during read: session=%s path=%s size=%d limit=%d",
			sessionID, clean, total, maxBytes)
		return oversizedSandboxFileResult(sessionID, clean, rootDir, total, maxBytes), nil
	}

	binary := isBinaryShellOutput(string(data))

	// Build LLM-facing output. Text is inlined only once in Output; binary
	// bytes are suppressed and represented by metadata.
	var b strings.Builder
	b.WriteString(fmt.Sprintf("=== Sandbox file: %s ===\n\n", clean))
	b.WriteString(fmt.Sprintf("size=%d bytes, returned=%d bytes\n", total, len(data)))

	resultData := map[string]interface{}{
		"session_id":     sessionID,
		"path":           clean,
		"root":           rootDir,
		"size":           total,
		"returned_bytes": len(data),
		"truncated":      false,
		"binary":         binary,
	}

	if binary {
		b.WriteString("binary file — content suppressed; use the artifact attachment to download it.\n")
	} else {
		b.WriteString("\n```\n")
		b.Write(data)
		if len(data) > 0 && data[len(data)-1] != '\n' {
			b.WriteString("\n")
		}
		b.WriteString("```\n")
	}

	logger.Infof(ctx, "[Tool][ReadSandboxFile] session=%s path=%s size=%d returned=%d binary=%v",
		sessionID, clean, total, len(data), binary)

	return &types.ToolResult{
		Success: true,
		Output:  b.String(),
		Data:    resultData,
	}, nil
}

func oversizedSandboxFileResult(sessionID, filePath, rootDir string, size, limit int64) *types.ToolResult {
	output := fmt.Sprintf(
		"=== Sandbox file too large to read: %s ===\n\n"+
			"size=%d bytes, limit=%d bytes, returned=0 bytes\n\n"+
			"The file was not downloaded. Use shell_exec with sed -n, head, tail, grep, or awk "+
			"to inspect only the relevant text section. Binary files remain available through the artifact attachment.\n",
		filePath, size, limit,
	)
	return &types.ToolResult{
		Success: true,
		Output:  output,
		Data: map[string]interface{}{
			"session_id":     sessionID,
			"path":           filePath,
			"root":           rootDir,
			"size":           size,
			"limit":          limit,
			"returned_bytes": 0,
			"truncated":      true,
			"read_refused":   true,
		},
	}
}

// Cleanup releases any resources.
func (t *ReadSandboxFileTool) Cleanup(ctx context.Context) error {
	return nil
}
