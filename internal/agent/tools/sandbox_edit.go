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
//   - One input shape: every change is an entry in edits[]. Each entry resolves
//     against the original content, so a batch is order-independent and
//     overlaps are rejected rather than silently corrupting the file.
//   - Default is a unique match (Cursor-style). Per-entry replace_all replaces
//     every occurrence. 0 matches or an ambiguous match without replace_all
//     fail without writing.
//   - Result is path + replacement count + size; contents are not echoed.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// editSandboxMissingFieldHint is appended when schema validation fails
// (typically a truncated call that omitted path or old_string).
const editSandboxMissingFieldHint = "\nIf the previous call was truncated, retry with a complete JSON object: " +
	"put `path` first, then `edits` as an array of {old_string, new_string}. " +
	"Do not send the whole file — this tool replaces snippets."

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
- Every change goes in ` + "`edits`" + `, an array of
  ` + "`{old_string, new_string}`" + ` — an array of one for a single change.
- Changing several places in one file: send them all in ONE call. Do not call
  this tool once per change.
- ` + "`old_string`" + ` must match the file exactly, including whitespace and
  quotes. Include a few surrounding lines so the match is unique.
- Every ` + "`old_string`" + ` is matched against the ORIGINAL file, not against
  the result of earlier entries, so entries must not overlap. If two changes
  touch the same lines, merge them into one entry.
- Keep each ` + "`old_string`" + ` as small as it can be while staying unique; do
  not pad with unchanged lines to bridge distant edits.
- Default: each snippet must occur exactly once. Set ` + "`replace_all`" + ` on an
  entry only when you intentionally want every occurrence of it changed.
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
` + fmt.Sprintf("- The file (and the result) must stay within %d bytes.", maxWriteSandboxBytes) + `

