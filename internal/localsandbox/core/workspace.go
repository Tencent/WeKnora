package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
)

// WorkspaceKind distinguishes a user-picked project from an auto-allocated session dir.
type WorkspaceKind int

const (
	// WorkspaceProject is a directory the user picked. Sessions opened on the
	// same project share it.
	WorkspaceProject WorkspaceKind = iota
	// WorkspaceSession is auto-allocated per session under the session root.
	WorkspaceSession
)

// Workspace is where one session's work happens.
type Workspace struct {
	Kind WorkspaceKind
	// Root is the only work directory and the child process's cwd. Agent
	// edits land here; there is no separate input/ or output/ tree.
	Root string
	// ProtectGit is true only for project workspaces: a session workspace's
	// .git was created by the agent itself, so protecting it only gets in
	// the agent's way.
	ProtectGit bool
}

// ProjectLookup reports whether a session is bound to a user-selected project.
type ProjectLookup interface {
	ProjectDirForSession(ctx context.Context, sessionID string) (string, bool)
}

// DirLayout names the roots the resolver allocates under.
type DirLayout struct {
	// SessionRoot is where session workspaces are created, by default
	// ~/Documents/WeKnoraLite. It is user-visible on purpose.
	SessionRoot string
}

// WorkspaceResolver maps a session to the directory the agent may work in.
type WorkspaceResolver interface {
	Resolve(ctx context.Context, sessionID string) (Workspace, error)
}

type workspaceResolver struct {
	layout   DirLayout
	projects ProjectLookup
	now      func() time.Time
}

// NewWorkspaceResolver returns a resolver that prefers a bound project
// directory and otherwise allocates under layout.SessionRoot.
func NewWorkspaceResolver(layout DirLayout, projects ProjectLookup) WorkspaceResolver {
	return &workspaceResolver{layout: layout, projects: projects, now: time.Now}
}

func (r *workspaceResolver) Resolve(ctx context.Context, sessionID string) (Workspace, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		logger.Warnf(ctx, "[LocalSandbox] resolve workspace: session id is required")
		return Workspace{}, fmt.Errorf("localsandbox: session id is required")
	}

	if r.projects != nil {
		if dir, ok := r.projects.ProjectDirForSession(ctx, sessionID); ok && dir != "" {
			root, err := filepath.EvalSymlinks(filepath.Clean(dir))
			if err != nil {
				logger.Warnf(ctx, "[LocalSandbox] resolve project dir session=%s dir=%q: %v",
					sessionID, dir, err)
				return Workspace{}, fmt.Errorf("localsandbox: resolve project dir: %w", err)
			}
			logger.Infof(ctx, "[LocalSandbox] workspace session=%s kind=project root=%s", sessionID, root)
			return Workspace{
				Kind:       WorkspaceProject,
				Root:       root,
				ProtectGit: true,
			}, nil
		}
	}

	day := r.now().Format("2006-01-02")
	seg := sanitizeSegment(sessionID)
	if existing, ok := r.existingSessionDir(seg); ok {
		logger.Infof(ctx, "[LocalSandbox] workspace session=%s kind=session root=%s", sessionID, existing)
		return Workspace{
			Kind:       WorkspaceSession,
			Root:       existing,
			ProtectGit: false,
		}, nil
	}
	root := filepath.Join(r.layout.SessionRoot, day, "session-"+seg)
	if err := os.MkdirAll(root, 0o700); err != nil {
		logger.Errorf(ctx, "[LocalSandbox] create session workspace session=%s root=%s: %v",
			sessionID, root, err)
		return Workspace{}, fmt.Errorf("localsandbox: create session workspace: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return Workspace{}, fmt.Errorf("localsandbox: restrict session workspace: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		logger.Errorf(ctx, "[LocalSandbox] resolve session workspace session=%s root=%s: %v",
			sessionID, root, err)
		return Workspace{}, fmt.Errorf("localsandbox: resolve session workspace: %w", err)
	}
	logger.Infof(ctx, "[LocalSandbox] workspace session=%s kind=session root=%s", sessionID, resolvedRoot)
	return Workspace{
		Kind:       WorkspaceSession,
		Root:       resolvedRoot,
		ProtectGit: false,
	}, nil
}

// sanitizeSegment reduces an identifier to a short, space-free, filesystem-safe
// directory segment. Spaces are excluded by construction: created paths cross
// shell, sbpl and Windows command-line escaping on every call.
func sanitizeSegment(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	visible := b.String()
	if visible == "" {
		visible = "session"
	}
	sum := sha256.Sum256([]byte(id))
	return visible + "-" + hex.EncodeToString(sum[:4])
}

func (r *workspaceResolver) existingSessionDir(seg string) (string, bool) {
	if r.layout.SessionRoot == "" || seg == "" {
		return "", false
	}
	matches, err := filepath.Glob(filepath.Join(r.layout.SessionRoot, "*", "session-"+seg))
	if err != nil || len(matches) == 0 {
		return "", false
	}
	info, err := os.Lstat(matches[0])
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(matches[0])
	if err != nil {
		return "", false
	}
	return resolved, true
}
