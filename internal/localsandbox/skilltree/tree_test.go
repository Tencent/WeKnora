package skilltree

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func newTree(t *testing.T) *Tree {
	t.Helper()
	tree, err := New(filepath.Join(t.TempDir(), ".weknora", "skills"))
	require.NoError(t, err)
	return tree
}

func writeSkill(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: pdf\n---\n"), 0o644))
}

func TestNewRejectsRelativeRoot(t *testing.T) {
	_, err := New("skills")
	require.Error(t, err)
}

func TestNewVersionNumbersPerSkill(t *testing.T) {
	tree := newTree(t)
	v1, err := tree.NewVersion("pdf")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(tree.VersionsRoot(), "pdf-1"), v1)
	v2, err := tree.NewVersion("pdf")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(tree.VersionsRoot(), "pdf-2"), v2)

	// A skill whose name extends another's must not share its counter.
	other, err := tree.NewVersion("pdf-tools")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(tree.VersionsRoot(), "pdf-tools-1"), other)
	v3, err := tree.NewVersion("pdf")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(tree.VersionsRoot(), "pdf-3"), v3)
}

func TestInvalidNamesAreRejected(t *testing.T) {
	tree := newTree(t)
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "a\x00b"} {
		_, err := tree.NewVersion(name)
		require.ErrorIs(t, err, ErrInvalidName, name)
	}
}

func TestActivateSwapsLinkAndReportsPrevious(t *testing.T) {
	tree := newTree(t)
	v1, _ := tree.NewVersion("pdf")
	writeSkill(t, v1)
	prev, err := tree.Activate("pdf", v1)
	require.NoError(t, err)
	require.Empty(t, prev)
	require.True(t, tree.Installed("pdf"))

	v2, _ := tree.NewVersion("pdf")
	writeSkill(t, v2)
	prev, err = tree.Activate("pdf", v2)
	require.NoError(t, err)
	require.Equal(t, v1, prev)

	link, _ := tree.LinkPath("pdf")
	target, err := os.Readlink(link)
	require.NoError(t, err)
	require.Equal(t, v2, target)
	_, err = os.Lstat(filepath.Join(tree.Root(), ".next-pdf"))
	require.True(t, os.IsNotExist(err))
}

func TestActivateRefusesAnotherSkillsVersion(t *testing.T) {
	tree := newTree(t)
	other, _ := tree.NewVersion("docx")
	_, err := tree.Activate("pdf", other)
	require.Error(t, err)
	_, err = tree.Activate("pdf", t.TempDir())
	require.Error(t, err)
}

func TestPruneKeepsOnlyListedVersions(t *testing.T) {
	tree := newTree(t)
	v1, _ := tree.NewVersion("pdf")
	v2, _ := tree.NewVersion("pdf")
	v3, _ := tree.NewVersion("pdf")
	keepOther, _ := tree.NewVersion("docx")
	require.NoError(t, tree.Prune("pdf", v3, v2))
	require.NoDirExists(t, v1)
	require.DirExists(t, v2)
	require.DirExists(t, v3)
	require.DirExists(t, keepOther)
}

func TestRemoveDeletesLinkAndEveryVersion(t *testing.T) {
	tree := newTree(t)
	v1, _ := tree.NewVersion("pdf")
	writeSkill(t, v1)
	_, err := tree.Activate("pdf", v1)
	require.NoError(t, err)
	require.NoError(t, tree.Remove("pdf"))
	require.False(t, tree.Installed("pdf"))
	require.NoDirExists(t, v1)
	require.NoError(t, tree.Remove("pdf"), "removing twice is a no-op")
}

func TestDiscardRefusesPathsOutsideVersions(t *testing.T) {
	tree := newTree(t)
	require.Error(t, tree.Discard(tree.Root()))
	require.Error(t, tree.Discard(filepath.Join(tree.VersionsRoot(), "..", "x")))
	v1, _ := tree.NewVersion("pdf")
	require.NoError(t, tree.Discard(v1))
	require.NoDirExists(t, v1)
}

func TestSweepRemovesOrphansAndStaleNextLinks(t *testing.T) {
	tree := newTree(t)
	live, _ := tree.NewVersion("pdf")
	writeSkill(t, live)
	_, err := tree.Activate("pdf", live)
	require.NoError(t, err)
	orphan, _ := tree.NewVersion("pdf")
	require.NoError(t, os.Symlink(orphan, filepath.Join(tree.Root(), ".next-pdf")))

	require.NoError(t, tree.Sweep())
	require.DirExists(t, live)
	require.NoDirExists(t, orphan)
	_, err = os.Lstat(filepath.Join(tree.Root(), ".next-pdf"))
	require.True(t, os.IsNotExist(err))
}

func TestSweepKeepsThePreviousVersion(t *testing.T) {
	tree := newTree(t)
	previous, _ := tree.NewVersion("pdf")
	writeSkill(t, previous)
	live, _ := tree.NewVersion("pdf")
	writeSkill(t, live)
	_, err := tree.Activate("pdf", live)
	require.NoError(t, err)

	require.NoError(t, tree.Sweep())
	require.DirExists(t, previous)
	require.DirExists(t, live)
}

// Another Lite process holding the lock is mid-install; its version dir stays.
func TestSweepSkipsASkillLockedElsewhere(t *testing.T) {
	tree := newTree(t)
	installing, _ := tree.NewVersion("pdf")
	unlock, err := tree.Lock("pdf")
	require.NoError(t, err)

	require.NoError(t, tree.Sweep())
	require.DirExists(t, installing)

	unlock()
	require.NoError(t, tree.Sweep())
	require.NoDirExists(t, installing)
}

func TestLockIsExclusivePerSkill(t *testing.T) {
	tree := newTree(t)
	unlock, err := tree.Lock("pdf")
	require.NoError(t, err)
	unlock()
	unlock2, err := tree.Lock("pdf")
	require.NoError(t, err)
	unlock2()
}
