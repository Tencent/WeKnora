package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// sessionBound is embedded by sandbox tools so Description() and schema copy
// can resolve the per-session layout. Host adapters refuse sessionID="".
type sessionBound struct {
	mu        sync.Mutex
	sessionID string
	layout    sandbox.WorkspaceLayout
	layoutFor string
	hasLayout bool
}

func (s *sessionBound) BindSession(id string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = strings.TrimSpace(id)
	s.hasLayout = false
}

// copyLayoutTimeout bounds the lookup behind Description() / Parameters().
// Those run while assembling a model request and have no caller context, so
// an adapter that blocks must not stall the turn.
const copyLayoutTimeout = 3 * time.Second

// describeLayout resolves the layout for Description() / Parameters().
//
// Unbound tools (empty sessionID) keep the remote copy so host adapters that
// refuse sessionID="" do not advertise /workspace after BindSession. A bound
// lookup that fails uses a host origin with no root so copy does not say
// /workspace, and is not cached: the next call retries.
//
// Successful lookups are cached because both methods are called repeatedly
// while building every model request. The registry is rebuilt per turn, so
// the cache cannot outlive the layout it describes; Execute never reads it
// and always performs the fail-closed lookup against the request context.
func (s *sessionBound) describeLayout(dep any) sandbox.WorkspaceLayout {
	if s == nil {
		return sandbox.RemoteWorkspaceLayout()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasLayout && s.layoutFor == s.sessionID {
		return s.layout
	}
	ctx, cancel := context.WithTimeout(context.Background(), copyLayoutTimeout)
	defer cancel()
	layout, err := lookupWorkspaceLayout(ctx, s.sessionID, dep)
	if err != nil {
		if s.sessionID == "" {
			return sandbox.RemoteWorkspaceLayout()
		}
		return sandbox.FailedHostWorkspaceLayout()
	}
	s.layout, s.layoutFor, s.hasLayout = layout, s.sessionID, true
	return layout
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
	// Scope checks compare against these roots verbatim, so normalize what
	// the adapter returned before any of them run.
	layout = layout.Normalized()
	if !layout.HasRoot() {
		return sandbox.WorkspaceLayout{}, fmt.Errorf("session workspace root is empty")
	}
	return layout, nil
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
			"this tool only writes files under %s (not the directory roots themselves). "+
				"path %q is outside that scope; use shell_exec for other locations",
			scope, requested,
		)
	}
	return fmt.Sprintf(
		"this tool only writes files under %s (not under %s, and not the directory roots themselves). "+
			"path %q is outside that scope; use shell_exec for other locations",
		scope, modelSafeLayoutPath(l.InputDir, "the attachment directory"), requested,
	)
}

func inspectScopeErrorIn(l sandbox.WorkspaceLayout, requested string) string {
	return fmt.Sprintf(
		"this tool only reads files under %s. path %q is outside that scope",
		layoutScopeName(l), requested,
	)
}

// layoutScopeName names the scope a refused path fell outside of. A host
// layout never borrows the remote wording: FailedHostWorkspaceLayout has no
// root, and saying "/workspace" there would send the model at a directory
// that does not exist on the user's machine.
func layoutScopeName(l sandbox.WorkspaceLayout) string {
	if l.IsHost() {
		return layoutRootOrGeneric(l)
	}
	if hint := strings.TrimSpace(l.Hint); hint != "" {
		return hint
	}
	if root := sandbox.PromptSafePath(l.Root); root != "" {
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

// layoutHintOrRemote is the wording remote copy anchors on. A layout without
// a hint keeps the /workspace contract the copy was written against.
func layoutHintOrRemote(l sandbox.WorkspaceLayout) string {
	if hint := strings.TrimSpace(l.Hint); hint != "" {
		return hint
	}
	return sandbox.RemoteWorkspaceLayout().Hint
}

// genericWorkspaceName stands in wherever a real root cannot be shown: the
// layout lookup failed, or the root is not safe to embed in prompt text.
const genericWorkspaceName = "the session workspace"

// layoutRootOrGeneric is the single place tool copy turns a layout into a
// workspace name, so the prompt-safety check cannot be forgotten at one of
// the call sites.
func layoutRootOrGeneric(l sandbox.WorkspaceLayout) string {
	if root := sandbox.PromptSafePath(l.Root); root != "" {
		return root
	}
	return genericWorkspaceName
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
	hint := layoutHintOrRemote(l)
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
		// layoutRootOrGeneric, not l.Root: a failed lookup has no root, and
		// substituting "" leaves holes like "Commands already start in ;".
		root := jsonSafePath(layoutRootOrGeneric(l))
		s = strings.ReplaceAll(s, sandbox.SessionInputRoot, "attachments")
		s = strings.ReplaceAll(s, sandbox.SessionOutputRoot, layoutRootToken)
		s = strings.ReplaceAll(s, sandbox.SessionWorkspaceRoot, layoutRootToken)
		s = strings.ReplaceAll(s, layoutRootToken, root)
		return json.RawMessage(s)
	}
	return json.RawMessage(rewriteRemoteWorkspaceCopy(s, l))
}
