package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProposeFileGrantOffersOnlyTheFile(t *testing.T) {
	env := proposeFixture(t)
	target := filepath.Join(env.other, "a.txt")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o644))

	g, ok := env.b.ProposeFileGrant(env.p, target)
	require.True(t, ok)
	require.Equal(t, Grant{Path: target, Access: AccessWrite, File: true}, g)

	missing := filepath.Join(env.other, "new.txt")
	g, ok = env.b.ProposeFileGrant(env.p, missing)
	require.True(t, ok, "a new file in an existing directory may be offered")
	require.Equal(t, missing, g.Path)
}

func TestProposeFileGrantRefusesWhatAFileGrantCannotCover(t *testing.T) {
	env := proposeFixture(t)
	dir := filepath.Join(env.other, "dir")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(env.other, ".git"), 0o755))

	for _, path := range []string{
		dir, // a directory
		filepath.Join(env.other, "missing", "a.txt"),    // parent missing
		filepath.Join(env.home, ".ssh", "id_rsa"),       // credentials
		filepath.Join(env.other, ".git", "config"),      // git metadata
		filepath.Join(env.home, "Library", "Prefs.txt"), // ~/Library
		filepath.Join(env.root, "inside.txt"),           // already writable
		"relative.txt",
	} {
		_, ok := env.b.ProposeFileGrant(env.p, path)
		require.False(t, ok, path)
	}
}

func TestRelaxFileGrantOpensOnlyThatFile(t *testing.T) {
	env := proposeFixture(t)
	target := filepath.Join(env.other, "a.txt")
	require.NoError(t, os.WriteFile(target, []byte("old"), 0o644))

	relaxed, err := env.b.Relax(env.p, Grant{Path: target, Access: AccessWrite, File: true})
	require.NoError(t, err)
	require.Equal(t, []string{target}, relaxed.WritableFiles)
	require.Len(t, relaxed.WritableRoots, len(env.p.WritableRoots), "no directory is opened")

	guard := NewPathGuard(relaxed)
	require.NoError(t, guard.WriteFile(target, []byte("new"), 0o644))
	got, err := guard.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "new", string(got))

	_, err = guard.CheckWrite(filepath.Join(env.other, "b.txt"))
	require.ErrorIs(t, err, ErrPathDenied)
	require.ErrorIs(t, guard.WriteFile(filepath.Join(env.other, "b.txt"), []byte("x"), 0o644), ErrPathDenied)
}

func TestRelaxFileGrantRefusesADirectory(t *testing.T) {
	env := proposeFixture(t)
	_, err := env.b.Relax(env.p, Grant{Path: env.other, Access: AccessWrite, File: true})
	require.Error(t, err)
}

func TestFingerprintCoversWritableFiles(t *testing.T) {
	env := proposeFixture(t)
	a := filepath.Join(env.other, "a.txt")
	b := filepath.Join(env.other, "b.txt")

	withA, err := env.b.Relax(env.p, Grant{Path: a, Access: AccessWrite, File: true})
	require.NoError(t, err)
	require.NotEqual(t, env.p.Fingerprint(), withA.Fingerprint())

	ab, err := env.b.Relax(withA, Grant{Path: b, Access: AccessWrite, File: true})
	require.NoError(t, err)
	ba := ab
	ba.WritableFiles = []string{b, a}
	require.Equal(t, ab.Fingerprint(), ba.Fingerprint(), "order does not matter")
}
