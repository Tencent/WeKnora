package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// guardFixture builds a real on-disk workspace so EvalSymlinks has something
// to resolve. The directory name contains a space on purpose.
func guardFixture(t *testing.T) (root string, g *PathGuard) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	root = filepath.Join(base, "My Project")
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	secrets := filepath.Join(base, "secrets")
	require.NoError(t, os.MkdirAll(secrets, 0o755))

	p := Policy{
		WritableRoots: []WritableRoot{{
			Path:             root,
			ReadOnlySubpaths: []string{filepath.Join(root, ".git")},
		}},
		DenyRead: []string{secrets},
		Cwd:      root,
	}
	require.NoError(t, p.Validate())
	return root, NewPathGuard(p)
}

func TestPathGuardAllowsWriteInsideRoot(t *testing.T) {
	root, g := guardFixture(t)
	got, err := g.CheckWrite(filepath.Join(root, "sub", "new.txt"))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "sub", "new.txt"), got)
}

func TestPathGuardRejectsWriteOutsideRoot(t *testing.T) {
	root, g := guardFixture(t)
	_, err := g.CheckWrite(filepath.Join(filepath.Dir(root), "outside.txt"))
	require.ErrorIs(t, err, ErrPathDenied)
}

func TestPathGuardRejectsWriteToReadOnlySubpath(t *testing.T) {
	root, g := guardFixture(t)
	_, err := g.CheckWrite(filepath.Join(root, ".git", "config"))
	require.ErrorIs(t, err, ErrPathDenied)
}

func TestPathGuardRejectsDotDotEscape(t *testing.T) {
	root, g := guardFixture(t)
	_, err := g.CheckWrite(filepath.Join(root, "..", "escaped.txt"))
	require.ErrorIs(t, err, ErrPathDenied)
}

// A symlink inside the workspace pointing out of it must not widen the guard.
func TestPathGuardRejectsSymlinkEscape(t *testing.T) {
	root, g := guardFixture(t)
	target := filepath.Dir(root)
	link := filepath.Join(root, "escape")
	require.NoError(t, os.Symlink(target, link))

	_, err := g.CheckWrite(filepath.Join(link, "pwned.txt"))
	require.ErrorIs(t, err, ErrPathDenied)
}

func TestPathGuardRejectsReadOfDeniedPath(t *testing.T) {
	root, g := guardFixture(t)
	secrets := filepath.Join(filepath.Dir(root), "secrets", "id_rsa")
	_, err := g.CheckRead(secrets)
	require.ErrorIs(t, err, ErrPathDenied)
}

func TestPathGuardAllowsReadOfReadOnlySubpath(t *testing.T) {
	root, g := guardFixture(t)
	_, err := g.CheckRead(filepath.Join(root, ".git", "config"))
	require.NoError(t, err)
}

func TestPathGuardRejectsRelativePath(t *testing.T) {
	_, g := guardFixture(t)
	_, err := g.CheckRead("relative/path.txt")
	require.ErrorIs(t, err, ErrPathDenied)
}

func TestPathGuardWriteFileCreatesInsideRoot(t *testing.T) {
	root, g := guardFixture(t)
	path := filepath.Join(root, "sub", "new.txt")
	require.NoError(t, g.WriteFile(path, []byte("hello"), 0o644))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, []byte("hello"), got)
}

func TestPathGuardWriteFileRejectsOutsideRoot(t *testing.T) {
	root, g := guardFixture(t)
	err := g.WriteFile(filepath.Join(filepath.Dir(root), "outside.txt"), []byte("nope"), 0o644)
	require.ErrorIs(t, err, ErrPathDenied)
}

func TestPathGuardReadFileReturnsContents(t *testing.T) {
	root, g := guardFixture(t)
	path := filepath.Join(root, "notes.txt")
	require.NoError(t, os.WriteFile(path, []byte("hi"), 0o644))
	got, err := g.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, []byte("hi"), got)
}
