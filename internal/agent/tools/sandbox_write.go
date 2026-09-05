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

// maxWriteSandboxBytes is the absolute ceiling on one write. It is a resource
// guard, not a claim about what the model can emit: the real limit on a single
// call is the round's completion-token budget, which is usually far smaller.
// See writeBudgetBytes.
const maxWriteSandboxBytes = 256 * 1024

// maxSandboxFileBytes bounds the file a sequence of appends can build up. One
// call still cannot carry more than maxWriteSandboxBytes; this only stops an
// unbounded append loop from filling the sandbox.
const maxSandboxFileBytes = 8 * 1024 * 1024

// bytesPerCompletionToken converts the round's completion-token budget into
// the byte length the model can actually fit in `content`.
//
// Deliberately pessimistic. A CJK character is one token and three UTF-8
// bytes; ASCII source runs three to four bytes per token; JSON string escaping
// inflates both. Advertising a size the model cannot reach is exactly the
// failure this replaces — the model plans a 256KB write, gets cut off at the
// token cap, and retries the same doomed call — so the number must err low.
const bytesPerCompletionToken = 2

// completionTokensReservedForCall covers everything in the response that is
// not file content: the tool name, the path, JSON scaffolding, and any prose
// the model emits before deciding to call the tool.
const completionTokensReservedForCall = 512

// writeBudgetBytes is the largest `content` a single call can carry given the
// round's completion-token budget. A non-positive budget means the caller does
// not know, in which case only the hard cap applies.
func writeBudgetBytes(completionTokens int) int {
	if completionTokens <= 0 {
		return maxWriteSandboxBytes
	}
	usable := completionTokens - completionTokensReservedForCall
	if usable < 1 {
		usable = 1
	}
	return min(usable*bytesPerCompletionToken, maxWriteSandboxBytes)
}

// Write modes accepted by write_sandbox_file.
const (
	writeModeOverwrite = "overwrite"
	writeModeAppend    = "append"
)

// writeSandboxMissingFieldHint is appended when schema validation fails
// (typically a truncated call that only sent `content`).
const writeSandboxMissingFieldHint = "\nIf the previous call was truncated, retry with a complete JSON object: " +
	"put `path` first (e.g. /workspace/output/script.py), then `content`. Split large files."

// SandboxFileSink is the file-store slice write_sandbox_file needs.
// Production uses *sandbox.SessionBoundManager via SessionFileStore.
//
// It is not write-only: append mode has to see the file that is already there.
// The remote backends expose no atomic append, so this reads and rewrites the
// whole file, which is what edit_sandbox_file already does.
type SandboxFileSink interface {
	StatSessionFile(ctx context.Context, sessionID, filePath string) (*sandbox.RemoteStatEntry, error)
	ReadSessionFile(ctx context.Context, sessionID, filePath string) ([]byte, error)
	WriteSessionWorkspaceFile(ctx context.Context, sessionID, filePath string, content []byte) error
}

// writeSandboxFileDescription carries one %s, filled with the size guidance
// derived from the round's completion-token budget.
const writeSandboxFileDescription = `Create, overwrite, or append a text file under /workspace, excluding /workspace/input.
Use /workspace/output for deliverables. Send both path and content (path first). Use edit_sandbox_file for small changes to an existing file. File content does not pass through shell quoting.
Large files: first call uses mode=overwrite (default), subsequent calls use mode=append with only the next chunk. Keep calls in order and inspect the reported running byte count. A refused/truncated call wrote nothing; retry that chunk with complete JSON, never duplicate successful chunks.
%s
Binary content is not accepted. The result reports the absolute path and total size without echoing content.`

// WriteSandboxFileInput defines the input parameters for write_sandbox_file.
//
// The byte limit is deliberately absent from the `content` description: it
// depends on the agent's per-round token budget and is stated in the tool
// description, which is built per session.
type WriteSandboxFileInput struct {
	Path    string `json:"path" jsonschema:"Absolute or /workspace-relative sandbox path to write. Must sit under /workspace and must not sit under /workspace/input. Prefer /workspace/output for downloadable artifacts."`
	Content string `json:"content" jsonschema:"Text to write. In overwrite mode this is the full file; in append mode it is only the next chunk. Keep near the per-call size stated in the tool description so the response is not cut off. Do not send binary bytes."` //nolint:lll // one-line struct tag
	Mode    string `json:"mode,omitempty" jsonschema:"How to apply content: 'overwrite' (default) replaces the file, 'append' adds to the end of an existing file. Use append to build a large file across several calls."`                                             //nolint:lll // one-line struct tag
}

