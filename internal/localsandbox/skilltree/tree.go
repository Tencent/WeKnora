// Package skilltree manages Lite's on-disk skill install tree:
//
//	<root>/<name>                 symlink to the version that serves chats
//	<root>/.versions/<name>-<n>/  one directory per install, never moved
//	<root>/.locks/<name>.lock     per-skill advisory lock
//	<root>/.next-<name>           transient link renamed over <name>
//
// Versions never move because a Python venv records absolute paths.
package skilltree

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	versionsDir = ".versions"
	locksDir    = ".locks"
	nextPrefix  = ".next-"
)

// ErrInvalidName rejects a name that is not exactly one path segment.
var ErrInvalidName = errors.New("skilltree: invalid skill name")

// Tree is one skills root.
type Tree struct {
	root string
}

// New prepares root and its bookkeeping directories.
func New(root string) (*Tree, error) {
	clean := filepath.Clean(strings.TrimSpace(root))
	if clean == "" || !filepath.IsAbs(clean) || clean == string(filepath.Separator) {
		return nil, fmt.Errorf("skilltree: invalid root %q", root)
	}
	for _, dir := range []string{clean, filepath.Join(clean, versionsDir), filepath.Join(clean, locksDir)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("skilltree: prepare %s: %w", dir, err)
		}
	}
	return &Tree{root: clean}, nil
}

// Root is the directory chat sessions read skills from.
func (t *Tree) Root() string { return t.root }

// VersionsRoot holds every install's own directory.
func (t *Tree) VersionsRoot() string { return filepath.Join(t.root, versionsDir) }

func validName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return fmt.Errorf("%w %q", ErrInvalidName, name)
	}
	return nil
}

// LinkPath is where the serving symlink of name lives.
func (t *Tree) LinkPath(name string) (string, error) {
	if err := validName(name); err != nil {
		return "", err
	}
	return filepath.Join(t.root, name), nil
}

// versionNumber parses "<name>-<n>" and reports n.
func versionNumber(name, base string) (int, bool) {
	rest, ok := strings.CutPrefix(base, name+"-")
	if !ok || rest == "" {
		return 0, false
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil && n > 0
}

func (t *Tree) versionDirs(name string) ([]string, []int, error) {
	entries, err := os.ReadDir(t.VersionsRoot())
	if err != nil {
		return nil, nil, err
	}
	type version struct {
		dir string
		n   int
	}
	var found []version
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if n, ok := versionNumber(name, e.Name()); ok {
			found = append(found, version{dir: filepath.Join(t.VersionsRoot(), e.Name()), n: n})
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].n < found[j].n })
	dirs := make([]string, len(found))
	nums := make([]int, len(found))
	for i, v := range found {
		dirs[i], nums[i] = v.dir, v.n
	}
	return dirs, nums, nil
}

// NewVersion creates the next empty version directory of name.
func (t *Tree) NewVersion(name string) (string, error) {
	if err := validName(name); err != nil {
		return "", err
	}
	_, nums, err := t.versionDirs(name)
	if err != nil {
		return "", err
	}
	next := 1
	if len(nums) > 0 {
		next = nums[len(nums)-1] + 1
	}
	dir := filepath.Join(t.VersionsRoot(), fmt.Sprintf("%s-%d", name, next))
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// ownVersion checks dir is a direct child of VersionsRoot that belongs to name.
// An empty name accepts any skill's version.
func (t *Tree) ownVersion(name, dir string) error {
	clean := filepath.Clean(dir)
	if filepath.Dir(clean) != t.VersionsRoot() {
		return fmt.Errorf("skilltree: %q is not a version directory", dir)
	}
	if name != "" {
		if _, ok := versionNumber(name, filepath.Base(clean)); !ok {
			return fmt.Errorf("skilltree: %q is not a version of %q", dir, name)
		}
	}
	return nil
}

// Activate points name at versionDir with one rename.
func (t *Tree) Activate(name, versionDir string) (string, error) {
	link, err := t.LinkPath(name)
	if err != nil {
		return "", err
	}
	if err := t.ownVersion(name, versionDir); err != nil {
		return "", err
	}
	previous, err := os.Readlink(link)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	tmp := filepath.Join(t.root, nextPrefix+name)
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.Symlink(filepath.Clean(versionDir), tmp); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return previous, nil
}

// Discard deletes one version directory that never became active.
func (t *Tree) Discard(versionDir string) error {
	if err := t.ownVersion("", versionDir); err != nil {
		return err
	}
	return os.RemoveAll(versionDir)
}

// Prune deletes versions of name that are not listed in keep.
func (t *Tree) Prune(name string, keep ...string) error {
	if err := validName(name); err != nil {
		return err
	}
	dirs, _, err := t.versionDirs(name)
	if err != nil {
		return err
	}
	kept := make(map[string]bool, len(keep))
	for _, k := range keep {
		if k != "" {
			kept[filepath.Clean(k)] = true
		}
	}
	var errs []error
	for _, dir := range dirs {
		if kept[dir] {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Remove deletes the serving link and every version of name.
func (t *Tree) Remove(name string) error {
	link, err := t.LinkPath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(link); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return t.Prune(name)
}

// Installed reports whether name serves a SKILL.md.
func (t *Tree) Installed(name string) bool {
	link, err := t.LinkPath(name)
	if err != nil {
		return false
	}
	info, err := os.Stat(filepath.Join(link, "SKILL.md"))
	return err == nil && !info.IsDir()
}

// Sweep deletes leftovers of runs that died: stale .next- links and version
// directories that never became active (newer than the live one, or of a
// skill with no live link). Versions at or below the live one are Prune's to
// manage, so the previous version a rollback relies on survives. A skill whose
// lock another Lite process holds is mid-install there and is skipped.
func (t *Tree) Sweep() error {
	entries, err := os.ReadDir(t.root)
	if err != nil {
		return err
	}
	live := map[string]int{}
	var errs []error
	for _, e := range entries {
		if e.Type()&os.ModeSymlink == 0 {
			continue
		}
		p := filepath.Join(t.root, e.Name())
		if name, ok := strings.CutPrefix(e.Name(), nextPrefix); ok {
			errs = append(errs, t.sweepLocked(name, p))
			continue
		}
		if target, err := os.Readlink(p); err == nil {
			if n, ok := versionNumber(e.Name(), filepath.Base(filepath.Clean(target))); ok {
				live[e.Name()] = n
			}
		}
	}
	versions, err := os.ReadDir(t.VersionsRoot())
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, e := range versions {
		dir := filepath.Join(t.VersionsRoot(), e.Name())
		name, n, ok := splitVersion(e.Name())
		if !ok {
			if err := os.RemoveAll(dir); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		if cur, linked := live[name]; linked && n <= cur {
			continue
		}
		errs = append(errs, t.sweepLocked(name, dir))
	}
	return errors.Join(errs...)
}

// sweepLocked removes p under name's lock, and leaves it when another process
// holds that lock.
func (t *Tree) sweepLocked(name, p string) error {
	unlock, ok, err := t.tryLock(name)
	if err != nil || !ok {
		return err
	}
	defer unlock()
	return os.RemoveAll(p)
}

// splitVersion parses "<name>-<n>" without knowing name.
func splitVersion(base string) (string, int, bool) {
	i := strings.LastIndex(base, "-")
	if i <= 0 {
		return "", 0, false
	}
	name := base[:i]
	n, ok := versionNumber(name, base)
	return name, n, ok && validName(name) == nil
}
