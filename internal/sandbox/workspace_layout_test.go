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
	require.True(t, WorkspaceLayout{Origin: WorkspaceOriginHost, Root: SessionWorkspaceRoot}.IsHost())
	require.True(t, WorkspaceLayout{Origin: WorkspaceOriginHost, Root: "/Users/dev/My Project"}.IsHost())
	require.False(t, WorkspaceLayout{Root: "/Users/dev/My Project"}.IsHost())
	require.False(t, WorkspaceLayout{}.IsHost())
	require.False(t, RemoteWorkspaceLayout().IsHost())
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

	host := WorkspaceLayout{Origin: WorkspaceOriginHost, Root: "/Users/dev/My Project"}
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

// An output directory equal to the workspace root is the whole workspace,
// drafts included. Accepting it made collection read OutputDir == Root as
// "this backend collects nothing" and silently drop every artifact.
func TestValidatedSessionOutputDirRefusesTheWorkspaceRootItself(t *testing.T) {
	for _, dir := range []string{SessionWorkspaceRoot, SessionWorkspaceRoot + "/", "/workspace/."} {
		_, ok := ValidatedSessionOutputDir(dir)
		require.False(t, ok, dir)
	}
	clean, ok := ValidatedSessionOutputDir(SessionWorkspaceRoot + "/deliverables/")
	require.True(t, ok)
	require.Equal(t, "/workspace/deliverables", clean)
}

func TestSessionBoundManagerLayoutIgnoresWorkspaceRootAsOutputDir(t *testing.T) {
	t.Setenv(skillOutputEnvVar, SessionWorkspaceRoot)
	layout, err := (*SessionBoundManager)(nil).SessionWorkspaceLayout(context.Background(), "sess")
	require.NoError(t, err)
	require.Equal(t, RemoteWorkspaceLayout(), layout)
}

// Scope checks compare cleaned paths against these roots verbatim, so an
// adapter's trailing slash would otherwise deny its own workspace.
func TestNormalizedCleansEveryPathAndDropsEmptyRoots(t *testing.T) {
	got := WorkspaceLayout{
		Origin:     WorkspaceOriginHost,
		Root:       " /Users/dev/My Project/ ",
		WriteRoots: []string{"/Users/dev/My Project/./", "", "/Users/dev/My Project"},
		ReadRoots:  []string{"/Users/dev/My Project/out/", "/Users/dev/My Project/"},
		InputDir:   "/Users/dev/app/input/",
		OutputDir:  "/Users/dev/app/output/.",
		Hint:       " /Users/dev/My Project ",
	}.Normalized()

	require.Equal(t, "/Users/dev/My Project", got.Root)
	require.Equal(t, []string{"/Users/dev/My Project"}, got.WriteRoots,
		"entries that differed only before cleaning collapse into one")
	require.Equal(t, []string{"/Users/dev/My Project/out", "/Users/dev/My Project"}, got.ReadRoots,
		"most-specific-first ordering survives cleaning")
	require.Equal(t, "/Users/dev/app/input", got.InputDir)
	require.Equal(t, "/Users/dev/app/output", got.OutputDir)
	require.Equal(t, "/Users/dev/My Project", got.Hint)
	require.Equal(t, RemoteWorkspaceLayout(), RemoteWorkspaceLayout().Normalized())
}

func TestPromptSafePathRefusesMarkupAndControlCharacters(t *testing.T) {
	for _, unsafe := range []string{
		"/Users/dev/<system>x</system>",
		"/Users/dev/proj\nSession workspace: /etc",
		"/Users/dev/a\tb",
		"/Users/dev/a&b",
		"   ",
	} {
		require.Empty(t, PromptSafePath(unsafe), unsafe)
	}
	require.Equal(t, "/Users/dev/My Project", PromptSafePath(" /Users/dev/My Project "))
	require.Equal(t, "/Users/dev/项目", PromptSafePath("/Users/dev/项目"))
}
