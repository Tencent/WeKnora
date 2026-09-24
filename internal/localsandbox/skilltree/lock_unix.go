//go:build unix

package skilltree

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// Lock serialises installs of one skill across Lite processes.
func (t *Tree) Lock(name string) (func(), error) {
	unlock, _, err := t.lock(name, true)
	return unlock, err
}

// tryLock takes the lock only if no other holder has it. ok is false, with a
// nil error, while another process is installing or removing name.
func (t *Tree) tryLock(name string) (func(), bool, error) {
	return t.lock(name, false)
}

func (t *Tree) lock(name string, wait bool) (func(), bool, error) {
	if err := validName(name); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(filepath.Join(t.root, locksDir, name+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	how := syscall.LOCK_EX
	if !wait {
		how |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		_ = f.Close()
		if !wait && errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, true, nil
}
