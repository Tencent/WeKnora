package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	writable []WritableRoot
	readable []string
	denyRead []string
}

// NewPathGuard builds a PathGuard from p. The guard copies the policy's path
// lists so later mutations of p do not change enforcement.
func NewPathGuard(p Policy) *PathGuard {
	g := &PathGuard{
		writable: append([]WritableRoot(nil), p.WritableRoots...),
		readable: append([]string(nil), p.ReadableRoots...),
		denyRead: append([]string(nil), p.DenyRead...),
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
	for _, root := range g.writable {
		if !PathUnder(resolved, root.Path) {
			continue
		}
		for _, ro := range root.ReadOnlySubpaths {
			if PathUnder(resolved, ro) {
				return "", fmt.Errorf("%w: %q is read-only", ErrPathDenied, path)
			}
		}
		return resolved, nil
	}
	return "", fmt.Errorf("%w: %q", ErrPathDenied, path)
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
