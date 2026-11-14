// Package workspace describes where one session's files live inside a remote
// sandbox and the lexical path rules the manager and the agent tools share.
//
// It is a leaf package. internal/sandbox re-exports Layout as
// sandbox.WorkspaceLayout, so this package must never import it back.
package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
)

// Fixed paths inside every remote sandbox image.
const (
	Root            = "/workspace"
	InputDir        = "/workspace/input"
	OutputDir       = "/workspace/output"
	GitDir          = "/var/lib/weknora/workspace.git"
	SkillsImageRoot = "/opt/weknora/tenant/skills"
	TmpRoot         = "/tmp"

	// UserSessionsRoot holds one directory per session on a machine that
	// several sessions of one owner share; UserGitRoot holds their checkpoint
	// repositories, outside the work trees for the same reason GitDir is.
	UserSessionsRoot = Root + "/sessions"
	UserGitRoot      = "/var/lib/weknora/sessions"

	// RemoteBrowserRoot is the agent-browser state root on a machine one
	// session owns. Outside Root for the same reason GitDir is: Root is a
	// git work tree committed with add -A.
	RemoteBrowserRoot = "/var/lib/weknora/browser/session"
)

// Origin says what kind of workspace a layout describes. Isolation policy
// (work_dir clamping, inspect enforcement, prompt copy) follows Origin, never
// the Root string: a host project can itself be /workspace.
type Origin int

const (
	// OriginUnspecified is the zero value. Tools treat it as remote.
	OriginUnspecified Origin = iota
	// OriginRemote is a disposable container one session owns outright.
	OriginRemote
	// OriginHost is a directory on the user's machine.
	OriginHost
	// OriginRemoteShared is a remote machine shared by several sessions of
	// one owner. Tools must stay inside the layout's roots.
	OriginRemoteShared
)

// Layout describes the shape of one session's workspace. It is pure data, so
// remote managers and local adapters can both produce it and the tools can
// consume it for prompts and tool-layer scope.
//
// It is not a symlink-safe privilege boundary. A host SessionFileStore must
// still enforce the OS-sandbox PathGuard on every read and write. Empty
// InputDir / OutputDir is the host shape: there is no separate attachment or
// collect tree, and scanning Root would upload the user's project.
type Layout struct {
	// Origin selects remote vs host vs shared-remote policy.
	Origin Origin
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
	// GitDir is the checkpoint repository for Root. Empty when the backend
	// does not version the workspace (host).
	GitDir string
	// BrowserRoot holds the session's agent-browser home and profile. Never
	// under Root. Exported to the sandbox as WEKNORA_BROWSER_ROOT.
	BrowserRoot string
	// WorkDirRoots limits shell work_dir on a shared machine. Empty means
	// unrestricted for remote layouts; host layouts clamp to WriteRoots.
	WorkDirRoots []string
}

// IsHost reports a local OS workspace. Host adapters must set Origin; a
// non-empty Root that is not /workspace is not enough (Gitpod uses /workspace).
func (l Layout) IsHost() bool { return l.Origin == OriginHost }

// IsSharedRemote reports a remote machine shared by several sessions.
func (l Layout) IsSharedRemote() bool { return l.Origin == OriginRemoteShared }

// HasRoot reports a usable workspace root.
func (l Layout) HasRoot() bool { return strings.TrimSpace(l.Root) != "" }

// Normalized cleans every path in the layout and drops empty roots.
//
// Scope checks compare cleaned paths against these entries verbatim, so an
// adapter that hands back a trailing slash or an uncleaned path would silently
// deny every write and work_dir inside its own workspace.
func (l Layout) Normalized() Layout {
	l.Root = cleanLayoutPath(l.Root)
	l.InputDir = cleanLayoutPath(l.InputDir)
	l.OutputDir = cleanLayoutPath(l.OutputDir)
	l.GitDir = cleanLayoutPath(l.GitDir)
	l.BrowserRoot = cleanLayoutPath(l.BrowserRoot)
	l.WriteRoots = cleanLayoutRoots(l.WriteRoots)
	l.ReadRoots = cleanLayoutRoots(l.ReadRoots)
	l.WorkDirRoots = cleanLayoutRoots(l.WorkDirRoots)
	l.Hint = strings.TrimSpace(l.Hint)
	return l
}

// Remote is the /workspace layout of a machine one session owns.
func Remote() Layout {
	return Layout{
		Origin:      OriginRemote,
		Root:        Root,
		WriteRoots:  []string{Root, OutputDir},
		ReadRoots:   []string{OutputDir, InputDir, Root},
		InputDir:    InputDir,
		OutputDir:   OutputDir,
		Hint:        Root,
		GitDir:      GitDir,
		BrowserRoot: RemoteBrowserRoot,
	}
}

// UserSession is the layout of sessionID on a machine its owner's sessions
// share. Only canonical lower-case UUIDs are accepted, so the id can never
// carry a path segment of its own. The directory name is SessionDirName, not
// the full UUID: the model sees this path on every turn. The session root
// anchors commands and artifact collection; it is not a tool-layer access
// boundary — tools treat the machine like a session-owned one.
func UserSession(sessionID string) (Layout, error) {
	name, err := SessionDirName(sessionID)
	if err != nil {
		return Layout{}, err
	}
	root := path.Join(UserSessionsRoot, name)
	input := path.Join(root, "input")
	output := path.Join(root, "output")
	return Layout{
		Origin:      OriginRemoteShared,
		Root:        root,
		WriteRoots:  []string{Root, OutputDir},
		ReadRoots:   []string{output, input, root, SkillsImageRoot},
		InputDir:    input,
		OutputDir:   output,
		Hint:        root,
		GitDir:      path.Join(UserGitRoot, name+".git"),
		BrowserRoot: path.Join(UserGitRoot, sessionID+".browser"),
	}, nil
}

// SessionDirName is the on-disk directory for a canonical session UUID: the
// first 12 hex characters of the id's SHA-256. The hash uses every bit of the
// UUID, so a collision needs roughly a million sessions on one machine, and
// the name is deterministic: no mapping is stored anywhere. The full id stays
// the session's identity everywhere else.
func SessionDirName(sessionID string) (string, error) {
	if !IsCanonicalUUID(sessionID) {
		return "", fmt.Errorf("workspace: session id %q is not a canonical UUID", sessionID)
	}
	sum := sha256.Sum256([]byte(sessionID))
	return hex.EncodeToString(sum[:])[:12], nil
}

// FailedHost is what tools and prompts use when a layout provider exists but
// errors: do not fall back to /workspace.
func FailedHost() Layout { return Layout{Origin: OriginHost} }

// IsCanonicalUUID reports the 36-character lower-case form uuid.New().String()
// produces.
func IsCanonicalUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				return false
			}
		}
	}
	return true
}

// PromptSafePath returns p when it can be embedded verbatim in prompt text,
// and "" when it cannot. Callers fall back to generic wording instead of
// sanitizing, so a real path is never shown wrong.
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
