package sandbox

import "strings"

// WorkspaceLayout describes the shape of one session's workspace. It is pure
// data with no behaviour and no dependencies, so both the remote managers and
// the local sandbox adapter can produce it and the tools can just consume it.
//
// Before this existed, the tools referenced /workspace constants directly,
// which made every path rule remote-only.
type WorkspaceLayout struct {
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

// IsHost reports a local OS workspace: the root is a real host path, not
// the remote /workspace contract.
func (l WorkspaceLayout) IsHost() bool {
	root := strings.TrimSpace(l.Root)
	return root != "" && root != SessionWorkspaceRoot
}

// RemoteWorkspaceLayout is the /workspace layout every remote provider shares.
func RemoteWorkspaceLayout() WorkspaceLayout {
	return WorkspaceLayout{
		Root:       SessionWorkspaceRoot,
		WriteRoots: []string{SessionWorkspaceRoot, SessionOutputRoot},
		ReadRoots:  []string{SessionOutputRoot, SessionInputRoot, SessionWorkspaceRoot},
		InputDir:   SessionInputRoot,
		OutputDir:  SessionOutputRoot,
		Hint:       SessionWorkspaceRoot,
	}
}
