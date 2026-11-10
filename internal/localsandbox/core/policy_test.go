package core

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func validPolicy() Policy {
	return Policy{
		WritableRoots: []WritableRoot{{
			Path:             "/Users/dev/My Project",
			ReadOnlySubpaths: []string{"/Users/dev/My Project/.git"},
		}},
		ReadableRoots: []string{"/usr/lib"},
		DenyRead:      []string{"/Users/dev/.ssh"},
		Network:       NetworkDenied,
		Cwd:           "/Users/dev/My Project",
	}
}

func TestPolicyValidateAcceptsPathsWithSpaces(t *testing.T) {
	require.NoError(t, validPolicy().Validate())
}

func TestPolicyValidateRejectsNoWritableRoot(t *testing.T) {
	p := validPolicy()
	p.WritableRoots = nil
	require.ErrorIs(t, p.Validate(), ErrNoWritableRoot)
}

func TestPolicyValidateAcceptsAskModeWithCwdUnderReadableRoot(t *testing.T) {
	p := validPolicy()
	p.WritableRoots = nil
	p.ReadableRoots = append(p.ReadableRoots, p.Cwd)
	require.NoError(t, p.Validate())
}

func TestPathUnderRejectsEmptyRoot(t *testing.T) {
	require.False(t, PathUnder("/Users/dev/file", ""))
	require.False(t, PathUnder("", "/Users/dev"))
	require.False(t, PathUnder("", ""))
}

func TestPolicyValidateRejectsFilesystemRoot(t *testing.T) {
	p := validPolicy()
	p.WritableRoots = []WritableRoot{{Path: "/"}}
	p.Cwd = "/"
	require.ErrorIs(t, p.Validate(), ErrFilesystemRoot)
}

func TestPolicyValidateRejectsWellKnownWideWritableRoot(t *testing.T) {
	for _, wide := range []string{"/var", "/private/var", "/tmp", "/System/Volumes/Data"} {
		if !filepath.IsAbs(wide) {
			continue
		}
		p := validPolicy()
		p.WritableRoots = []WritableRoot{{Path: wide}}
		p.Cwd = wide
		require.ErrorIs(t, p.Validate(), ErrWorkspaceTooBroad, wide)
	}
}

func TestPolicyValidateRejectsDenyReadOfFilesystemRoot(t *testing.T) {
	p := validPolicy()
	p.DenyRead = []string{"/"}
	require.ErrorIs(t, p.Validate(), ErrFilesystemRoot)
}

// A private root that is "/" would deny reading the whole machine, including
// the paths exec itself needs.
func TestPolicyValidateRejectsFilesystemRootAsPrivateRoot(t *testing.T) {
	p := validPolicy()
	p.PrivateRoots = []string{"/"}
	require.ErrorIs(t, p.Validate(), ErrFilesystemRoot)
}

// A private root covering the workspace is the normal case (home contains the
// project); the compiler re-opens it afterwards.
func TestPolicyValidateAllowsPrivateRootCoveringWritableRoot(t *testing.T) {
	p := validPolicy()
	p.PrivateRoots = []string{"/Users/dev"}
	require.NoError(t, p.Validate())
}

func TestPolicyFingerprintCoversPrivateRoots(t *testing.T) {
	base := validPolicy()
	withPrivate := validPolicy()
	withPrivate.PrivateRoots = []string{"/Users/dev"}
	require.NotEqual(t, base.Fingerprint(), withPrivate.Fingerprint())
}

func TestPolicyValidateRejectsRelativePath(t *testing.T) {
	p := validPolicy()
	p.ReadableRoots = []string{"usr/lib"}
	require.ErrorIs(t, p.Validate(), ErrRelativePath)
}

func TestPolicyValidateRejectsCwdOutsideRoots(t *testing.T) {
	p := validPolicy()
	p.Cwd = "/Users/dev/elsewhere"
	require.ErrorIs(t, p.Validate(), ErrCwdOutsideRoots)
}

// A path that was safe at approval can be replaced by a symlink before the
// backend resolves it. Validate must refuse the resolved target when it is
// git metadata, ~/Library, or a system path, for both files and roots.
func TestPolicyValidateRejectsResolvedGrantTargets(t *testing.T) {
	home := "/Users/dev"
	workspace := filepath.Join(home, "My Project")
	private := []string{home, "/Users", "/Volumes", "/var", "/private/var"}

	base := func() Policy {
		return Policy{
			WritableRoots: []WritableRoot{{Path: workspace}},
			PrivateRoots:  private,
			Cwd:           workspace,
		}
	}

	t.Run("git file outside writable root", func(t *testing.T) {
		p := base()
		p.WritableFiles = []string{"/Users/dev/other/.git/config"}
		require.Error(t, p.Validate())
	})
	t.Run("home library file", func(t *testing.T) {
		p := base()
		p.WritableFiles = []string{filepath.Join(home, "Library", "Prefs.txt")}
		require.Error(t, p.Validate())
	})
	t.Run("etc passwd", func(t *testing.T) {
		p := base()
		p.WritableFiles = []string{"/etc/passwd"}
		require.Error(t, p.Validate())
	})

	for _, path := range []string{
		"/Users/dev/other/.git",
		filepath.Join(home, "Library"),
		"/etc/passwd",
	} {
		t.Run("writable root "+path, func(t *testing.T) {
			p := Policy{
				WritableRoots: []WritableRoot{{Path: path}},
				PrivateRoots:  private,
				Cwd:           path,
			}
			require.Error(t, p.Validate())
		})
	}

	t.Run("writable file is home", func(t *testing.T) {
		p := base()
		p.WritableFiles = []string{home}
		require.Error(t, p.Validate())
	})
	t.Run("writable root is home", func(t *testing.T) {
		p := Policy{
			WritableRoots: []WritableRoot{{Path: home}},
			PrivateRoots:  private,
			Cwd:           home,
		}
		require.Error(t, p.Validate())
	})

	t.Run("ordinary file", func(t *testing.T) {
		p := base()
		p.WritableFiles = []string{filepath.Join(home, "Desktop", "a.txt")}
		require.NoError(t, p.Validate())
	})
}

// A deny-read entry that swallows a writable root is a contradiction the
// backends cannot express coherently; reject it at the door.
func TestPolicyValidateRejectsDenyReadCoveringWritableRoot(t *testing.T) {
	p := validPolicy()
	p.DenyRead = []string{"/Users/dev"}
	require.ErrorIs(t, p.Validate(), ErrDenyReadCoversRoot)
}

func TestPolicyFingerprintIsStableAndDiscriminating(t *testing.T) {
	a := validPolicy()
	b := validPolicy()
	require.Equal(t, a.Fingerprint(), b.Fingerprint())

	b.Network = NetworkUnrestricted
	require.NotEqual(t, a.Fingerprint(), b.Fingerprint())
}

// Ordering must not change identity: the same permissions written in a
// different order describe the same sandbox.
func TestPolicyFingerprintIgnoresOrdering(t *testing.T) {
	a := validPolicy()
	a.ReadableRoots = []string{"/usr/lib", "/bin"}
	b := validPolicy()
	b.ReadableRoots = []string{"/bin", "/usr/lib"}
	require.Equal(t, a.Fingerprint(), b.Fingerprint())
}
