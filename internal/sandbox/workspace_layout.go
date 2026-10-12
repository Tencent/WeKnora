package sandbox

import (
	"path"
	"strings"
)

// WorkspaceOrigin says whether a layout is a disposable remote sandbox or a
// directory on the host. Isolation policy (work_dir clamping, inspect
// enforcement, prompt copy) follows Origin, never the Root string: a host
// project can itself be /workspace.
type WorkspaceOrigin int

const (
	// WorkspaceOriginUnspecified is the zero value. Tools treat it as remote.
	WorkspaceOriginUnspecified WorkspaceOrigin = iota
	// WorkspaceOriginRemote is a disposable container that owns /workspace.
	WorkspaceOriginRemote
	// WorkspaceOriginHost is a directory on the user's machine.
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

// FailedHostWorkspaceLayout is what tools and prompts use when a layout
// provider exists but errors: do not fall back to /workspace.
func FailedHostWorkspaceLayout() WorkspaceLayout {
	return WorkspaceLayout{Origin: WorkspaceOriginHost}
}

// HasRoot reports a usable workspace root.
func (l WorkspaceLayout) HasRoot() bool {
	return strings.TrimSpace(l.Root) != ""
}

// Normalized cleans every path in the layout and drops empty roots.
//
// Scope checks compare cleaned paths against these entries verbatim, so an
// adapter that hands back a trailing slash or an uncleaned path would
// silently deny every write and work_dir inside its own workspace. Consumers
// normalize on receipt rather than trusting the producer.
func (l WorkspaceLayout) Normalized() WorkspaceLayout {
	l.Root = cleanLayoutPath(l.Root)
	l.InputDir = cleanLayoutPath(l.InputDir)
	l.OutputDir = cleanLayoutPath(l.OutputDir)
	l.WriteRoots = cleanLayoutRoots(l.WriteRoots)
	l.ReadRoots = cleanLayoutRoots(l.ReadRoots)
	l.Hint = strings.TrimSpace(l.Hint)
	return l
}

func cleanLayoutPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	return path.Clean(p)
}

// cleanLayoutRoots preserves order (ReadRoots is most-specific-first) and
// drops duplicates that only differed before cleaning.
func cleanLayoutRoots(roots []string) []string {
	if len(roots) == 0 {
		return roots
	}
	out := make([]string, 0, len(roots))
	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		clean := cleanLayoutPath(root)
		if clean == "" {
			continue
		}
		if _, dup := seen[clean]; dup {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}

// PromptSafePath returns p when it can be embedded verbatim in prompt text,
// and "" when it cannot. On a host layout the workspace root is a directory
// the user chose, so a name carrying newlines or markup would otherwise reach
// the model as instructions rather than as a path. Callers fall back to
// generic wording instead of sanitizing, so a real path is never shown wrong.
func PromptSafePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f || r == '<' || r == '>' || r == '&' {
			return ""
		}
	}
	return p
}
