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

// Seatbelt denies unlinking the workspace directory so it cannot be swapped
// for a symlink. PathGuard still has to refuse if that happens anyway: file
// tools do not run inside sandbox-exec.
func TestPathGuardWriteFileDoesNotFollowReplacedRoot(t *testing.T) {
	root, g := guardFixture(t)
	outside := filepath.Join(filepath.Dir(root), "outside")
	require.NoError(t, os.Mkdir(outside, 0o755))

	require.NoError(t, os.RemoveAll(root))
	require.NoError(t, os.Symlink(outside, root))

	err := g.WriteFile(filepath.Join(root, "pwned.txt"), []byte("x"), 0o644)
	require.ErrorIs(t, err, ErrPathDenied)
	_, statErr := os.Stat(filepath.Join(outside, "pwned.txt"))
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

func TestPathGuardLstatDoesNotFollowReplacedParent(t *testing.T) {
	root, g := guardFixture(t)
	inner := filepath.Join(root, "out")
	require.NoError(t, os.Mkdir(inner, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(inner, "a.txt"), []byte("in"), 0o644))
	secrets := filepath.Join(filepath.Dir(root), "secrets")
	require.NoError(t, os.WriteFile(filepath.Join(secrets, "a.txt"), []byte("KEY"), 0o644))

	info, err := g.Lstat(filepath.Join(inner, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, int64(2), info.Size())

	require.NoError(t, os.RemoveAll(inner))
	require.NoError(t, os.Symlink(secrets, inner))

	_, err = g.Lstat(filepath.Join(root, "out", "a.txt"))
	require.ErrorIs(t, err, ErrPathDenied)
}

func TestPathGuardReadFileReadableFileRoot(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	root := filepath.Join(base, "My Project")
	require.NoError(t, os.MkdirAll(root, 0o755))
	rc := filepath.Join(base, "notes.txt")
	require.NoError(t, os.WriteFile(rc, []byte("ok"), 0o644))

	p := Policy{
		WritableRoots: []WritableRoot{{Path: root}},
		ReadableRoots: []string{rc},
		Cwd:           root,
	}
	require.NoError(t, p.Validate())
	g := NewPathGuard(p)
	got, err := g.ReadFile(rc)
	require.NoError(t, err)
	require.Equal(t, []byte("ok"), got)
}

func TestPathGuardLstatReportsFinalSymlink(t *testing.T) {
	root, g := guardFixture(t)
	target := filepath.Join(root, "real.txt")
	require.NoError(t, os.WriteFile(target, []byte("hi"), 0o644))
	link := filepath.Join(root, "alias.txt")
	require.NoError(t, os.Symlink(target, link))

	info, err := g.Lstat(link)
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSymlink)
}

func TestPathGuardCheckDirDoesNotFollowReplacedParent(t *testing.T) {
	root, g := guardFixture(t)
	inner := filepath.Join(root, "out")
	require.NoError(t, os.Mkdir(inner, 0o755))
	secrets := filepath.Join(filepath.Dir(root), "secrets")

	got, err := g.CheckDir(inner)
	require.NoError(t, err)
	require.Equal(t, inner, got)

	require.NoError(t, os.Remove(inner))
	require.NoError(t, os.Symlink(secrets, inner))

	_, err = g.CheckDir(inner)
	require.ErrorIs(t, err, ErrPathDenied)
}
