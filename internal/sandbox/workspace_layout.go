package sandbox

import "github.com/Tencent/WeKnora/internal/sandbox/workspace"

// WorkspaceOrigin is where a workspace path is rooted. The type lives in the
// workspace subpackage; this alias keeps existing callers compiling.
type WorkspaceOrigin = workspace.Origin

// WorkspaceLayout is the set of roots a session may read and write. The type
// lives in the workspace subpackage; this alias keeps existing callers compiling.
type WorkspaceLayout = workspace.Layout

// Workspace origins re-exported for callers that do not import the subpackage.
const (
	WorkspaceOriginUnspecified  = workspace.OriginUnspecified
	WorkspaceOriginRemote       = workspace.OriginRemote
	WorkspaceOriginHost         = workspace.OriginHost
	WorkspaceOriginRemoteShared = workspace.OriginRemoteShared
)

// RemoteWorkspaceLayout is the /workspace layout of a session-owned machine.
func RemoteWorkspaceLayout() WorkspaceLayout { return workspace.Remote() }

// UserWorkspaceLayout is sessionID's layout on a machine its owner shares.
func UserWorkspaceLayout(sessionID string) (WorkspaceLayout, error) {
	return workspace.UserSession(sessionID)
}

// FailedHostWorkspaceLayout is what tools and prompts use when a layout
// provider exists but errors: do not fall back to /workspace.
func FailedHostWorkspaceLayout() WorkspaceLayout { return workspace.FailedHost() }

// PromptSafePath returns p when it can be embedded verbatim in prompt text.
func PromptSafePath(p string) string { return workspace.PromptSafePath(p) }
