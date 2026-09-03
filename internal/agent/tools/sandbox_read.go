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
//   - Directory guardrail: path must sit underneath /workspace, matching
//     what this session may write. Skills that need to peek outside it
//     should print via stdout.
//   - Paginated reads: a page is bounded by a line count, a byte budget, and
//     the registry's rune budget, and a partial page ends with the offset to
//     continue from. Files over 8 MiB are never downloaded; the model gets
//     metadata and uses shell_exec with sed/head/tail/grep/awk instead.
//   - Binary handling: binary bytes are never returned or base64-encoded into
//     model/client data. ArtifactCollector remains the download path.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

const (
	// How much of the file one call may put in front of the model. This bounds
	// context, not I/O: a larger file is paginated, not refused. The caller may
	// lower it but never raise it.
	defaultReadSandboxMaxBytes int64 = 64 * 1024
	maxReadSandboxMaxBytes     int64 = 64 * 1024

	// The line ceiling for one page, so a file of many short lines does not
	// arrive as thousands of nearly empty rows.
	defaultReadSandboxMaxLines = 2000

	// Room left for the header, the fences, and the continuation hint when
	// sizing a page against the registry's output budget.
	readSandboxPageOverhead = 512

	// How large a file may be and still be downloaded to serve a page. This is
	// the I/O bound, and it matches what write_sandbox_file can build by
	// appending: a file the agent can write but can never read back is a dead
	// end, and shell_exec is not registered in every deployment.
	maxReadSandboxDownloadBytes int64 = maxSandboxFileBytes
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
- ` + "`path`" + ` MUST be an absolute path under ` + "`/workspace`" + `: an
  artifact under ` + "`/workspace/output`" + `, an attachment listed in
  ` + "`<sandbox_attachments>`" + `, or a scratch file you wrote yourself with
  ` + "`write_sandbox_file`" + `. Reads outside ` + "`/workspace`" + ` are rejected.

## Size Handling
- One call returns at most 2000 lines, and as much text as fits this agent's
  tool-output budget. A larger file is paginated, not refused.
- A partial page ends with the exact ` + "`offset`" + ` to continue from. Repeat
  the call with it until the file is exhausted; do not guess the offset.
- ` + "`max_bytes`" + ` may lower the per-call byte budget but cannot exceed 65536.
- Files larger than 8 MiB are not downloaded at all; use ` + "`shell_exec`" + `
  with ` + "`sed -n`" + `, ` + "`head`" + `, ` + "`tail`" + `, ` + "`grep`" + `, or
  ` + "`awk`" + ` to inspect a targeted section.

## Binary Files
- Binary bytes are never returned to the model or embedded as base64.
- Use the ArtifactCollector download attachment for PDFs, PPTX files, images,
  archives, and other binary artifacts.`,
	schema: utils.GenerateSchema[ReadSandboxFileInput](),
}

// ReadSandboxFileInput defines the input parameters for read_sandbox_file.
type ReadSandboxFileInput struct {
	// Path is the absolute path inside the sandbox to read. Required.
	// Must sit underneath /workspace.
	Path string `json:"path" jsonschema:"Absolute path under /workspace: an artifact, an attachment, or a file you wrote yourself. Get paths from list_sandbox_files or sandbox_attachments."` //nolint:lll // one-line struct tag
	// Offset is the 1-indexed line to start the page at. Zero means the
	// beginning, so an unset field reads the file from the top.
	Offset int `json:"offset,omitempty" jsonschema:"Line number to start reading from, 1-indexed. Omit to read from the start. When a previous call reported it returned a partial page, pass the offset it gave you to continue."` //nolint:lll // one-line struct tag
	// Limit caps the lines in one page. Zero uses defaultReadSandboxMaxLines.
	Limit int `json:"limit,omitempty" jsonschema:"Maximum number of lines to return. Defaults to 2000."`
	// MaxBytes caps the bytes one call returns. Zero uses 64 KiB; callers may
	// lower but never raise the hard 64 KiB ceiling.
	MaxBytes int64 `json:"max_bytes,omitempty" jsonschema:"Optional maximum bytes to return in this call. Defaults to 65536 and is hard-capped at 65536. This paginates the file rather than refusing it."` //nolint:lll // one-line struct tag
}

// ReadSandboxFileTool exposes SandboxFileSource.ReadSessionFile as a
// safe, session-scoped read primitive.
type ReadSandboxFileTool struct {
	BaseTool
	source SandboxFileSource

	mu     sync.Mutex
	cached *readCacheEntry
}

// readCacheEntry holds the last file downloaded, so consecutive pages of one
// file do not each pull it again.
type readCacheEntry struct {
	sessionID string
	path      string
	size      int64
	modTime   time.Time
	epoch     uint64
	data      []byte
}

// readWithCache returns the file contents, reusing the previous download when
// nothing indicates the file changed.
//
// A page is cut out of the whole file, and none of the sandbox backends expose
// a range read — RemoteClient.ReadFile takes a path and returns everything. So
// without this, every page downloads the file again: paging a 1 MB artifact at
// roughly 23 KB per page is 45 full downloads and, worse, 45 round trips. The
// backends have no range read, so the only way to page is to keep the last
// download and slice it.
//
// One slot rather than a map: paging walks a single file, and a slot bounds
// what is held to one file instead of to every file the turn happened to read.
// The tool instance lives for one agent run, so nothing outlives the turn.
func (t *ReadSandboxFileTool) readWithCache(
	ctx context.Context, sessionID, filePath string, stat *sandbox.RemoteStatEntry,
) ([]byte, error) {
	epoch := sandboxMutationEpoch()

	t.mu.Lock()
	hit := t.cached
	t.mu.Unlock()

	if hit != nil && hit.sessionID == sessionID && hit.path == filePath &&
		hit.size == stat.Size && hit.modTime.Equal(stat.ModTime) && hit.epoch == epoch {
		return hit.data, nil
	}

	data, err := t.source.ReadSessionFile(ctx, sessionID, filePath)
	if err != nil {
		return nil, err
	}

	t.mu.Lock()
	t.cached = &readCacheEntry{
		sessionID: sessionID,
		path:      filePath,
		size:      stat.Size,
		modTime:   stat.ModTime,
		epoch:     epoch,
		data:      data,
	}
	t.mu.Unlock()

	return data, nil
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
	if stat.Size > maxReadSandboxDownloadBytes {
		logger.Infof(ctx, "[Tool][ReadSandboxFile] refused oversize file: session=%s path=%s size=%d limit=%d",
			sessionID, clean, stat.Size, maxReadSandboxDownloadBytes)
		return oversizedSandboxFileResult(sessionID, clean, rootDir, stat.Size), nil
	}

	data, err := t.readWithCache(ctx, sessionID, clean, stat)
	if err != nil {
		logger.Warnf(ctx, "[Tool][ReadSandboxFile] read failed: session=%s path=%s err=%v",
			sessionID, clean, err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to read %s: %v", clean, err),
		}, nil
	}

	total := int64(len(data))
	// The file may have grown between Stat and Read.
	if total > maxReadSandboxDownloadBytes {
		logger.Warnf(ctx, "[Tool][ReadSandboxFile] file grew during read: session=%s path=%s size=%d limit=%d",
			sessionID, clean, total, maxReadSandboxDownloadBytes)
		return oversizedSandboxFileResult(sessionID, clean, rootDir, total), nil
	}

	binary := isBinaryShellOutput(string(data))

	resultData := map[string]interface{}{
		"session_id": sessionID,
		"path":       clean,
		"root":       rootDir,
		"size":       total,
		"binary":     binary,
	}

	// Build LLM-facing output. Text is inlined only once in Output; binary
	// bytes are suppressed and represented by metadata.
	var b strings.Builder
	fmt.Fprintf(&b, "=== Sandbox file: %s ===\n\n", clean)

	if binary {
		fmt.Fprintf(&b, "size=%d bytes, returned=0 bytes\n", total)
		b.WriteString("binary file — content suppressed; use the artifact attachment to download it.\n")
		resultData["returned_bytes"] = 0
		resultData["truncated"] = false
		logger.Infof(ctx, "[Tool][ReadSandboxFile] session=%s path=%s size=%d binary", sessionID, clean, total)
		return &types.ToolResult{Success: true, Output: b.String(), Data: resultData}, nil
	}

	// The registry truncates any output past its rune ceiling by deleting the
	// MIDDLE and keeping head and tail. On a page that would be the worst
	// possible outcome: the continuation hint lives at the tail, so it would
	// survive and certify "lines 1-2000 shown" while a slab out of the middle
	// was silently dropped. Sizing the page against the same budget the
	// registry enforces means that truncation never fires here.
	//
	// The two budgets are also in different units, and the mismatch runs
	// opposite to intuition: 64 KiB of CJK is ~22k runes and fits, while 64 KiB
	// of ASCII is 65k runes and does not.
	maxRunes := max(OutputBudget(ctx)-readSandboxPageOverhead, 1)
	page := paginateSandboxFile(string(data), input.Offset, input.Limit, maxBytes, maxRunes)

	// A line wider than the whole byte budget cannot be paged around: every
	// retry would land on the same line and return the same nothing. Name the
	// escape hatch instead of letting the model rediscover the wall.
	if page.lineTooLarge {
		fmt.Fprintf(&b,
			"size=%d bytes, returned=0 bytes\n\n"+
				"[Line %d is %d bytes, over the %d byte budget for one call. "+
				"Use shell_exec: sed -n '%dp' %s | head -c %d]\n",
			total, page.startLine, page.lineBytes, maxBytes, page.startLine, clean, maxBytes,
		)
		resultData["returned_bytes"] = 0
		resultData["truncated"] = true
		resultData["total_lines"] = page.totalLines
		return &types.ToolResult{Success: true, Output: b.String(), Data: resultData}, nil
	}

	fmt.Fprintf(&b, "size=%d bytes, returned=%d bytes\n", total, len(page.text))
	b.WriteString("\n```\n")
	b.WriteString(page.text)
	if page.text != "" && !strings.HasSuffix(page.text, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("```\n")

	// The continuation hint rides on the result rather than the system prompt:
	// it reaches the model at the moment it is actionable, and it carries the
	// exact offset so continuing does not require arithmetic.
	if page.nextOffset > 0 {
		fmt.Fprintf(&b,
			"\n[Showing lines %d-%d of %d. Use offset=%d to continue.]\n",
			page.startLine, page.endLine, page.totalLines, page.nextOffset,
		)
	} else if page.startLine > 1 {
		fmt.Fprintf(&b,
			"\n[Showing lines %d-%d of %d — end of file.]\n",
			page.startLine, page.endLine, page.totalLines,
		)
	}

	resultData["returned_bytes"] = len(page.text)
	resultData["truncated"] = page.nextOffset > 0
	resultData["total_lines"] = page.totalLines
	resultData["start_line"] = page.startLine
	resultData["end_line"] = page.endLine
	if page.nextOffset > 0 {
		resultData["next_offset"] = page.nextOffset
	}

	logger.Infof(ctx, "[Tool][ReadSandboxFile] session=%s path=%s size=%d lines=%d-%d/%d returned=%d",
		sessionID, clean, total, page.startLine, page.endLine, page.totalLines, len(page.text))

	return &types.ToolResult{Success: true, Output: b.String(), Data: resultData}, nil
}

// sandboxFilePage is one window of a file's lines plus what the model needs to
// ask for the next one.
type sandboxFilePage struct {
	text       string
	startLine  int // 1-indexed, inclusive
	endLine    int // 1-indexed, inclusive
	totalLines int
	nextOffset int // 0 once the page reaches the end of the file

	// lineTooLarge marks the case where a single line exceeds the byte budget,
	// so paging cannot make progress at all.
	lineTooLarge bool
	lineBytes    int
}

// paginateSandboxFile returns the window of content starting at the 1-indexed
// offset, bounded by a line count, a byte budget, and a rune budget. Whichever
// bound is hit first ends the page.
//
// Pages always break at a line boundary, which is what keeps them UTF-8 safe:
// 0x0A cannot occur inside a multi-byte sequence, so splitting on newlines can
// never cut a rune in half no matter where the byte budget lands.
func paginateSandboxFile(content string, offset, limit int, maxBytes int64, maxRunes int) sandboxFilePage {
	if offset <= 0 {
		offset = 1
	}
	if limit <= 0 {
		limit = defaultReadSandboxMaxLines
	}
	if maxRunes <= 0 {
		maxRunes = int(maxBytes)
	}

	lines := strings.Split(content, "\n")
	// A trailing newline terminates the last line; it does not begin an empty
	// one. Without this a file of N lines reports N+1 and the final page is
	// always blank.
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	total := len(lines)

	if offset > total {
		return sandboxFilePage{startLine: offset, endLine: offset - 1, totalLines: total}
	}

	start := offset - 1
	var b strings.Builder
	var usedBytes int64
	var usedRunes int
	end := start
	for ; end < total && end-start < limit; end++ {
		costBytes := int64(len(lines[end])) + 1
		costRunes := utf8.RuneCountInString(lines[end]) + 1
		if usedBytes+costBytes > maxBytes || usedRunes+costRunes > maxRunes {
			if end == start {
				return sandboxFilePage{
					startLine:    offset,
					totalLines:   total,
					lineTooLarge: true,
					lineBytes:    len(lines[end]),
				}
			}
			break
		}
		b.WriteString(lines[end])
		b.WriteByte('\n')
		usedBytes += costBytes
		usedRunes += costRunes
	}

	page := sandboxFilePage{
		text:       b.String(),
		startLine:  offset,
		endLine:    end,
		totalLines: total,
	}
	if end < total {
		page.nextOffset = end + 1
	}
	return page
}

func oversizedSandboxFileResult(sessionID, filePath, rootDir string, size int64) *types.ToolResult {
	output := fmt.Sprintf(
		"=== Sandbox file too large to read: %s ===\n\n"+
			"size=%d bytes, limit=%d bytes, returned=0 bytes\n\n"+
			"The file was not downloaded. Use shell_exec with sed -n, head, tail, grep, or awk "+
			"to inspect only the relevant text section. Binary files remain available through the artifact attachment.\n",
		filePath, size, maxReadSandboxDownloadBytes,
	)
	return &types.ToolResult{
		Success: true,
		Output:  output,
		Data: map[string]interface{}{
			"session_id":     sessionID,
			"path":           filePath,
			"root":           rootDir,
			"size":           size,
			"limit":          maxReadSandboxDownloadBytes,
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
