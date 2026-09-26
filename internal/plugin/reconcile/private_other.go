//go:build !unix

package reconcile

// ensurePrivateDir is a no-op where the temp directory is already per user
// (Windows).
func ensurePrivateDir(string) error { return nil }
