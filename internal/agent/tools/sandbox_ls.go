// Package tools — list_sandbox_files.
//
// Read-only tool that lets the LLM enumerate files under a session's
// inspectable sandbox directories. Without this tool, the LLM cannot
// see files produced by prior skill invocations or staged chat
// attachments and has to guess paths when chaining work together.
//
// Design notes:
//   - Session-scoped: the sandbox path is resolved from the tool exec
//     context (`ToolExecContext.SessionID`). The LLM cannot pass an
//     arbitrary session ID.
//   - Directory guardrail: `path` must resolve underneath `/workspace`,
//     the session's own tree — the same scope write_sandbox_file may
//     create in. Omitting `path` lists the artifact output dir
//     (`$WEKNORA_SKILL_OUTPUT_DIR`, default `/workspace/output`) so a
//     listing does not dump every attachment and scratch file into context.
//   - Read-only: this tool never creates, modifies or deletes anything
//     inside the sandbox. Model-authored files go through write_sandbox_file.
//   - Graceful "no sandbox": if the session has never spawned a sandbox
//     yet (chat-only turn, or sandbox was reaped), the tool returns an
//     empty listing with a helpful message rather than an error, so the
//     LLM can decide to invoke a skill first.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// SandboxFileSource is the narrow, tool-facing subset of a session-aware
// sandbox manager. In production it is satisfied by
// *sandbox.SessionBoundManager; tests can stub it with an in-memory fake.
//
// Keeping the interface local to the tools package avoids leaking a
// dependency on internal/application/service (which is a higher layer)
// and mirrors the pattern used by ArtifactCollector.SandboxArtifactSource.
type SandboxFileSource interface {
	ListSessionFiles(ctx context.Context, sessionID, dir string) ([]sandbox.RemoteDirEntry, error)
	StatSessionFile(ctx context.Context, sessionID, path string) (*sandbox.RemoteStatEntry, error)
	ReadSessionFile(ctx context.Context, sessionID, path string) ([]byte, error)
}

// defaultListSandboxMaxEntries caps a single list_sandbox_files call at
// this many entries so a runaway directory can't blow up the LLM context.
// Aligns with the "sane pagination" advice in the Anthropic tool-use guide.
const (
	defaultListSandboxMaxEntries = 200
	maxListSandboxMaxEntries     = 500
)

// Tool schema

var listSandboxFilesTool = BaseTool{
	name:        ToolListSandboxFiles,
	description: `List files under /workspace when no shell executor is available. Omitted path lists the artifact output directory. Use known paths directly with read_file; list only to discover unknown files. Results are bounded by max_entries. An unprovisioned session returns an empty listing.`,
	schema:      utils.GenerateSchema[ListSandboxFilesInput](),
}

// ListSandboxFilesInput defines the input parameters for list_sandbox_files.
type ListSandboxFilesInput struct {
	// Path is the absolute path inside the sandbox to list. When empty
	// the tool falls back to skills.ArtifactOutputDir(). Must sit under
	// /workspace.
	Path string `json:"path,omitempty" jsonschema:"Optional absolute sandbox path to list, under /workspace. Defaults to the session's artifact output directory."` //nolint:lll // one-line struct tag
	// MaxEntries caps the listing size to protect the LLM context.
	// Zero uses defaultListSandboxMaxEntries.
	MaxEntries int `json:"max_entries,omitempty" jsonschema:"Optional cap on the number of entries returned. Defaults to 200, hard-capped at 500. Use a smaller value when you only need to check whether a specific file exists."`
}

// ListSandboxFilesTool exposes SandboxFileSource.ListSessionFiles to the
// agent as a read-only enumeration primitive.
type ListSandboxFilesTool struct {
	BaseTool
	source SandboxFileSource
}

// NewListSandboxFilesTool constructs the tool. `source` MUST NOT be nil:
// callers should feature-gate registration in the agent bootstrap when
// the sandbox backend does not support per-session file inspection.
func NewListSandboxFilesTool(source SandboxFileSource) *ListSandboxFilesTool {
	return &ListSandboxFilesTool{
		BaseTool: listSandboxFilesTool,
		source:   source,
	}
}

