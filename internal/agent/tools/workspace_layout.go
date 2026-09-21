package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// sessionBound is embedded by sandbox tools so Description() and schema copy
// can resolve the per-session layout. Host adapters refuse sessionID="".
type sessionBound struct {
	sessionID string
}

func (s *sessionBound) BindSession(id string) {
	if s == nil {
		return
	}
	s.sessionID = strings.TrimSpace(id)
}

const layoutRootToken = "\x00WEKNORA_LAYOUT_ROOT\x00"

// lookupWorkspaceLayout is the Execute-time lookup.
//
// No provider keeps the remote /workspace contract (Cube/E2B/Docker that do
// not advertise a layout). A provider that errors or returns an empty Root
// is fail-closed: callers must not resolve relative paths under /workspace.
func lookupWorkspaceLayout(
	ctx context.Context, sessionID string, dep any,
) (sandbox.WorkspaceLayout, error) {
	provider, ok := dep.(sandbox.SessionWorkspaceLayoutProvider)
	if !ok || provider == nil {
		return sandbox.RemoteWorkspaceLayout(), nil
	}
	layout, err := provider.SessionWorkspaceLayout(ctx, sessionID)
	if err != nil {
		return sandbox.WorkspaceLayout{}, err
	}
	if !layout.HasRoot() {
		return sandbox.WorkspaceLayout{}, fmt.Errorf("session workspace root is empty")
	}
	return layout, nil
}

// sessionWorkspaceLayout is for Description() / Parameters(). Unbound tools
// (empty sessionID) keep the remote copy so host adapters that refuse
// sessionID="" do not advertise /workspace after BindSession. A bound lookup
// that fails uses a host origin with no root so copy does not say /workspace.
func sessionWorkspaceLayout(ctx context.Context, sessionID string, dep any) sandbox.WorkspaceLayout {
	layout, err := lookupWorkspaceLayout(ctx, sessionID, dep)
	if err == nil {
		return layout
	}
	if strings.TrimSpace(sessionID) == "" {
		return sandbox.RemoteWorkspaceLayout()
	}
	return sandbox.FailedHostWorkspaceLayout()
}

func executeWorkspaceLayout(
	ctx context.Context, sessionID string, dep any,
) (sandbox.WorkspaceLayout, *types.ToolResult) {
	layout, err := lookupWorkspaceLayout(ctx, sessionID, dep)
	if err != nil {
		return sandbox.WorkspaceLayout{}, &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("session workspace is unavailable: %v", err),
		}
	}
	return layout, nil
}

// resolveIn gives file tools the same relative-path semantics as commands:
// a relative path is anchored at the workspace root.
func resolveIn(l sandbox.WorkspaceLayout, p string) string {
	return sandbox.ResolveWorkspacePathIn(l, p)
}

// writableRootIn returns the writable root containing clean, or ("", false).
//
// The path must sit under some writable root, must not BE one of those roots,
// and must not sit under the read-only attachment directory. When several
// roots match, the most specific one is reported.
func writableRootIn(l sandbox.WorkspaceLayout, clean string) (string, bool) {
	if l.InputDir != "" && isUnderRoot(clean, l.InputDir) {
		return "", false
	}
	best := ""
	for _, root := range l.WriteRoots {
		if clean == root {
			return "", false
		}
		if isUnderRoot(clean, root) && len(root) > len(best) {
			best = root
		}
	}
	if best == "" {
		return "", false
	}
	return best, true
}

// inspectableRootIn returns the first read root containing clean. ReadRoots is
// ordered most-specific-first, so the first hit is the narrowest.
func inspectableRootIn(l sandbox.WorkspaceLayout, clean string) (string, bool) {
	for _, root := range l.ReadRoots {
		if isUnderRoot(clean, root) {
			return root, true
		}
	}
	return "", false
}

// inspectRoot reports the metadata root for a read/list. Remote sandboxes
// label misses as "/" and still inspect (tmp, installed skills). Host
// layouts refuse anything outside ReadRoots.
func inspectRoot(l sandbox.WorkspaceLayout, clean string) (string, bool) {
	if root, ok := inspectableRootIn(l, clean); ok {
		return root, true
	}
	if l.IsHost() {
		return "", false
	}
	return "/", true
}