// WriteSandboxFileTool writes a text file into the session sandbox.
type WriteSandboxFileTool struct {
	BaseTool
	sink SandboxFileSink
}

// NewWriteSandboxFileTool constructs the tool. `sink` MUST NOT be nil.
//
// completionTokens is the agent's per-round completion-token budget, which is
// what actually bounds how much the model can emit in one call. It shapes the
// size guidance in the description; it is not enforced, for the reasons in
// Execute. Pass 0 when it is not known.
func NewWriteSandboxFileTool(sink SandboxFileSink, completionTokens int) *WriteSandboxFileTool {
	return &WriteSandboxFileTool{
		BaseTool: BaseTool{
			name: ToolWriteSandboxFile,
			description: fmt.Sprintf(
				writeSandboxFileDescription,
				writeSizeGuidance(writeBudgetBytes(completionTokens)),
			),
			schema: utils.GenerateSchema[WriteSandboxFileInput](),
		},
		sink: sink,
	}
}

// writeSizeGuidance states how much content one call should carry and why.
//
// The number is a forecast of what fits in one response, not a rule the tool
// enforces — a call that lands intact is written whatever its size. The reason
// to respect it is the real one: go far past it and the response gets cut off
// mid-string, and a truncated call is refused before it ever reaches the tool.
// A model told only a number treats it as arbitrary; told that its own response
// length is the constraint, it splits the file instead of retrying.
func writeSizeGuidance(maxBytes int) string {
	if maxBytes >= maxWriteSandboxBytes {
		return fmt.Sprintf(
			"Keep `content` under about %d bytes per call. A larger file must be built "+
				"with `mode: \"append\"`. The whole file is capped at %d bytes.",
			maxBytes, maxSandboxFileBytes)
	}
	return fmt.Sprintf(
		"Keep `content` under about %d bytes per call, because that is what fits in one "+
			"response at this agent's token budget. Go much past it and the response is cut "+
			"off mid-string, which makes the call unusable and it will be refused. Split "+
			"anything longer across several calls with `mode: \"append\"`. The whole file is "+
			"capped at %d bytes.",
		maxBytes, maxSandboxFileBytes)
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

	clean := sandbox.ResolveWorkspacePath(trimmed)
	rootDir, ok := matchingWritableRoot(clean)
	if !ok {
		return &types.ToolResult{
			Success: false,
			Error:   workspaceWriteScopeError(input.Path),
		}, nil
	}

	// Only the file-size limit is enforced here, deliberately not the per-call
	// budget the description advertises.
	//
	// That budget is a forecast of what fits in one response, derived from
	// token math whose bytes-per-token factor swings by 3x between ASCII and
	// CJK. Content that reaches this point already arrived intact — a response
	// cut off at the token cap was refused in act.go, and arguments closed by
	// JSON repair were refused there too. Rejecting a complete payload for
	// beating a wrong forecast discards work already paid for and forces the
	// model to re-emit it in chunks, which costs strictly more and is more
	// likely to truncate than the call that just succeeded. Nor would the check
	// catch what it looks like it catches: a truncated write is usually SMALLER
	// than the budget and passes straight through.
	chunk := []byte(input.Content)
	if len(chunk) > maxSandboxFileBytes {
		return &types.ToolResult{
			Success: false,
			Error: fmt.Sprintf(
				"content is %d bytes, past the %d-byte file limit. Write the first part now "+
					"and send the rest with mode=%q",
				len(chunk), maxSandboxFileBytes, writeModeAppend,
			),
		}, nil
	}
	if isBinaryShellOutput(string(chunk)) {
		return &types.ToolResult{
			Success: false,
			Error:   "binary content is not accepted; write a text script and have it produce binary files under /workspace/output",
		}, nil
	}

	mode, modeErr := normalizeWriteMode(input.Mode)
	if modeErr != "" {
		return &types.ToolResult{Success: false, Error: modeErr}, nil
	}

	// Held across the read and the write: an append that reads its base while
	// a sibling call is mid-write appends to bytes that no longer exist.
	defer lockSandboxFile(sessionID, clean)()

	content := chunk
	if mode == writeModeAppend {
		existing, appendErr := t.readForAppend(ctx, sessionID, clean)
		if appendErr != "" {
			return &types.ToolResult{Success: false, Error: appendErr}, nil
		}
		if len(existing)+len(chunk) > maxSandboxFileBytes {
			return &types.ToolResult{
				Success: false,
				Error: fmt.Sprintf(
					"appending %d bytes would take %s past the %d-byte file limit (currently %d)",
					len(chunk), clean, maxSandboxFileBytes, len(existing),
				),
			}, nil
		}
		content = make([]byte, 0, len(existing)+len(chunk))
		content = append(content, existing...)
		content = append(content, chunk...)
	}

	if err := t.sink.WriteSessionWorkspaceFile(ctx, sessionID, clean, content); err != nil {
		logger.Warnf(ctx, "[Tool][WriteSandboxFile] write failed: session=%s path=%s err=%v",
			sessionID, clean, err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to write %s: %v", clean, err),
		}, nil
	}

	logger.Infof(ctx, "[Tool][WriteSandboxFile] session=%s path=%s mode=%s chunk_bytes=%d total_bytes=%d",
		sessionID, clean, mode, len(chunk), len(content))

	// Check the whole file, not just this chunk: a chunk boundary lands in the
	// middle of the source, so only the assembled result can be judged.
	added := CountContentLines(string(chunk))

	if hint := pythonScriptSyntaxHint(clean, string(content), ToolEditSandboxFile); hint != "" {
		data := map[string]interface{}{
			"display_type": ToolWriteSandboxFile,
			"session_id":   sessionID,
			"path":         clean,
			"root":         rootDir,
			"name":         path.Base(clean),
			"size":         len(content),
			"mode":         mode,
			"syntax_error": true,
		}
		attachSandboxDiffStats(data, added, 0)
		return &types.ToolResult{
			Success: false,
			Error:   hint,
			Output:  fmt.Sprintf("=== Wrote sandbox file with syntax problems: %s ===\n\n%s\n", clean, hint),
			Data:    data,
		}, nil
	}

	sizeLine := fmt.Sprintf("bytes=%d", len(content))
	if mode == writeModeAppend {
		sizeLine = fmt.Sprintf("appended=%d, total_bytes=%d", len(chunk), len(content))
	}
	if stat := formatSandboxDiffStat(added, 0); stat != "" {
		sizeLine = stat + ", " + sizeLine
	}
	output := fmt.Sprintf("=== Wrote sandbox file: %s ===\n\n%s\n", clean, sizeLine)
	data := map[string]interface{}{
		"display_type": ToolWriteSandboxFile,
		"session_id":   sessionID,
		"path":         clean,
		"root":         rootDir,
		"name":         path.Base(clean),
		"size":         len(content),
		"mode":         mode,
		"appended":     len(chunk),
	}
	attachSandboxDiffStats(data, added, 0)
	return &types.ToolResult{
		Success:     true,
		Output:      output,
		OutputFiles: sandboxOutputLinks(clean),
		Data:        data,
	}, nil
}

