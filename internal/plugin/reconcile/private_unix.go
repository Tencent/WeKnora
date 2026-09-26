//go:build unix

package reconcile

import (
	"fmt"
	"os"
	"syscall"
)

// ensurePrivateDir makes sure dir is a real directory of this user that no
// one else can write into, tightening its mode if it is ours.
func ensurePrivateDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(st.Uid) != os.Getuid() {
		return fmt.Errorf("owned by uid %d, not this process's %d", st.Uid, os.Getuid())
	}
	if info.Mode().Perm()&0o077 != 0 {
		return os.Chmod(dir, 0o700)
	}
	return nil
}
