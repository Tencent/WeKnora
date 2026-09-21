package core

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func builderFixture(t *testing.T) (*PolicyBuilder, Workspace) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	appData := filepath.Join(home, "App Support", "WeKnora Lite")
	ws := Workspace{
		Kind:       WorkspaceProject,
		Root:       filepath.Join(home, "My Project"),
		ProtectGit: true,
	}
	return NewPolicyBuilder(home, appData), ws
}

func TestBuildAutoModeMakesWorkspaceWritable(t *testing.T) {
	b, ws := builderFixture(t)
	p, err := b.Build(ModeAuto, ws)
	require.NoError(t, err)

	require.Equal(t, ws.Root, p.Cwd)
	require.Equal(t, NetworkDenied, p.Network)

	var roots []string
	for _, r := range p.WritableRoots {
		roots = append(roots, r.Path)
	}
	require.Contains(t, roots, ws.Root)
	require.Len(t, roots, 1)
}

func TestBuildProtectsGitWhenWorkspaceAsksForIt(t *testing.T) {
	b, ws := builderFixture(t)
	p, err := b.Build(ModeAuto, ws)
	require.NoError(t, err)

	for _, r := range p.WritableRoots {
		if r.Path == ws.Root {
			require.Contains(t, r.ReadOnlySubpaths, filepath.Join(ws.Root, ".git"))
			return
		}
	}
	t.Fatal("workspace root missing from policy")
}

// A session workspace's .git belongs to the agent, so it is not protected.
func TestBuildDoesNotProtectGitForSessionWorkspace(t *testing.T) {
	b, ws := builderFixture(t)
	ws.Kind = WorkspaceSession
	ws.ProtectGit = false

	p, err := b.Build(ModeAuto, ws)
	require.NoError(t, err)
	for _, r := range p.WritableRoots {
		require.Empty(t, r.ReadOnlySubpaths)
	}
}

// Ask mode still compiles a sandbox; approval is enforced above this layer.
func TestBuildAskModeKeepsWorkspaceReadableButNotWritable(t *testing.T) {
	b, ws := builderFixture(t)
	p, err := b.Build(ModeAsk, ws)
	require.NoError(t, err)

	require.Contains(t, p.ReadableRoots, ws.Root)
	require.Empty(t, p.WritableRoots)
	require.Equal(t, ws.Root, p.Cwd)
}

// Full access has no policy at all; representing it as a very wide policy
// would invite treating it as "still sandboxed".
// Which modes this build can actually serve is a property of the sandbox, not
// of whatever happens to read the preferences file. One list, here.
func TestApprovalModeShippedIsAutoOnly(t *testing.T) {
	require.True(t, ModeAuto.Shipped())
	require.False(t, ModeAsk.Shipped(), "ask needs the approval round-trip")
	require.False(t, ModeFull.Shipped(), "full needs the unsandboxed path")
	require.False(t, ApprovalMode("yolo").Shipped())

	require.True(t, ModeAsk.Known(), "ask is a real mode, just not shipped yet")
	require.False(t, ApprovalMode("yolo").Known())
}

// Unknown values fall back to Auto so a typo cannot stop the agent.
// Known-but-unshipped modes stay themselves: mapping ask→auto would widen
// access past what was asked for, and Service refuses them instead.
func TestParseApprovalModeKeepsKnownModes(t *testing.T) {
	require.Equal(t, ModeAuto, ParseApprovalMode("auto"))
	require.Equal(t, ModeAuto, ParseApprovalMode(" AUTO "))
	require.Equal(t, ModeAsk, ParseApprovalMode("ask"))
	require.Equal(t, ModeFull, ParseApprovalMode("full"))
	for _, raw := range []string{"", "yolo"} {
		require.Equal(t, ModeAuto, ParseApprovalMode(raw), raw)
	}
}

func TestBuildFullAccessHasNoPolicy(t *testing.T) {
	b, ws := builderFixture(t)
	_, err := b.Build(ModeFull, ws)
	require.ErrorIs(t, err, ErrFullAccessHasNoPolicy)
}

func TestBuildDeniesReadingCredentialDirectories(t *testing.T) {
	b, ws := builderFixture(t)
	p, err := b.Build(ModeAuto, ws)
	require.NoError(t, err)

	require.Contains(t, p.DenyRead, filepath.Join(b.homeDir, ".ssh"))
	require.Contains(t, p.DenyRead, filepath.Join(b.homeDir, ".aws"))
	require.Contains(t, p.DenyRead, filepath.Join(b.homeDir, ".config", "git", "credentials"))
	require.Contains(t, p.DenyRead, b.appDataDir)
}