// Execute enumerates files under the requested path inside the current
// session's sandbox.
func (t *ListSandboxFilesTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][ListSandboxFiles] Execute started")

	var input ListSandboxFilesInput
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

	// Resolve session ID from tool exec context (preferred) or the
	// ambient context helper (fallback for direct unit tests).
	sessionID := resolveSessionID(ctx)
	if sessionID == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "no session ID in context; list_sandbox_files must run inside an agent turn",
		}, nil
	}

	// Resolve target directory. When the caller omits path we scan the
	// same directory ArtifactCollector drains. An explicit path may also
	// sit under /workspace/input so staged chat attachments are listable.
	targetDir := strings.TrimSpace(input.Path)
	if targetDir == "" {
		targetDir = skills.ArtifactOutputDir()
	} else {
		targetDir = sandbox.ResolveWorkspacePath(targetDir)
	}
	rootDir, ok := matchingInspectableRoot(targetDir)
	if !ok {
		return &types.ToolResult{
			Success: false,
			Error:   inspectablePathError(input.Path),
		}, nil
	}

	maxEntries := input.MaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultListSandboxMaxEntries
	}
	if maxEntries > maxListSandboxMaxEntries {
		maxEntries = maxListSandboxMaxEntries
	}

	entries, err := t.source.ListSessionFiles(ctx, sessionID, targetDir)
	if err != nil {
		logger.Warnf(ctx, "[Tool][ListSandboxFiles] list failed: session=%s dir=%s err=%v",
			sessionID, targetDir, err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to list %s: %v", targetDir, err),
		}, nil
	}

	// Deterministic ordering by path so multiple calls return the same
	// pagination window even when the underlying backend does not
	// guarantee ordering.
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})

	truncated := false
	if len(entries) > maxEntries {
		entries = entries[:maxEntries]
		truncated = true
	}

	// Build human-readable output for the LLM. Machine-consumable data
	// goes into ToolResult.Data.
	var b strings.Builder
	b.WriteString(fmt.Sprintf("=== Sandbox listing: %s ===\n\n", targetDir))
	if len(entries) == 0 {
		b.WriteString("No files found under this path. Either nothing has been written here yet, or the sandbox has been reaped.\n")
	} else {
		b.WriteString(fmt.Sprintf("Found %d file(s)", len(entries)))
		if truncated {
			b.WriteString(fmt.Sprintf(" (truncated to %d; increase max_entries to see more)", maxEntries))
		}
		b.WriteString(":\n\n")
		for _, e := range entries {
			b.WriteString(fmt.Sprintf("- %s (size=%d, modified=%s)\n",
				e.Path, e.Size, formatSandboxModTime(e.ModTime)))
		}
	}

	// Serialise entries for structured consumption.
	items := make([]map[string]interface{}, 0, len(entries))
	for _, e := range entries {
		items = append(items, map[string]interface{}{
			"name":        e.Name,
			"path":        e.Path,
			"size":        e.Size,
			"modified_at": formatSandboxModTime(e.ModTime),
		})
	}

	logger.Infof(ctx, "[Tool][ListSandboxFiles] session=%s dir=%s count=%d truncated=%v",
		sessionID, targetDir, len(items), truncated)

	return &types.ToolResult{
		Success: true,
		Output:  b.String(),
		Data: map[string]interface{}{
			"session_id": sessionID,
			"path":       targetDir,
			"root":       rootDir,
			"entries":    items,
			"count":      len(items),
			"truncated":  truncated,
		},
	}, nil
}

// Cleanup releases any resources.
func (t *ListSandboxFilesTool) Cleanup(ctx context.Context) error {
	return nil
}

// formatSandboxModTime renders a mod time in the RFC3339 shape the tool has
// historically emitted. Zero times render as the empty string so LLM output
// stays visually clean.
func formatSandboxModTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// resolveSessionID pulls the session ID out of the tool exec context (set
// by the agent engine per tool call) with a fallback to the ambient
// context helper used elsewhere in WeKnora.
func resolveSessionID(ctx context.Context) string {
	if meta, ok := ToolExecFromContext(ctx); ok && meta != nil && meta.SessionID != "" {
		return meta.SessionID
	}
	if sid, ok := types.SessionIDFromContext(ctx); ok {
		return sid
	}
	return ""
}

// sandboxInspectableRoots is the allowlist for list_sandbox_files and
// read_file: the session workspace, and nothing outside it.
//
// It matches what write_sandbox_file may create. Narrowing the readers to
// artifacts and attachments used to leave the agent unable to read back the
// scratch script it had just written to /workspace, which bought no safety —
// shell_exec reaches the same files — and only forced a detour through `cat`.
//
// The list is ordered most specific first so the reported root still names the
// artifact or attachment tree when the path is inside one.
func sandboxInspectableRoots() []string {
	return []string{
		skills.ArtifactOutputDir(),
		sandbox.SessionInputRoot,
		sandbox.SessionWorkspaceRoot,
	}
}

func inspectableRootsDescription() string {
	return sandbox.SessionWorkspaceRoot
}

// inspectablePathError explains a refused list/read path. Skill image
// paths are the common miss: the model sees /opt/weknora/tenant/skills/<name>
// in read_file's environment section and retries with this tool or ls.
func inspectablePathError(requested string) string {
	base := fmt.Sprintf("this tool only lists/reads /workspace. path %q is outside that scope", requested)
	name, inImage := sandbox.SkillNameFromImagePath(path.Clean(requested))
	if inImage && name != "" {
		return base + fmt.Sprintf(". Use read_file(path=%q) for package instructions and file discovery. Do not ls the whole installed dependency tree.", "skill://"+name+"/SKILL.md")
	}
	return base + ". Use read_file with a listed skill:// resource for skill packages."
}

func relativeSkillFileFromImagePath(clean, skillName string) string {
	dir, err := sandbox.SkillDirFor(skillName)
	if err != nil || clean == dir {
		return ""
	}
	prefix := dir + "/"
	if strings.HasPrefix(clean, prefix) {
		return strings.TrimPrefix(clean, prefix)
	}
	return ""
}

// matchingInspectableRoot returns the allowlisted root that contains
// clean, or ("", false) when the path sits outside every root.
func matchingInspectableRoot(clean string) (string, bool) {
	for _, root := range sandboxInspectableRoots() {
		if isUnderRoot(clean, root) {
			return root, true
		}
	}
	return "", false
}

// isUnderRoot reports whether clean sits at or underneath root. Both
// arguments must already be cleaned. The list/read tools use this as a
// scope check so they stay on artifacts and attachments; it is not a
// privilege boundary (shell_exec can already reach the same files).
func isUnderRoot(clean, root string) bool {
	if clean == root {
		return true
	}
	rootWithSep := root
	if !strings.HasSuffix(rootWithSep, "/") {
		rootWithSep += "/"
	}
	return strings.HasPrefix(clean, rootWithSep)
}
