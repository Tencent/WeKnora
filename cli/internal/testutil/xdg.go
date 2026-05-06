// Package testutil holds small test-only helpers shared across cli/internal packages.
//
// Build tag is intentionally absent: Go links _test.go files only into test
// binaries, but other packages need to import this from their tests. Keep
// production code out.
package testutil

import (
	"testing"
)

// XDGTempDir creates a fresh temp dir and points XDG_CONFIG_HOME at it for the
// duration of t. Use whenever a test exercises code that reads
// $XDG_CONFIG_HOME/weknora/* (config.Load, secrets.NewFileStore, ...).
func XDGTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return dir
}
