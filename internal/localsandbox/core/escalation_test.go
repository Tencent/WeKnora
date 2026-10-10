package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

type proposeEnv struct {
	b     *PolicyBuilder
	p     Policy
	home  string
	root  string
	other string
}

func proposeFixture(t *testing.T) proposeEnv {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	home := filepath.Join(base, "home")
	root := filepath.Join(home, "proj")
	other := filepath.Join(base, "other")
	for _, dir := range []string{root, other, filepath.Join(home, ".ssh"), filepath.Join(home, ".nvm")} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}
	b := NewPolicyBuilder(home, filepath.Join(home, "AppData"))
	p, err := b.Build(ModeAuto, Workspace{Kind: WorkspaceProject, Root: root})
	require.NoError(t, err)
	return proposeEnv{b: b, p: p, home: home, root: root, other: other}
}

func denialAt(path string) Denial {
	return Denial{Reason: DenialOperationNotPermitted, Path: path}
}

func TestProposeGrantOffersExistingDirOutsidePolicy(t *testing.T) {
	env := proposeFixture(t)
	g, ok := env.b.ProposeGrant(env.p, denialAt(filepath.Join(env.other, "a.txt")), env.root)
	require.True(t, ok)
	require.Equal(t, Grant{Path: env.other, Access: AccessWrite}, g)
}

func TestProposeGrantDoesNotWalkUpPastAMissingDirectory(t *testing.T) {
	env := proposeFixture(t)
	_, ok := env.b.ProposeGrant(env.p, denialAt(filepath.Join(env.other, "new", "deep", "f")), env.root)
	require.False(t, ok)
}

func TestProposeGrantRejectsTheApplicationTree(t *testing.T) {
	env := proposeFixture(t)
	app := filepath.Join(env.home, ".weknora")
	skills := filepath.Join(app, "skills", "pdf")
	otherSession := filepath.Join(app, "sessions", "2026", "10", "08", "session-other")
	desktop := filepath.Join(env.home, "Desktop")
	for _, dir := range []string{skills, otherSession, desktop} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}
	env.b.WithSkillsRoot(filepath.Join(app, "skills")).WithSessionRoot(filepath.Join(app, "sessions"))

	for _, path := range []string{
		filepath.Join(app, "notes.txt"),
		filepath.Join(skills, "run.py"),
		filepath.Join(otherSession, "a.txt"),
		filepath.Join(app, "sessions", "2026", "a.txt"),
	} {
		_, ok := env.b.ProposeGrant(env.p, denialAt(path), env.root)
		require.False(t, ok, path)
	}
	for _, path := range []string{app, filepath.Join(app, "skills"), otherSession} {
		_, err := env.b.Relax(env.p, Grant{Path: path, Access: AccessWrite})
		require.Error(t, err, path)
	}

	g, ok := env.b.ProposeGrant(env.p, denialAt(filepath.Join(desktop, "a.txt")), env.root)
	require.True(t, ok)
	require.Equal(t, desktop, g.Path)
}

func TestProposeGrantResolvesRelativePathFromCwd(t *testing.T) {
	env := proposeFixture(t)
	g, ok := env.b.ProposeGrant(env.p, denialAt("a.txt"), env.other)
	require.True(t, ok)
	require.Equal(t, env.other, g.Path)
}

func TestProposeGrantOffersWriteForReadableDir(t *testing.T) {
	env := proposeFixture(t)
	nvm := filepath.Join(env.home, ".nvm")
	g, ok := env.b.ProposeGrant(env.p, denialAt(filepath.Join(nvm, "x")), env.root)
	require.True(t, ok)
	require.Equal(t, Grant{Path: nvm, Access: AccessWrite}, g)
}

// Allowed by the policy means the failure was not the sandbox.
func TestProposeGrantSkipsPathInsideWorkspace(t *testing.T) {
	env := proposeFixture(t)
	_, ok := env.b.ProposeGrant(env.p, denialAt(filepath.Join(env.root, "a.txt")), env.root)
	require.False(t, ok)
}

func TestProposeGrantSkipsCredentialDir(t *testing.T) {
	env := proposeFixture(t)
	_, ok := env.b.ProposeGrant(env.p, denialAt(filepath.Join(env.home, ".ssh", "id_rsa")), env.root)
	require.False(t, ok)
}

func TestProposeGrantSkipsHomeItself(t *testing.T) {
	env := proposeFixture(t)
	_, ok := env.b.ProposeGrant(env.p, denialAt(filepath.Join(env.home, "notes.txt")), env.root)
	require.False(t, ok)
}

