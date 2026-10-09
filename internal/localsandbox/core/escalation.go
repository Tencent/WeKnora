package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// systemRoots are never offered as grants: writing under them is something the
// user should do by hand, not approve for an agent from a card.
var systemRoots = []string{"/System", "/usr", "/bin", "/sbin", "/etc", "/private/etc", "/Library"}

// ProposeGrant returns the directory a denied command would need, or false
// when the denial cannot be tied to a path this policy actually withholds.
// The path parsed from output is only a hint; the PathGuard re-check is what
// makes the proposal. The proposal is always read-write: the user may narrow
// it to read-only on the card, never widen it.
func (b *PolicyBuilder) ProposeGrant(p Policy, d Denial, cwd string) (Grant, bool) {
	if !d.IsDenied() || d.Path == "" {
		return Grant{}, false
	}
	target := d.Path
	if !filepath.IsAbs(target) {
		if cwd == "" {
			return Grant{}, false
		}
		target = filepath.Join(cwd, target)
	}
	target = filepath.Clean(target)
	if _, err := NewPathGuard(p).CheckWrite(target); err == nil {
		return Grant{}, false
	}
	// The card and the stored grant use the resolved path. A symlink inside
	// the workspace must not be offered as if it were that workspace path.
	resolved, err := resolveExistingPrefix(target)
	if err != nil {
		return Grant{}, false
	}
	dir, ok := grantDirectory(resolved)
	if !ok || b.rejectGrantPath(p, resolved) != nil || b.rejectGrantPath(p, dir) != nil {
		return Grant{}, false
	}
	return Grant{Path: dir, Access: AccessWrite}, true
}

// rejectGrantPath is the check ProposeGrant and Relax share. path must already
// be absolute; callers resolve symlinks first so a later EvalSymlinks in the
// seatbelt backend cannot widen the grant.
func (b *PolicyBuilder) rejectGrantPath(p Policy, path string) error {
	if err := b.rejectBroadWorkspace(path); err != nil {
		return err
	}
	for _, deny := range p.DenyRead {
		denyPath := deny
		if resolved, err := resolveExistingPrefix(deny); err == nil {
			denyPath = resolved
		}
		if PathUnder(path, deny) || PathUnder(path, denyPath) {
			return fmt.Errorf("localsandbox: %q is read-denied and cannot be granted", path)
		}
	}
	for _, root := range systemRoots {
		if PathUnder(path, root) {
			return fmt.Errorf("%w: %q", ErrWorkspaceTooBroad, path)
		}
	}
	if err := b.rejectAppOwnedPath(path); err != nil {
		return err
	}
	if err := rejectGitMetadata(p, path); err != nil {
		return err
	}
	return nil
}

// rejectGitMetadata refuses a grant inside repository metadata: a .git
// directory anywhere, or a read-only subpath the policy already protects
// (such as the directory a .git file points to).
func rejectGitMetadata(p Policy, path string) error {
	if hasGitComponent(path) {
		return fmt.Errorf("localsandbox: %q is git metadata and cannot be granted", path)
	}
	for _, root := range p.WritableRoots {
		for _, ro := range root.ReadOnlySubpaths {
			roPath := ro
			if resolved, err := resolveExistingPrefix(ro); err == nil {
				roPath = resolved
			}
			if PathUnder(path, ro) || PathUnder(path, roPath) {
				return fmt.Errorf("localsandbox: %q is read-only and cannot be granted", path)
			}
		}
	}
	return nil
}

// rejectAppOwnedPath refuses the skills tree, the session tree, and the
// application directory that contains them. A grant of ~/.weknora, or of
// one skill or another session's workspace, would let this session rewrite
// files every session trusts.
func (b *PolicyBuilder) rejectAppOwnedPath(path string) error {
	for _, root := range b.appOwnedRoots() {
		resolved := root
		if candidate, err := resolveExistingPrefix(root); err == nil {
			resolved = candidate
		}
		if PathUnder(path, root) || PathUnder(path, resolved) || PathUnder(root, path) || PathUnder(resolved, path) {
			return fmt.Errorf("%w: %q belongs to the application", ErrWorkspaceTooBroad, path)
		}
	}
	return nil
}

func (b *PolicyBuilder) appOwnedRoots() []string {
	var roots []string
	seen := map[string]bool{}
	add := func(p string) {
		p = filepath.Clean(strings.TrimSpace(p))
		if p == "" || p == "." || !filepath.IsAbs(p) || isFilesystemRoot(p) || samePath(p, b.homeDir) || seen[p] {
			return
		}
		seen[p] = true
		roots = append(roots, p)
	}
	add(b.skillsRoot)
	add(b.sessionRoot)
	if b.skillsRoot != "" {
		add(filepath.Dir(filepath.Clean(b.skillsRoot)))
	}
	if b.sessionRoot != "" {
		add(filepath.Dir(filepath.Clean(b.sessionRoot)))
	}
	return roots
}

// resolveExistingPrefix EvalSymlinks the longest existing prefix and rejoins
// the remainder. The result is what the kernel will see.
func resolveExistingPrefix(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("localsandbox: %q is not absolute", path)
	}
	cur := filepath.Clean(path)
	remainder := ""
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			if remainder == "" {
				return resolved, nil
			}
			return filepath.Clean(filepath.Join(resolved, remainder)), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("localsandbox: %q has no existing ancestor", path)
		}
		if remainder == "" {
			remainder = filepath.Base(cur)
		} else {
			remainder = filepath.Join(filepath.Base(cur), remainder)
		}
		cur = parent
	}
}

// grantDirectory is the directory a card may offer. A directory target is
// itself. A file target is its parent, and that parent must already exist.
// Missing intermediate directories are not replaced by a higher ancestor
// such as ~/Documents.
func grantDirectory(p string) (string, bool) {
	info, err := os.Lstat(p)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		if resolved, rerr := filepath.EvalSymlinks(p); rerr == nil {
			p = resolved
			info, err = os.Lstat(p)
		}
	}
	if err == nil && info.IsDir() {
		return p, true
	}
	parent := filepath.Dir(p)
	if parent == p {
		return "", false
	}
	info, err = os.Lstat(parent)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		if resolved, rerr := filepath.EvalSymlinks(parent); rerr == nil {
			parent = resolved
			info, err = os.Lstat(parent)
		}
	}
	if err != nil || !info.IsDir() {
		return "", false
	}
	return parent, true
}