// Seatbelt cannot enforce a read allowlist on darwin (dropping the blanket
// file-read* aborts exec), so the home directory is denied wholesale and the
// few paths a build legitimately needs are re-opened after it.
func TestBuildMakesHomePrivate(t *testing.T) {
	b, ws := builderFixture(t)
	p, err := b.Build(ModeAuto, ws)
	require.NoError(t, err)

	require.Contains(t, p.PrivateRoots, b.homeDir)
}

// The workspace normally lives inside the home directory, so the private deny
// covers it. It has to be re-opened or the agent cannot read its own files.
func TestBuildReAllowsWorkspaceInsidePrivateHome(t *testing.T) {
	b, ws := builderFixture(t)
	p, err := b.Build(ModeAuto, ws)
	require.NoError(t, err)

	require.True(t, PathUnder(ws.Root, b.homeDir), "fixture must put the workspace in home")
	require.Contains(t, p.ReadableRoots, ws.Root)
}

// Per-user toolchains (nvm, pyenv, cargo) and the login shell's startup files
// live in home. Denying them would leave the agent unable to run node at all.
func TestBuildReAllowsToolchainsButNotShellStartupFiles(t *testing.T) {
	b, ws := builderFixture(t)
	p, err := b.Build(ModeAuto, ws)
	require.NoError(t, err)

	require.Contains(t, p.ReadableRoots, filepath.Join(b.homeDir, ".nvm"))
	require.Contains(t, p.ReadableRoots, filepath.Join(b.homeDir, ".cargo"))
	require.Contains(t, p.ReadableRoots, filepath.Join(b.homeDir, ".local", "bin"))
	require.NotContains(t, p.ReadableRoots, filepath.Join(b.homeDir, ".zshrc"))
	require.NotContains(t, p.ReadableRoots, filepath.Join(b.homeDir, ".profile"))
	require.NotContains(t, p.ReadableRoots, filepath.Join(b.homeDir, ".cache"))
	require.NotContains(t, p.ReadableRoots, filepath.Join(b.homeDir, ".local"))
	require.NotContains(t, p.ReadableRoots, filepath.Join(b.homeDir, "Library", "Caches"))
}

// A re-opened toolchain directory must not carry its credential file back in.
// DenyRead is emitted last, so it wins over the re-allow.
func TestBuildKeepsCredentialsDeniedInsideReAllowedToolchains(t *testing.T) {
	b, ws := builderFixture(t)
	p, err := b.Build(ModeAuto, ws)
	require.NoError(t, err)

	require.Contains(t, p.ReadableRoots, filepath.Join(b.homeDir, ".cargo"))
	require.Contains(t, p.DenyRead, filepath.Join(b.homeDir, ".cargo", "credentials.toml"))
	require.Contains(t, p.DenyRead, filepath.Join(b.homeDir, "Library", "Keychains"))
	require.Contains(t, p.DenyRead, filepath.Join(b.homeDir, ".netrc"))
}

func TestBuildDoesNotDenyWorkspaceRoot(t *testing.T) {
	b, ws := builderFixture(t)
	p, err := b.Build(ModeAuto, ws)
	require.NoError(t, err)

	for _, deny := range p.DenyRead {
		require.False(t, PathUnder(ws.Root, deny),
			"workspace %q must not be covered by deny-read %q", ws.Root, deny)
	}
}

func TestBuildIncludesPlatformRuntimeReadRoots(t *testing.T) {
	b, ws := builderFixture(t)
	p, err := b.Build(ModeAuto, ws)
	require.NoError(t, err)
	require.Contains(t, p.ReadableRoots, "/usr/lib")
}

func TestRelaxAddsWritePathAndNetwork(t *testing.T) {
	b, ws := builderFixture(t)
	base, err := b.Build(ModeAuto, ws)
	require.NoError(t, err)

	extra := filepath.Join(b.homeDir, "Other Dir")
	relaxed, err := b.Relax(base, Grant{WritePath: extra, AllowNetwork: true})
	require.NoError(t, err)

	var roots []string
	for _, r := range relaxed.WritableRoots {
		roots = append(roots, r.Path)
	}
	require.Contains(t, roots, extra)
	require.Equal(t, NetworkUnrestricted, relaxed.Network)
	require.NotEqual(t, base.Fingerprint(), relaxed.Fingerprint())
}

// Relaxation must never be able to unlock a denied credential directory.
func TestRelaxRefusesToGrantDeniedPath(t *testing.T) {
	b, ws := builderFixture(t)
	base, err := b.Build(ModeAuto, ws)
	require.NoError(t, err)

	_, err = b.Relax(base, Grant{WritePath: filepath.Join(b.homeDir, ".ssh")})
	require.Error(t, err)
}

func TestBuildRefusesHomeAsWorkspace(t *testing.T) {
	b, ws := builderFixture(t)
	ws.Root = b.homeDir
	ws.ProtectGit = true

	_, err := b.Build(ModeAuto, ws)
	require.ErrorIs(t, err, ErrWorkspaceTooBroad)
}

