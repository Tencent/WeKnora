package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/sandbox"
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

// sessionWorkspaceLayout is the single Execute-time lookup: if the session
// sandbox advertises a layout, use it; otherwise use the remote /workspace
// contract. Tools do not store a layout of their own.
func sessionWorkspaceLayout(ctx context.Context, sessionID string, dep any) sandbox.WorkspaceLayout {
	if provider, ok := dep.(sandbox.SessionWorkspaceLayoutProvider); ok && provider != nil {
		layout, err := provider.SessionWorkspaceLayout(ctx, sessionID)
		if err == nil && strings.TrimSpace(layout.Root) != "" {
			return layout
		}
	}
	return sandbox.RemoteWorkspaceLayout()
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

// layoutDefaultListDir is what list_sandbox_files scans when the model omits
// path. Remote sessions keep the artifact output tree; host sessions have no
// such tree, so the workspace root is the listing.
func layoutDefaultListDir(l sandbox.WorkspaceLayout) string {
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
		s = strings.ReplaceAll(s, sandbox.SessionInputRoot, "attachments")
		s = strings.ReplaceAll(s, sandbox.SessionOutputRoot, l.Root)
		s = strings.ReplaceAll(s, sandbox.SessionWorkspaceRoot, l.Root)
		return json.RawMessage(s)
	}
	return json.RawMessage(rewriteRemoteWorkspaceCopy(s, l))
}