func writeScopeErrorIn(l sandbox.WorkspaceLayout, requested string) string {
	scope := layoutScopeName(l)
	if strings.TrimSpace(l.InputDir) == "" {
		return fmt.Sprintf(
			"this tool only writes files under %s (not the directory roots themselves). path %q is outside that scope; use shell_exec for other locations",
			scope, requested,
		)
	}
	return fmt.Sprintf(
		"this tool only writes files under %s (not under %s, and not the directory roots themselves). path %q is outside that scope; use shell_exec for other locations",
		scope, modelSafeLayoutPath(l.InputDir, "the attachment directory"), requested,
	)
}

func inspectScopeErrorIn(l sandbox.WorkspaceLayout, requested string) string {
	return fmt.Sprintf(
		"this tool only reads files under %s. path %q is outside that scope",
		layoutScopeName(l), requested,
	)
}

func layoutScopeName(l sandbox.WorkspaceLayout) string {
	if l.IsHost() && strings.TrimSpace(l.Root) != "" {
		return l.Root
	}
	if hint := strings.TrimSpace(l.Hint); hint != "" {
		return hint
	}
	if root := strings.TrimSpace(l.Root); root != "" {
		return root
	}
	return sandbox.RemoteWorkspaceLayout().Hint
}

func modelSafeLayoutPath(p, generic string) string {
	if p == "" {
		return generic
	}
	remote := sandbox.RemoteWorkspaceLayout()
	if p == remote.Root || p == remote.InputDir || p == remote.OutputDir ||
		strings.HasPrefix(p, remote.Root+"/") {
		return p
	}
	return generic
}

func layoutOutputDir(l sandbox.WorkspaceLayout) string {
	return strings.TrimSpace(l.OutputDir)
}

func layoutRootOrGeneric(l sandbox.WorkspaceLayout) string {
	if root := strings.TrimSpace(l.Root); root != "" {
		return root
	}
	return "the session workspace"
}

func jsonSafePath(p string) string {
	encoded, err := json.Marshal(p)
	if err != nil || len(encoded) < 2 {
		return p
	}
	return string(encoded[1 : len(encoded)-1])
}

// layoutDefaultListDir is what list_sandbox_files scans when the model omits
// path. Remote sessions keep the artifact output tree. Host sessions edit in
// place: omitted path lists Root even when a separate collect OutputDir exists.
func layoutDefaultListDir(l sandbox.WorkspaceLayout) string {
	if l.IsHost() {
		return strings.TrimSpace(l.Root)
	}
	if dir := layoutOutputDir(l); dir != "" {
		return dir
	}
	return strings.TrimSpace(l.Root)
}

// rewriteRemoteWorkspaceCopy replaces the remote /workspace constants in
// tool copy with the session layout's model-safe wording. Longer paths
// are substituted first so /workspace/input is not split into Hint+"/input".
func rewriteRemoteWorkspaceCopy(text string, l sandbox.WorkspaceLayout) string {
	hint := strings.TrimSpace(l.Hint)
	if hint == "" {
		hint = sandbox.RemoteWorkspaceLayout().Hint
	}
	input := modelSafeLayoutPath(l.InputDir, hint)
	output := modelSafeLayoutPath(l.OutputDir, hint)
	text = strings.ReplaceAll(text, sandbox.SessionInputRoot, input)
	text = strings.ReplaceAll(text, sandbox.SessionOutputRoot, output)
	return strings.ReplaceAll(text, sandbox.SessionWorkspaceRoot, hint)
}

func schemaForLayout(schema json.RawMessage, l sandbox.WorkspaceLayout) json.RawMessage {
	if len(schema) == 0 {
		return schema
	}
	s := string(schema)
	if l.IsHost() {
		root := jsonSafePath(l.Root)
		s = strings.ReplaceAll(s, sandbox.SessionInputRoot, "attachments")
		s = strings.ReplaceAll(s, sandbox.SessionOutputRoot, layoutRootToken)
		s = strings.ReplaceAll(s, sandbox.SessionWorkspaceRoot, layoutRootToken)
		s = strings.ReplaceAll(s, layoutRootToken, root)
		return json.RawMessage(s)
	}
	return json.RawMessage(rewriteRemoteWorkspaceCopy(s, l))
}
