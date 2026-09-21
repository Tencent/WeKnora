package sandbox

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// The remote layout must describe exactly the constants the tools used to
// reference directly; this is the anchor for every behaviour-preservation test.
func TestRemoteWorkspaceLayoutMatchesLegacyConstants(t *testing.T) {
	l := RemoteWorkspaceLayout()

	require.Equal(t, SessionWorkspaceRoot, l.Root)
	require.Equal(t, []string{SessionWorkspaceRoot, SessionOutputRoot}, l.WriteRoots)
	require.Equal(t, SessionInputRoot, l.InputDir)
	require.Equal(t, SessionOutputRoot, l.OutputDir)
	require.Contains(t, l.Hint, SessionWorkspaceRoot)
	require.False(t, l.IsHost())
}

func TestHostWorkspaceLayoutIsHost(t *testing.T) {
	require.True(t, WorkspaceLayout{Root: "/Users/dev/My Project"}.IsHost())
	require.False(t, WorkspaceLayout{}.IsHost())
}

// ReadRoots must stay most-specific-first: the reported root has to name the
// narrowest match, which is what the list/read tools show the model.
func TestRemoteWorkspaceLayoutOrdersReadRootsMostSpecificFirst(t *testing.T) {
	l := RemoteWorkspaceLayout()
	require.Equal(t, SessionWorkspaceRoot, l.ReadRoots[len(l.ReadRoots)-1])
	require.Contains(t, l.ReadRoots, SessionInputRoot)
}

func TestResolveWorkspacePathInJoinsRelativeAgainstLayoutRoot(t *testing.T) {
	remote := RemoteWorkspaceLayout()
	require.Equal(t, "/workspace/a.txt", ResolveWorkspacePathIn(remote, "a.txt"))
	require.Equal(t, "/etc/passwd", ResolveWorkspacePathIn(remote, "/etc/passwd"))

	host := WorkspaceLayout{Root: "/Users/dev/My Project"}
	require.Equal(t, "/Users/dev/My Project/a.txt", ResolveWorkspacePathIn(host, "a.txt"))
}

func TestSessionBoundManagerProvidesRemoteWorkspaceLayout(t *testing.T) {
	var _ SessionWorkspaceLayoutProvider = (*SessionBoundManager)(nil)

	t.Setenv(skillOutputEnvVar, "")
	layout, err := (*SessionBoundManager)(nil).SessionWorkspaceLayout(context.Background(), "sess")
	require.NoError(t, err)
	require.Equal(t, RemoteWorkspaceLayout(), layout)
}

func TestSessionBoundManagerLayoutOverlaysValidatedSkillOutputDir(t *testing.T) {
	mgr := (*SessionBoundManager)(nil)

	t.Setenv(skillOutputEnvVar, "/workspace/custom-output")
	layout, err := mgr.SessionWorkspaceLayout(context.Background(), "sess")
	require.NoError(t, err)
	require.Equal(t, "/workspace/custom-output", layout.OutputDir)
	require.Equal(t, "/workspace/custom-output", layout.ReadRoots[0])
	require.Equal(t, []string{SessionInputRoot, SessionWorkspaceRoot}, layout.ReadRoots[1:])
	require.Equal(t, RemoteWorkspaceLayout().WriteRoots, layout.WriteRoots)
	require.Equal(t, SessionOutputRoot, RemoteWorkspaceLayout().OutputDir)
	require.Equal(t, SessionOutputRoot, RemoteWorkspaceLayout().ReadRoots[0])

	t.Setenv(skillOutputEnvVar, "/tmp/outside")
	layout, err = mgr.SessionWorkspaceLayout(context.Background(), "sess")
	require.NoError(t, err)
	require.Equal(t, RemoteWorkspaceLayout(), layout)

	t.Setenv(skillOutputEnvVar, "/Users/dev/.ssh")
	layout, err = mgr.SessionWorkspaceLayout(context.Background(), "sess")
	require.NoError(t, err)
	require.Equal(t, RemoteWorkspaceLayout(), layout)
}
