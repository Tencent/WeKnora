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

// ProposeFileGrant offers write access to one file the policy withholds. The
// target must be a regular file, or missing with an existing parent; a
// directory, a missing parent, or any hard-denied path is not offered.
func (b *PolicyBuilder) ProposeFileGrant(p Policy, path string) (Grant, bool) {
	if !filepath.IsAbs(path) {
		return Grant{}, false
	}
	path = filepath.Clean(path)
	if _, err := NewPathGuard(p).CheckWrite(path); err == nil {
		return Grant{}, false
	}
	resolved, err := resolveExistingPrefix(path)
	if err != nil {
		return Grant{}, false
	}
	info, err := os.Lstat(resolved)
	switch {
	case err == nil && !info.Mode().IsRegular():
		return Grant{}, false
	case err != nil && !os.IsNotExist(err):
		return Grant{}, false
	}
	parent, err := os.Stat(filepath.Dir(resolved))
	if err != nil || !parent.IsDir() {
		return Grant{}, false
	}
	if b.rejectGrantPath(p, resolved) != nil {
		return Grant{}, false
	}
	return Grant{Path: resolved, Access: AccessWrite, File: true}, true
}

// rejectGrantPath is the check ProposeGrant and Relax share. path must already
// be absolute; callers resolve symlinks first. Resolved-path refusals live on
// Policy so Prepare can apply them again after EvalSymlinks.
func (b *PolicyBuilder) rejectGrantPath(p Policy, path string) error {
	if err := b.rejectBroadWorkspace(path); err != nil {
		return err
	}
	// Deny-read entries may still be symlinks here. The seatbelt backend
	// resolves them before Validate; grant checks compare both forms.
	if len(p.DenyRead) > 0 {
		denies := append([]string(nil), p.DenyRead...)
		for _, deny := range p.DenyRead {
			resolved, err := resolveExistingPrefix(deny)
			if err == nil && !samePath(resolved, deny) {
				denies = append(denies, resolved)
			}
		}
		p.DenyRead = denies
	}
	if err := p.rejectResolvedGrant(path); err != nil {
		return err
	}
	if err := b.rejectHomeDotPath(path); err != nil {
		return err
	}
	if err := b.rejectProgramPath(path); err != nil {
		return err
	}
	return b.rejectAppOwnedPath(path)
}

// programRoots hold programs the user runs outside the sandbox. They are
// refused as grants only: a workspace the user chose may still live there, so
// this stays out of rejectResolvedGrant, which Validate applies to every root.
var programRoots = []string{"/opt", "/Applications"}

// homeProgramDirs are the non-hidden per-user program directories.
var homeProgramDirs = []string{"bin", "sbin", "go/bin", "Applications"}

// rejectProgramPath refuses anything that holds programs the user runs outside
// the sandbox, and any repo.git, whose hooks git runs. Writing to one is a
// persistent escape, the same threat as rejectHomeDotPath.
func (b *PolicyBuilder) rejectProgramPath(path string) error {
	if hasBareGitComponent(path) {
		return fmt.Errorf("localsandbox: %q is git metadata and cannot be granted", path)
	}
	for _, root := range programRoots {
		if PathUnder(path, root) {
			return fmt.Errorf("%w: %q holds programs", ErrWorkspaceTooBroad, path)
		}
	}
	dirs := b.ToolchainBins()
	if b.homeDir != "" && !isFilesystemRoot(filepath.Clean(b.homeDir)) {
		homes := []string{filepath.Clean(b.homeDir)}
		if resolved, err := resolveExistingPrefix(homes[0]); err == nil && resolved != homes[0] {
			homes = append(homes, resolved)
		}
		for _, home := range homes {
			for _, name := range homeProgramDirs {
				dirs = append(dirs, filepath.Join(home, filepath.FromSlash(name)))
			}
		}
	}
	for _, dir := range dirs {
		if PathUnder(path, dir) || PathUnder(dir, path) {
			return fmt.Errorf("%w: %q holds programs", ErrWorkspaceTooBroad, path)
		}
	}
	return nil
}

// nonHomePrivateRoots are the private-root entries that are not a home
// directory. ~/Library is checked against every other private root.
var nonHomePrivateRoots = []string{"/Users", "/Volumes", "/var", "/private/var"}

func isNonHomePrivateRoot(root string) bool {
	for _, prefix := range nonHomePrivateRoots {
		if samePath(root, prefix) {
			return true
		}
	}
	return false
}

// rejectResolvedGrant refuses an already-resolved absolute path that must not
// be compiled as a write allow: git metadata, a system path, a deny-read
// entry, a filesystem or well-known wide root, the home directory, or ~/Library.
func (p Policy) rejectResolvedGrant(path string) error {
	if err := rejectGitMetadata(p, path); err != nil {
		return err
	}
	for _, root := range systemRoots {
		if PathUnder(path, root) {
			return fmt.Errorf("%w: %q", ErrWorkspaceTooBroad, path)
		}
	}
	for _, deny := range p.DenyRead {
		if PathUnder(path, deny) {
			return fmt.Errorf("localsandbox: %q is read-denied and cannot be granted", path)
		}
	}
	if isFilesystemRoot(path) {
		return fmt.Errorf("%w: %q", ErrFilesystemRoot, path)
	}
	if isWellKnownWideRoot(path) {
		return fmt.Errorf("%w: %q", ErrWorkspaceTooBroad, path)
	}
	for _, private := range p.PrivateRoots {
		if isNonHomePrivateRoot(private) {
			continue
		}
		// private is the home the builder recorded. A grant that covers it,
		// including the home directory itself, re-opens the private root.
		if PathUnder(private, path) {
			return fmt.Errorf("%w: %q covers the home directory", ErrWorkspaceTooBroad, path)
		}
		if PathUnder(path, filepath.Join(private, "Library")) {
			return fmt.Errorf("%w: %q", ErrWorkspaceTooBroad, path)
		}
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

// rejectHomeDotPath refuses anything under a dot entry of the home directory.
// Toolchain bins (~/.local/bin, ~/.cargo/bin), shell and git config
// (~/.config/fish, ~/.config/git) and version managers (~/.nvm) live there,
// and the user runs what they hold outside the sandbox: a write grant on one
// of them is a persistent escape, and a read grant exposes tokens and state.
func (b *PolicyBuilder) rejectHomeDotPath(path string) error {
	if b.homeDir == "" || isFilesystemRoot(filepath.Clean(b.homeDir)) {
		return nil
	}
	homes := []string{filepath.Clean(b.homeDir)}
	if resolved, err := resolveExistingPrefix(homes[0]); err == nil && resolved != homes[0] {
		homes = append(homes, resolved)
	}
	parts := strings.Split(filepath.ToSlash(path), "/")
	for _, home := range homes {
		if !PathUnder(path, home) || samePath(path, home) {
			continue
		}
		depth := len(strings.Split(filepath.ToSlash(home), "/"))
		if depth < len(parts) && strings.HasPrefix(parts[depth], ".") {
			return fmt.Errorf("%w: %q is a hidden directory in home", ErrWorkspaceTooBroad, path)
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