// normalizeWriteMode resolves the requested mode, returning a message for the
// model when it is not one this tool knows.
func normalizeWriteMode(requested string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(requested)) {
	case "", writeModeOverwrite:
		return writeModeOverwrite, ""
	case writeModeAppend:
		return writeModeAppend, ""
	default:
		return "", fmt.Sprintf("unknown mode %q; use %q (default) or %q",
			requested, writeModeOverwrite, writeModeAppend)
	}
}

// readForAppend returns the bytes an append must be added to. A missing file
// is refused rather than quietly created: a stat or read that fails for any
// other reason is indistinguishable from "not there yet", and creating the
// file would drop everything written so far. Failing tells the model exactly
// which call to repeat.
func (t *WriteSandboxFileTool) readForAppend(
	ctx context.Context, sessionID, filePath string,
) ([]byte, string) {
	stat, err := t.sink.StatSessionFile(ctx, sessionID, filePath)
	if err != nil || stat == nil {
		return nil, fmt.Sprintf(
			"cannot append to %s: it does not exist yet (%v). Write the first chunk with mode=%q, then append the rest",
			filePath, err, writeModeOverwrite)
	}
	if stat.Type != sandbox.RemoteEntryFile {
		return nil, fmt.Sprintf("cannot append to %s: it is not a regular file", filePath)
	}
	existing, err := t.sink.ReadSessionFile(ctx, sessionID, filePath)
	if err != nil {
		return nil, fmt.Sprintf("cannot append to %s: reading the current contents failed: %v", filePath, err)
	}
	return existing, ""
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