func TestBuildRefusesUsersAsWorkspace(t *testing.T) {
	if !filepath.IsAbs("/Users") {
		t.Skip("not a unix path layout")
	}
	b, _ := builderFixture(t)
	_, err := b.Build(ModeAuto, Workspace{Kind: WorkspaceProject, Root: "/Users"})
	require.ErrorIs(t, err, ErrWorkspaceTooBroad)
}

func TestBuildRefusesHomeLibraryAsWorkspace(t *testing.T) {
	b, ws := builderFixture(t)
	ws.Root = filepath.Join(b.homeDir, "Library")
	_, err := b.Build(ModeAuto, ws)
	require.ErrorIs(t, err, ErrWorkspaceTooBroad)
}

func TestBuildMakesOtherUsersAndVolumesPrivate(t *testing.T) {
	b, ws := builderFixture(t)
	p, err := b.Build(ModeAuto, ws)
	require.NoError(t, err)
	if filepath.IsAbs("/Users") {
		require.Contains(t, p.PrivateRoots, "/Users")
	}
	if filepath.IsAbs("/Volumes") {
		require.Contains(t, p.PrivateRoots, "/Volumes")
	}
}

func TestBuildMakesTempDirsPrivate(t *testing.T) {
	b, ws := builderFixture(t)
	p, err := b.Build(ModeAuto, ws)
	require.NoError(t, err)
	for _, root := range []string{"/tmp", "/private/tmp", "/var", "/private/var"} {
		if !filepath.IsAbs(root) {
			continue
		}
		require.Contains(t, p.PrivateRoots, filepath.Clean(root), root)
	}
}

func TestBuildRefusesPrivateVarAsWorkspace(t *testing.T) {
	if !filepath.IsAbs("/private/var") {
		t.Skip("not a unix path layout")
	}
	b, _ := builderFixture(t)
	_, err := b.Build(ModeAuto, Workspace{Kind: WorkspaceProject, Root: "/private/var"})
	require.ErrorIs(t, err, ErrWorkspaceTooBroad)
}

func TestBuildRefusesVarFoldersAsWorkspace(t *testing.T) {
	if !filepath.IsAbs("/var/folders") {
		t.Skip("not a unix path layout")
	}
	b, _ := builderFixture(t)
	_, err := b.Build(ModeAuto, Workspace{Kind: WorkspaceProject, Root: "/var/folders"})
	require.ErrorIs(t, err, ErrWorkspaceTooBroad)
}

func TestBuildRefusesSystemVolumesDataAsWorkspace(t *testing.T) {
	if !filepath.IsAbs("/System/Volumes/Data") {
		t.Skip("not a unix path layout")
	}
	b, _ := builderFixture(t)
	_, err := b.Build(ModeAuto, Workspace{Kind: WorkspaceProject, Root: "/System/Volumes/Data"})
	require.ErrorIs(t, err, ErrWorkspaceTooBroad)
}

func TestBuildRefusesHomeLibraryDescendantAsWorkspace(t *testing.T) {
	b, ws := builderFixture(t)
	ws.Root = filepath.Join(b.homeDir, "Library", "Application Support")
	_, err := b.Build(ModeAuto, ws)
	require.ErrorIs(t, err, ErrWorkspaceTooBroad)
}

func TestRelaxRefusesHomeWriteGrant(t *testing.T) {
	b, ws := builderFixture(t)
	base, err := b.Build(ModeAuto, ws)
	require.NoError(t, err)

	_, err = b.Relax(base, Grant{WritePath: b.homeDir})
	require.ErrorIs(t, err, ErrWorkspaceTooBroad)
}

func TestRelaxRefusesHomeLibraryDescendant(t *testing.T) {
	b, ws := builderFixture(t)
	base, err := b.Build(ModeAuto, ws)
	require.NoError(t, err)

	_, err = b.Relax(base, Grant{WritePath: filepath.Join(b.homeDir, "Library", "Application Support")})
	require.ErrorIs(t, err, ErrWorkspaceTooBroad)
}

func TestBuildRefusesWorkspaceInsideAppData(t *testing.T) {
	b, _ := builderFixture(t)
	ws := Workspace{
		Kind: WorkspaceSession,
		Root: filepath.Join(b.appDataDir, "sessions", "s1"),
	}
	_, err := b.Build(ModeAuto, ws)
	require.ErrorIs(t, err, ErrDenyReadCoversRoot)
}

func TestRelaxRefusesToGrantAppData(t *testing.T) {
	b, ws := builderFixture(t)
	base, err := b.Build(ModeAuto, ws)
	require.NoError(t, err)

	_, err = b.Relax(base, Grant{WritePath: b.appDataDir})
	require.Error(t, err)
}