func TestProposeGrantSkipsSystemDirs(t *testing.T) {
	env := proposeFixture(t)
	_, ok := env.b.ProposeGrant(env.p, denialAt("/etc/hosts"), env.root)
	require.False(t, ok)
}

func TestProposeGrantResolvesSymlinkToTheRealDirectory(t *testing.T) {
	env := proposeFixture(t)
	link := filepath.Join(env.root, "link")
	require.NoError(t, os.Symlink(env.other, link))

	g, ok := env.b.ProposeGrant(env.p, denialAt(filepath.Join(link, "a.txt")), env.root)
	require.True(t, ok)
	require.Equal(t, Grant{Path: env.other, Access: AccessWrite}, g)
}

func TestProposeGrantRejectsSymlinkToHome(t *testing.T) {
	env := proposeFixture(t)
	link := filepath.Join(env.root, "link")
	require.NoError(t, os.Symlink(env.home, link))

	_, ok := env.b.ProposeGrant(env.p, denialAt(filepath.Join(link, "x")), env.root)
	require.False(t, ok)
}

func TestRelaxResolvesSymlinkAndRejectsRetargetToHome(t *testing.T) {
	env := proposeFixture(t)
	link := filepath.Join(env.root, "link")
	require.NoError(t, os.Symlink(env.other, link))

	relaxed, err := env.b.Relax(env.p, Grant{Path: link, Access: AccessWrite})
	require.NoError(t, err)
	var roots []string
	for _, r := range relaxed.WritableRoots {
		roots = append(roots, r.Path)
	}
	require.Contains(t, roots, env.other)
	require.NotContains(t, roots, link)

	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Symlink(env.home, link))
	_, err = env.b.Relax(env.p, Grant{Path: link, Access: AccessWrite})
	require.Error(t, err)
}

func writableRootAt(t *testing.T, p Policy, path string) WritableRoot {
	t.Helper()
	for _, r := range p.WritableRoots {
		if r.Path == path {
			return r
		}
	}
	t.Fatalf("no writable root at %s", path)
	return WritableRoot{}
}

// A hook written into a granted repository runs with the user's own rights
// the next time they use git there, outside the sandbox.
func TestRelaxWriteGrantKeepsGitReadOnly(t *testing.T) {
	env := proposeFixture(t)
	gitDir := filepath.Join(env.other, ".git")
	require.NoError(t, os.MkdirAll(filepath.Join(gitDir, "hooks"), 0o755))

	relaxed, err := env.b.Relax(env.p, Grant{Path: env.other, Access: AccessWrite})
	require.NoError(t, err)
	require.Equal(t, []string{gitDir}, writableRootAt(t, relaxed, env.other).ReadOnlySubpaths)

	guard := NewPathGuard(relaxed)
	_, err = guard.CheckWrite(filepath.Join(gitDir, "hooks", "pre-commit"))
	require.ErrorIs(t, err, ErrPathDenied)
	_, err = guard.CheckWrite(filepath.Join(env.other, "a.txt"))
	require.NoError(t, err)
}

// Without this, git init in the granted directory creates a repository whose
// hooks the sandbox may write.
func TestRelaxWriteGrantProtectsAMissingGitDir(t *testing.T) {
	env := proposeFixture(t)

	relaxed, err := env.b.Relax(env.p, Grant{Path: env.other, Access: AccessWrite})
	require.NoError(t, err)
	require.Equal(t,
		[]string{filepath.Join(env.other, ".git")},
		writableRootAt(t, relaxed, env.other).ReadOnlySubpaths)
}

func TestRelaxWriteGrantProtectsTheGitdirAGitFileNames(t *testing.T) {
	env := proposeFixture(t)
	gitdir := filepath.Join(env.other, ".repo", "worktrees", "w1")
	require.NoError(t, os.MkdirAll(gitdir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(env.other, ".git"),
		[]byte("gitdir: .repo/worktrees/w1\n"), 0o644))

	relaxed, err := env.b.Relax(env.p, Grant{Path: env.other, Access: AccessWrite})
	require.NoError(t, err)
	require.ElementsMatch(t,
		[]string{filepath.Join(env.other, ".git"), gitdir},
		writableRootAt(t, relaxed, env.other).ReadOnlySubpaths)

	_, err = NewPathGuard(relaxed).CheckWrite(filepath.Join(gitdir, "hooks", "pre-commit"))
	require.ErrorIs(t, err, ErrPathDenied)
}

