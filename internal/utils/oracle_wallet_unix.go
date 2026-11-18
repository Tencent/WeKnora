//go:build unix

package utils

import (
	"io/fs"
	"os"
	"syscall"
)

// ownedByCurrentUser reports whether fi belongs to the effective uid.
func ownedByCurrentUser(fi fs.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Geteuid()
}
