//go:build !unix

package git_repo

// lockCloneDir is a no-op on non-Unix hosts. In-process repoDirMutex still
// serializes overlapping syncs of the same data source on this process.
func lockCloneDir(dir string) (func(), error) {
	if dir == "" {
		return func() {}, errUnsafeCloneDir
	}
	return func() {}, nil
}
