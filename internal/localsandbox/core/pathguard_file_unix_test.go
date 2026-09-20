//go:build unix

package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The file tools run unsandboxed. Check-then-os.WriteFile follows a parent
// that is swapped for a symlink in the window between the two.
func TestPathGuardWriteFileDoesNotFollowReplacedParent(t *testing.T) {
	root, g := guardFixture(t)
	inner := filepath.Join(root, "out")
	require.NoError(t, os.Mkdir(inner, 0o755))
	secrets := filepath.Join(filepath.Dir(root), "secrets")

	resolved, err := g.CheckWrite(filepath.Join(inner, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(inner, "a.txt"), resolved)

	require.NoError(t, os.Remove(inner))
	require.NoError(t, os.Symlink(secrets, inner))

	err = g.WriteFile(filepath.Join(root, "out", "a.txt"), []byte("pwned"), 0o644)
	require.ErrorIs(t, err, ErrPathDenied)

	_, statErr := os.Stat(filepath.Join(secrets, "a.txt"))
	require.Error(t, statErr)
	require.True(t, os.IsNotExist(statErr))
}

func TestPathGuardWriteFileDoesNotFollowInWorkspaceSymlink(t *testing.T) {
	root, g := guardFixture(t)
	realDir := filepath.Join(root, "real")
	require.NoError(t, os.Mkdir(realDir, 0o755))
	require.NoError(t, os.Symlink(realDir, filepath.Join(root, "link")))

	err := g.WriteFile(filepath.Join(root, "link", "x.txt"), []byte("x"), 0o644)
	require.ErrorIs(t, err, ErrPathDenied)
	_, statErr := os.Stat(filepath.Join(realDir, "x.txt"))
	require.True(t, os.IsNotExist(statErr))
}

func TestPathGuardReadFileDoesNotFollowSymlinkEscape(t *testing.T) {
	root, g := guardFixture(t)
	secrets := filepath.Join(filepath.Dir(root), "secrets", "id_rsa")
	require.NoError(t, os.WriteFile(secrets, []byte("key"), 0o644))
	require.NoError(t, os.Symlink(secrets, filepath.Join(root, "stolen")))

	_, err := g.ReadFile(filepath.Join(root, "stolen"))
	require.ErrorIs(t, err, ErrPathDenied)
}
