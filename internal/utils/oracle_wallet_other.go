//go:build !unix

package utils

import "io/fs"

// ownedByCurrentUser has no POSIX owner to check on these platforms (Windows
// keeps the per-user temp dir private by ACL).
func ownedByCurrentUser(fs.FileInfo) bool { return true }