## Returns
- The path, how many replacements were made, and the new byte count.
  File contents are not echoed back.`,
	schema: utils.GenerateSchema[EditSandboxFileInput](),
}

// SandboxEdit is one targeted replacement.
type SandboxEdit struct {
	OldString  string `json:"old_string" jsonschema:"Exact text to find, unique in the file and not overlapping any other edit in this call."` //nolint:lll // one-line struct tag
	NewString  string `json:"new_string" jsonschema:"Replacement text. Use an empty string to delete the matched text."`
	ReplaceAll bool   `json:"replace_all,omitempty" jsonschema:"If true, replace every occurrence of this old_string instead of requiring it to be unique."` //nolint:lll // one-line struct tag
}

// sandboxEditList tolerates the shapes models actually emit for an array
// parameter: the array itself, a single object, or the array serialized as a
// JSON string. Rejecting those would spend a round on a formatting mistake the
// model cannot see, when the intent is unambiguous.
type sandboxEditList []SandboxEdit

func (l *sandboxEditList) UnmarshalJSON(data []byte) error {
	var asArray []SandboxEdit
	if err := json.Unmarshal(data, &asArray); err == nil {
		*l = asArray
		return nil
	}

	var single SandboxEdit
	if err := json.Unmarshal(data, &single); err == nil {
		*l = sandboxEditList{single}
		return nil
	}

	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		return l.UnmarshalJSON([]byte(asString))
	}

	return fmt.Errorf("edits must be an array of {old_string, new_string} objects")
}

// EditSandboxFileInput defines the input parameters for edit_sandbox_file.
//
// There is deliberately one shape. A flat old_string/new_string pair alongside
// edits[] would save a model a dozen bytes on a one-line fix and cost it a
// choice it can get wrong, plus a rule about which of the two replace_all
// attaches to. Every replacement goes in edits[], and a single-element array is
// the ordinary way to change one thing.
type EditSandboxFileInput struct {
	Path  string          `json:"path" jsonschema:"Absolute sandbox path of an existing text file under /workspace (not /workspace/input)."`                                                                                                                                                                                   //nolint:lll // one-line struct tag
	Edits sandboxEditList `json:"edits" jsonschema:"Replacements to apply, as an array even for a single change. Each old_string is matched against the original file, not against the result of earlier edits, so they must not overlap. Send every change to one file in one call rather than calling the tool repeatedly."` //nolint:lll // one-line struct tag
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

	// This tool is read-modify-write, so it has to hold the path against
	// concurrent siblings for the whole span, not just the write.
	defer lockSandboxFile(sessionID, clean)()

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

	updated, replacements, err := applySandboxEdits(string(raw), input.Edits)
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

	if hint := pythonScriptSyntaxHint(clean, updated, ToolEditSandboxFile); hint != "" {
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

// applySandboxEdits applies every edit against the ORIGINAL content and splices
// the results together.
//
// Matching against the original rather than against the running result is what
// makes a batch predictable: the model wrote all of the old_strings while
// looking at one version of the file, so that is the version they have to be
// resolved in. It also turns overlap into a detectable error instead of silent
// corruption, since two edits claiming the same bytes show up as intersecting
// ranges before anything is written.
func applySandboxEdits(content string, edits []SandboxEdit) (string, int, error) {
	if len(edits) == 0 {
		return "", 0, fmt.Errorf(
			"edits is required: an array of {old_string, new_string}, " +
				"with one entry even for a single change")
	}
	multi := len(edits) > 1

	type span struct {
		start, end int
		index      int
		newString  string
	}
	spans := make([]span, 0, len(edits))

	for i, e := range edits {
		if err := validateSandboxEdit(e, i, multi); err != nil {
			return "", 0, err
		}
		found := indexAllNonOverlapping(content, e.OldString)
		if len(found) == 0 {
			return "", 0, notFoundSandboxEditError(i, multi)
		}
		if len(found) > 1 && !e.ReplaceAll {
			return "", 0, ambiguousSandboxEditError(i, multi, len(found))
		}
		for _, start := range found {
			spans = append(spans, span{
				start:     start,
				end:       start + len(e.OldString),
				index:     i,
				newString: e.NewString,
			})
		}
	}

	sort.Slice(spans, func(a, b int) bool { return spans[a].start < spans[b].start })
	for i := 1; i < len(spans); i++ {
		if spans[i].start < spans[i-1].end {
			return "", 0, overlappingSandboxEditError(spans[i-1].index, spans[i].index)
		}
	}

	var b strings.Builder
	prev := 0
	for _, s := range spans {
		b.WriteString(content[prev:s.start])
		b.WriteString(s.newString)
		prev = s.end
	}
	b.WriteString(content[prev:])

	return b.String(), len(spans), nil
}

// indexAllNonOverlapping returns the start offset of every occurrence of sub,
// counting the same way strings.Count does: after a match, scanning resumes
// past it rather than one byte in.
func indexAllNonOverlapping(content, sub string) []int {
	var found []int
	for offset := 0; ; {
		i := strings.Index(content[offset:], sub)
		if i < 0 {
			return found
		}
		found = append(found, offset+i)
		offset += i + len(sub)
	}
}

func validateSandboxEdit(e SandboxEdit, index int, multi bool) error {
	if e.OldString == "" {
		if multi {
			return fmt.Errorf(
				"edits[%d].old_string is required; copy the exact text to change, including whitespace",
				index,
			)
		}
		return fmt.Errorf("old_string is required; copy the exact text to change, including whitespace")
	}
	if e.OldString == e.NewString {
		if multi {
			return fmt.Errorf("edits[%d].old_string and new_string are identical; no change would be made", index)
		}
		return fmt.Errorf("old_string and new_string are identical; no change would be made")
	}
	return nil
}

func notFoundSandboxEditError(index int, multi bool) error {
	if multi {
		return fmt.Errorf(
			"edits[%d].old_string was not found in the file. Copy the exact text (including whitespace) from the file",
			index)
	}
	return fmt.Errorf("old_string was not found in the file. Copy the exact text (including whitespace) from the file")
}

func ambiguousSandboxEditError(index int, multi bool, occurrences int) error {
	if multi {
		return fmt.Errorf(
			"edits[%d].old_string matched %d times. Include more surrounding context so it is unique, "+
				"or set replace_all on that entry",
			index, occurrences)
	}
	return fmt.Errorf(
		"old_string matched %d times. Include more surrounding context so it is unique, or set replace_all",
		occurrences)
}

func overlappingSandboxEditError(a, b int) error {
	return fmt.Errorf(
		"edits[%d] and edits[%d] cover overlapping text. Merge them into one edit that spans both changes",
		a, b)
}
