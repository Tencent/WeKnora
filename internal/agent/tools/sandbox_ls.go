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
//   - Paths are inside the current session's sandbox. Relative paths resolve
//     from the workspace root. Omitting `path` lists the layout default
//     (remote: `$WEKNORA_SKILL_OUTPUT_DIR` / `/workspace/output`; host: the
//     workspace root).
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
	"sort"
	"strings"
	"time"

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
	name: ToolListSandboxFiles,
	description: `List files inside the current session's sandbox when no shell executor is available.
Relative paths resolve from /workspace; omitted path lists /workspace/output.
Use known paths directly with read_file; list only to discover unknown files.
Results are bounded by max_entries. An unprovisioned session returns an empty listing.`,
	schema: utils.GenerateSchema[ListSandboxFilesInput](),
}

// ListSandboxFilesInput defines the input parameters for list_sandbox_files.
type ListSandboxFilesInput struct {
	// Path is the absolute path inside the sandbox to list. When empty
	// the tool falls back to layoutDefaultListDir. Relative paths resolve
	// from the workspace root.
	Path string `json:"path,omitempty" jsonschema:"Optional absolute or /workspace-relative sandbox path to list. Defaults to /workspace/output on remote sandboxes, or the working directory on host."` //nolint:lll // one-line struct tag
	// MaxEntries caps the listing size to protect the LLM context.
	// Zero uses defaultListSandboxMaxEntries.
	MaxEntries int `json:"max_entries,omitempty" jsonschema:"Optional cap on the number of entries returned. Defaults to 200, hard-capped at 500. Use a smaller value when you only need to check whether a specific file exists."`
}

// ListSandboxFilesTool exposes SandboxFileSource.ListSessionFiles to the
// agent as a read-only enumeration primitive.
type ListSandboxFilesTool struct {
	BaseTool
	sessionBound
	source SandboxFileSource
}

// NewListSandboxFilesTool constructs the tool. `source` MUST NOT be nil:
// callers should feature-gate registration in the agent bootstrap when
// the sandbox backend does not support per-session file inspection.
func NewListSandboxFilesTool(source SandboxFileSource) *ListSandboxFilesTool {
	return &ListSandboxFilesTool{
		BaseTool: BaseTool{
			name:   listSandboxFilesTool.name,
			schema: listSandboxFilesTool.schema,
		},
		source: source,
	}
}

// Description is built from the session sandbox layout so host paths never
// hard-code /workspace.
func (t *ListSandboxFilesTool) Description() string {
	layout := t.boundLayout()
	if layout.IsHost() {
		return fmt.Sprintf(hostListSandboxFilesDescription, layoutRootOrGeneric(layout))
	}
	return rewriteRemoteWorkspaceCopy(listSandboxFilesTool.description, layout)
}

// Parameters rewrites workspace paths in the schema to match the session layout.
func (t *ListSandboxFilesTool) Parameters() json.RawMessage {
	return schemaForLayout(listSandboxFilesTool.schema, t.boundLayout())
}

func (t *ListSandboxFilesTool) boundLayout() sandbox.WorkspaceLayout {
	if t == nil {
		return sandbox.RemoteWorkspaceLayout()
	}
	return t.describeLayout(t.source)
}

const hostListSandboxFilesDescription = "List files in %s. Relative paths resolve from that folder; " +
	"omitted path lists it.\n" +
	"Use known paths directly with read_file; list only to discover unknown files.\n" +
	"Results are bounded by max_entries."

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

	layout, layoutErr := executeWorkspaceLayout(ctx, sessionID, t.source)
	if layoutErr != nil {
		return layoutErr, nil
	}

	// Resolve target directory. When the caller omits path we scan the
	// layout's default listing (remote: artifact output; host: workspace).
	targetDir := strings.TrimSpace(input.Path)
	if targetDir == "" {
		targetDir = layoutDefaultListDir(layout)
	} else {
		targetDir = resolveIn(layout, targetDir)
	}
	rootDir, ok := inspectRoot(layout, targetDir)
	if !ok {
		// Report the resolved directory, not input.Path: an omitted path
		// would otherwise be refused as `path ""`.
		return &types.ToolResult{
			Success: false,
			Error:   inspectScopeErrorIn(layout, targetDir),
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

// isUnderRoot reports whether clean sits at or underneath root. Both
// arguments must already be cleaned. On a remote sandbox this labels roots
// and protects the attachment tree; shell_exec can still reach the same
// files. On a host layout, write scope and work_dir clamping use this as
// the tool-layer boundary — the OS sandbox PathGuard remains the real
// privilege check, including symlink follow.
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
