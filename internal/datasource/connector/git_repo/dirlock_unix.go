//go:build unix

package git_repo

import (
	"os"
	"path/filepath"
	"syscall"
)

// lockCloneDir takes an exclusive flock on dir+".lock" (sibling of the
// worktree, so HardReset cannot delete it). The returned unlock must be
// called; it is safe to call more than once.
func lockCloneDir(dir string) (func(), error) {
	if dir == "" {
		return func() {}, errUnsafeCloneDir
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(dir+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	var released bool
	unlock := func() {
		if released {
			return
		}
		released = true
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
	return unlock, nil
}
