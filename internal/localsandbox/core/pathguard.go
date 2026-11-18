package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrPathDenied is returned for every rejected path. Callers surface it to
// the model as a scope error, so it deliberately carries no distinction
// between "outside the workspace" and "explicitly denied" — telling the
// model which secret paths exist is itself a small leak.
var ErrPathDenied = errors.New("localsandbox: path is outside the permitted workspace")

// PathGuard validates filesystem access performed by WeKnora itself, which
// runs with the user's full privileges and therefore cannot be constrained by
// the OS sandbox. It derives from the same Policy the backend compiles, so
// both paths enforce the same rules.
//
// Policy.PrivateRoots has no counterpart here on purpose: this guard is an
// allowlist (a read must land under a writable or readable root), so a path
// inside a private subtree that was not re-opened is already refused. The
// Seatbelt profile needs the explicit deny only because its base grants
// blanket file-read*.
type PathGuard struct {
	writable      []WritableRoot
	writableFiles []string
	readable      []string
	denyRead      []string
}

// NewPathGuard builds a PathGuard from p. The guard copies the policy's path
// lists so later mutations of p do not change enforcement.
func NewPathGuard(p Policy) *PathGuard {
	g := &PathGuard{
		writable:      append([]WritableRoot(nil), p.WritableRoots...),
		writableFiles: append([]string(nil), p.WritableFiles...),
		readable:      append([]string(nil), p.ReadableRoots...),
		denyRead:      append([]string(nil), p.DenyRead...),
	}
	return g
}

// CheckWrite reports whether writing path is permitted after following
// symlinks. It is a policy probe, not a safe I/O primitive: a parent can be
// replaced between this check and a later os.WriteFile. Trusted file tools
// must use WriteFile / ReadFile / MkdirAll / Lstat / CheckDir instead.
func (g *PathGuard) CheckWrite(path string) (string, error) {
	resolved, err := g.resolve(path)
	if err != nil {
		return "", err
	}
	if g.denied(resolved) {
		return "", fmt.Errorf("%w: %q", ErrPathDenied, path)
	}
	if g.readOnly(resolved) {
		return "", fmt.Errorf("%w: %q is read-only", ErrPathDenied, path)
	}
	if g.writableFile(resolved) {
		return resolved, nil
	}
	for _, root := range g.writable {
		if PathUnder(resolved, root.Path) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrPathDenied, path)
}

// readOnly checks every root, not only the one that admits path: a root that
// covers another must not reopen that root's carve-outs. Seatbelt compiles
// the same rules as final denies.
func (g *PathGuard) readOnly(path string) bool {
	for _, root := range g.writable {
		for _, ro := range root.ReadOnlySubpaths {
			if PathUnder(path, ro) {
				return true
			}
		}
		if (root.ProtectGitDirs || root.ProtectBareGitDirs) && PathUnder(path, root.Path) {
			// Count elements instead of slicing bytes: PathUnder folds case,
			// and folding can change a string's length.
			parts := strings.Split(filepath.ToSlash(path), "/")
			depth := len(strings.Split(filepath.ToSlash(root.Path), "/"))
			if root.ProtectGitDirs && hasGitComponent(strings.Join(parts[depth:], "/")) {
				return true
			}
			if root.ProtectBareGitDirs && isBareGitMetadata(parts[depth:]) {
				return true
			}
		}
	}
	return false
}

// writableFile reports whether path is one of the single files a grant opened.
func (g *PathGuard) writableFile(path string) bool {
	for _, file := range g.writableFiles {
		if samePath(path, file) {
			return true
		}
	}
	return false
}

// CheckRead returns the resolved absolute path when reading is permitted.
func (g *PathGuard) CheckRead(path string) (string, error) {
	resolved, err := g.resolve(path)
	if err != nil {
		return "", err
	}
	if g.denied(resolved) {
		return "", fmt.Errorf("%w: %q", ErrPathDenied, path)
	}
	if g.writableFile(resolved) {
		return resolved, nil
	}
	for _, root := range g.writable {
		if PathUnder(resolved, root.Path) {
			return resolved, nil
		}
	}
	for _, root := range g.readable {
		if PathUnder(resolved, root) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrPathDenied, path)
}

func (g *PathGuard) denied(resolved string) bool {
	for _, deny := range g.denyRead {
		if PathUnder(resolved, deny) {
			return true
		}
	}
	return false
}

// resolve cleans the path and resolves symlinks. The target may not exist yet
// (a file about to be created), so it resolves the longest existing prefix and
// re-joins the remainder. Resolving only the existing prefix is what stops a
// symlinked parent directory from widening the guard.
func (g *PathGuard) resolve(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: %q is not absolute", ErrPathDenied, path)
	}
	cur := filepath.Clean(path)
	remainder := ""
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			if remainder == "" {
				return resolved, nil
			}
			return filepath.Join(resolved, remainder), nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("%w: %q: %v", ErrPathDenied, path, err)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("%w: %q has no existing ancestor", ErrPathDenied, path)
		}
		remainder = filepath.Join(filepath.Base(cur), remainder)
		cur = parent
	}
}

// GrantWritePath decides one file-grant write. matches means target is the
// granted file under the same equality WriteFile uses, so the caller must
// check the approved digest and then write path.
//
// A resolved match writes the grant path. A failed resolve never substitutes
// that path; it matches only when target itself already equals the grant.
func (g *PathGuard) GrantWritePath(grantPath, target string) (path string, matches bool) {
	resolved, err := g.resolve(target)
	if err != nil {
		if samePath(target, grantPath) {
			return target, true
		}
		return target, false
	}
	if samePath(resolved, grantPath) {
		return grantPath, true
	}
	return target, false
}
