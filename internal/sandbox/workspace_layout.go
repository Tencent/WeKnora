package sandbox

import "github.com/Tencent/WeKnora/internal/sandbox/workspace"

// WorkspaceOrigin and WorkspaceLayout live in the workspace subpackage; these
// aliases keep every existing caller compiling unchanged.
type (
	WorkspaceOrigin = workspace.Origin
	WorkspaceLayout = workspace.Layout
)

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
