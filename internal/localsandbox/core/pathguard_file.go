package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func cleanAbs(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: %q is not absolute", ErrPathDenied, path)
	}
	return filepath.Clean(path), nil
}

func relativeInside(root, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	rel = filepath.Clean(rel)
	if rel == "." {
		return ".", nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrPathDenied
	}
	return rel, nil
}

func relParts(rel string) []string {
	rel = filepath.Clean(rel)
	if rel == "." || rel == "" {
		return nil
	}
	return strings.Split(rel, string(filepath.Separator))
}

// allowWrite maps path onto one writable root using the lexical path. The
// file I/O helpers then walk that relative path with O_NOFOLLOW so a swapped
// parent cannot smuggle the write outside the workspace.
func (g *PathGuard) allowWrite(path string) (root, rel string, err error) {
	path, err = cleanAbs(path)
	if err != nil {
		return "", "", err
	}
	if g.denied(path) {
		return "", "", fmt.Errorf("%w: %q", ErrPathDenied, path)
	}
	for _, wr := range g.writable {
		if !PathUnder(path, wr.Path) {
			continue
		}
		for _, ro := range wr.ReadOnlySubpaths {
			if PathUnder(path, ro) {
				return "", "", fmt.Errorf("%w: %q is read-only", ErrPathDenied, path)
			}
		}
		rel, err = relativeInside(wr.Path, path)
		if err != nil {
			continue
		}
		return wr.Path, rel, nil
	}
	return "", "", fmt.Errorf("%w: %q", ErrPathDenied, path)
}

func (g *PathGuard) allowRead(path string) (root, rel string, err error) {
	path, err = cleanAbs(path)
	if err != nil {
		return "", "", err
	}
	if g.denied(path) {
		return "", "", fmt.Errorf("%w: %q", ErrPathDenied, path)
	}
	for _, wr := range g.writable {
		if !PathUnder(path, wr.Path) {
			continue
		}
		rel, err = relativeInside(wr.Path, path)
		if err != nil {
			continue
		}
		return wr.Path, rel, nil
	}
	for _, readable := range g.readable {
		if !PathUnder(path, readable) {
			continue
		}
		rel, err = relativeInside(readable, path)
		if err != nil {
			continue
		}
		return readable, rel, nil
	}
	return "", "", fmt.Errorf("%w: %q", ErrPathDenied, path)
}

// WriteFile creates or replaces path after walking from a writable root
// without following symlinks.
func (g *PathGuard) WriteFile(path string, data []byte, perm os.FileMode) error {
	root, rel, err := g.allowWrite(path)
	if err != nil {
		return err
	}
	if rel == "." {
		return fmt.Errorf("%w: %q", ErrPathDenied, path)
	}
	return g.writeRel(root, rel, path, data, perm)
}

// ReadFile reads path after walking from an allowed root without following
// symlinks.
func (g *PathGuard) ReadFile(path string) ([]byte, error) {
	root, rel, err := g.allowRead(path)
	if err != nil {
		return nil, err
	}
	return g.readRel(root, rel, path)
}

// MkdirAll creates path and its parents without following symlinks.
func (g *PathGuard) MkdirAll(path string, perm os.FileMode) error {
	root, rel, err := g.allowWrite(path)
	if err != nil {
		return err
	}
	return g.mkdirRel(root, rel, path, perm)
}

// Lstat returns info for path without following a final symlink.
func (g *PathGuard) Lstat(path string) (os.FileInfo, error) {
	root, rel, err := g.allowRead(path)
	if err != nil {
		return nil, err
	}
	return g.lstatRel(root, rel, path)
}

// CheckDir returns path after walking from a workspace root without following
// symlinks. Service uses this for work_dir so a swapped parent cannot move
// the child process's cwd outside the workspace.
func (g *PathGuard) CheckDir(path string) (string, error) {
	root, rel, err := g.allowDir(path)
	if err != nil {
		return "", err
	}
	if err := g.dirRel(root, rel, path); err != nil {
		return "", err
	}
	if rel == "." {
		return root, nil
	}
	return filepath.Join(root, rel), nil
}

func (g *PathGuard) allowDir(path string) (root, rel string, err error) {
	path, err = cleanAbs(path)
	if err != nil {
		return "", "", err
	}
	if g.denied(path) {
		return "", "", fmt.Errorf("%w: %q", ErrPathDenied, path)
	}
	for _, wr := range g.writable {
		if !PathUnder(path, wr.Path) {
			continue
		}
		rel, err = relativeInside(wr.Path, path)
		if err != nil {
			continue
		}
		return wr.Path, rel, nil
	}
	return "", "", fmt.Errorf("%w: %q", ErrPathDenied, path)
}
