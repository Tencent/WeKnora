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