func protectedProjectFixture(t *testing.T) proposeEnv {
	t.Helper()
	env := proposeFixture(t)
	env.root = filepath.Join(env.home, "Documents", "proj")
	require.NoError(t, os.MkdirAll(filepath.Join(env.root, ".git", "hooks"), 0o755))
	p, err := env.b.Build(ModeAuto, Workspace{Kind: WorkspaceProject, Root: env.root, ProtectGit: true})
	require.NoError(t, err)
	env.p = p
	return env
}

// A card must never offer a directory inside a repository's metadata: the
// hooks there run outside the sandbox the next time the user runs git.
func TestProposeGrantRefusesTheWorkspaceGitDir(t *testing.T) {
	env := protectedProjectFixture(t)
	hook := filepath.Join(env.root, ".git", "hooks", "pre-commit")

	_, ok := env.b.ProposeGrant(env.p, denialAt(hook), env.root)
	require.False(t, ok)
	_, err := env.b.Relax(env.p, Grant{Path: filepath.Join(env.root, ".git", "hooks"), Access: AccessWrite})
	require.Error(t, err)
}

func TestRelaxRefusesAGitDirOutsideTheWorkspace(t *testing.T) {
	env := proposeFixture(t)
	for _, dir := range []string{
		filepath.Join(env.other, ".git"),
		filepath.Join(env.other, ".git", "hooks"),
		filepath.Join(env.other, "sub", ".GIT", "hooks"),
	} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
		_, err := env.b.Relax(env.p, Grant{Path: dir, Access: AccessWrite})
		require.Error(t, err, dir)
		_, ok := env.b.ProposeGrant(env.p, denialAt(filepath.Join(dir, "x")), env.root)
		require.False(t, ok, dir)
	}
}

// Granting the folder that holds the project must not reopen the project's
// own .git, whichever writable root the guard looks at first.
func TestWriteGrantCoveringTheWorkspaceKeepsItsGitReadOnly(t *testing.T) {
	env := protectedProjectFixture(t)
	parent := filepath.Dir(env.root)

	relaxed, err := env.b.Relax(env.p, Grant{Path: parent, Access: AccessWrite})
	require.NoError(t, err)
	reversed := relaxed
	reversed.WritableRoots = []WritableRoot{relaxed.WritableRoots[1], relaxed.WritableRoots[0]}

	hook := filepath.Join(env.root, ".git", "hooks", "pre-commit")
	for _, p := range []Policy{relaxed, reversed} {
		guard := NewPathGuard(p)
		_, err = guard.CheckWrite(hook)
		require.ErrorIs(t, err, ErrPathDenied)
		require.ErrorIs(t, guard.WriteFile(hook, []byte("#!/bin/sh\n"), 0o755), ErrPathDenied)
		_, err = guard.CheckWrite(filepath.Join(parent, "notes.txt"))
		require.NoError(t, err)
	}
}

// A granted folder may hold other repositories. None of their .git
// directories become writable, including ones created after the grant.
func TestWriteGrantKeepsNestedGitDirsReadOnly(t *testing.T) {
	env := proposeFixture(t)
	nested := filepath.Join(env.other, "code", "app")
	require.NoError(t, os.MkdirAll(filepath.Join(nested, ".git", "hooks"), 0o755))

	relaxed, err := env.b.Relax(env.p, Grant{Path: env.other, Access: AccessWrite})
	require.NoError(t, err)
	require.True(t, writableRootAt(t, relaxed, env.other).ProtectGitDirs)

	guard := NewPathGuard(relaxed)
	for _, path := range []string{
		filepath.Join(nested, ".git", "hooks", "pre-commit"),
		filepath.Join(nested, ".git"),
		filepath.Join(env.other, "later", ".git", "config"),
		filepath.Join(env.other, "later", ".GIT", "config"),
	} {
		_, err = guard.CheckWrite(path)
		require.ErrorIs(t, err, ErrPathDenied, path)
	}
	_, err = guard.CheckWrite(filepath.Join(nested, "main.go"))
	require.NoError(t, err)
	_, err = guard.CheckWrite(filepath.Join(nested, ".gitignore"))
	require.NoError(t, err)
}

func TestProposeGrantNeedsADeniedPath(t *testing.T) {
	env := proposeFixture(t)
	_, ok := env.b.ProposeGrant(env.p, Denial{Reason: DenialOperationNotPermitted}, env.root)
	require.False(t, ok)
	_, ok = env.b.ProposeGrant(env.p, Denial{Path: filepath.Join(env.other, "a")}, env.root)
	require.False(t, ok)
}
