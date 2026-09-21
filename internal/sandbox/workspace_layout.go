package sandbox

import "strings"

// WorkspaceOrigin says whether a layout is a disposable remote sandbox or a
// directory on the host. Isolation policy (work_dir clamping, inspect
// enforcement, prompt copy) follows Origin, never the Root string: a host
// project can itself be /workspace.
type WorkspaceOrigin int

const (
	// WorkspaceOriginUnspecified is the zero value. Tools treat it as remote.
	WorkspaceOriginUnspecified WorkspaceOrigin = iota
	WorkspaceOriginRemote
	WorkspaceOriginHost
)

// WorkspaceLayout describes the shape of one session's workspace. It is pure
// data with no behaviour, so both the remote managers and a local adapter can
// produce it and the tools can consume it for prompts and tool-layer scope.
//
// It is not a symlink-safe privilege boundary. A host SessionFileStore must
// still enforce the OS-sandbox PathGuard (EvalSymlinks, ProtectGit, deny-read)
// on every read and write. Empty InputDir / OutputDir is the host shape:
// there is no separate attachment or collect tree, and scanning Root would
// upload the user's project.
type WorkspaceLayout struct {
	// Origin selects remote vs host policy. RemoteWorkspaceLayout sets
	// WorkspaceOriginRemote; host adapters must set WorkspaceOriginHost.
	Origin WorkspaceOrigin
	// Root anchors relative paths and is the shell's working directory.
	Root string
	// WriteRoots are the directories write/edit tools may create files under.
	// A root itself is a directory, not a writable file path.
	WriteRoots []string
	// ReadRoots are the directories read/list tools may inspect, ordered
	// most-specific-first so the reported root names the narrowest match.
	ReadRoots []string
	// InputDir holds read-only user attachments. Empty when the backend has none.
	InputDir string
	// OutputDir is what artifact collection sweeps. Empty when it collects none.
	OutputDir string
	// Hint is the wording tools put in descriptions and scope errors.
	Hint string
}

// IsHost reports a local OS workspace. Host adapters must set Origin; a
// non-empty Root that is not /workspace is not enough (Gitpod uses /workspace).
func (l WorkspaceLayout) IsHost() bool {
	return l.Origin == WorkspaceOriginHost
}

// RemoteWorkspaceLayout is the /workspace layout every remote provider shares.
func RemoteWorkspaceLayout() WorkspaceLayout {
	return WorkspaceLayout{
		Origin:     WorkspaceOriginRemote,
		Root:       SessionWorkspaceRoot,
		WriteRoots: []string{SessionWorkspaceRoot, SessionOutputRoot},
		ReadRoots:  []string{SessionOutputRoot, SessionInputRoot, SessionWorkspaceRoot},
		InputDir:   SessionInputRoot,
		OutputDir:  SessionOutputRoot,
		Hint:       SessionWorkspaceRoot,
	}
}

// failedHostWorkspaceLayout is what tools and prompts use when a layout
// provider exists but errors: do not fall back to /workspace.
func FailedHostWorkspaceLayout() WorkspaceLayout {
	return WorkspaceLayout{Origin: WorkspaceOriginHost}
}

// HasRoot reports a usable workspace root.
func (l WorkspaceLayout) HasRoot() bool {
	return strings.TrimSpace(l.Root) != ""
}
