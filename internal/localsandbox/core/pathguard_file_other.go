//go:build !unix

package core

import (
	"fmt"
	"os"
	"path/filepath"
)

func (g *PathGuard) writeRel(root, rel, orig string, data []byte, perm os.FileMode) error {
	target, err := joinNoFollow(root, rel, orig)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, data, perm)
}

func (g *PathGuard) readRel(root, rel, orig string) ([]byte, error) {
	target, err := joinNoFollow(root, rel, orig)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(target)
}

func (g *PathGuard) mkdirRel(root, rel, orig string, perm os.FileMode) error {
	target, err := joinNoFollow(root, rel, orig)
	if err != nil {
		return err
	}
	return os.MkdirAll(target, perm)
}

func (g *PathGuard) dirRel(root, rel, orig string) error {
	target, err := joinNoFollow(root, rel, orig)
	if err != nil {
		return err
	}
	info, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%w: %q", ErrPathDenied, orig)
	}
	return nil
}

func (g *PathGuard) lstatRel(root, rel, orig string) (os.FileInfo, error) {
	if rel == "." {
		info, err := os.Lstat(root)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: %q", ErrPathDenied, orig)
		}
		return info, nil
	}
	parent := filepath.Dir(rel)
	if parent != "." {
		if _, err := joinNoFollow(root, parent, orig); err != nil {
			return nil, err
		}
	} else {
		info, err := os.Lstat(root)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: %q", ErrPathDenied, orig)
		}
	}
	return os.Lstat(filepath.Join(root, rel))
}

// joinNoFollow lexically joins rel onto root and refuses any component that
// is already a symlink. Host sandbox is not shipped off unix, so this is a
// best-effort stand-in for tests; it cannot close the openat window.
func joinNoFollow(root, rel, orig string) (string, error) {
	cur := root
	info, err := os.Lstat(cur)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: %q", ErrPathDenied, orig)
	}
	for _, part := range relParts(rel) {
		cur = filepath.Join(cur, part)
		info, err = os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%w: %q", ErrPathDenied, orig)
		}
	}
	if rel == "." {
		return root, nil
	}
	return filepath.Join(root, rel), nil
}
