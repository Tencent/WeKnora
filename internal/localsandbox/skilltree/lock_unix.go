//go:build unix

package skilltree

import (
	"os"
	"path/filepath"
	"syscall"
)

// Lock serialises installs of one skill across Lite processes.
func (t *Tree) Lock(name string) (func(), error) {
	if err := validName(name); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(t.root, locksDir, name+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
