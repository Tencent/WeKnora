package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRelaxFileGrantRefusesATargetSwappedForASymlink(t *testing.T) {
	env := proposeFixture(t)
	granted := filepath.Join(env.other, "a.txt")
	secret := filepath.Join(env.other, "secret.txt")
	require.NoError(t, os.WriteFile(granted, []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(secret, []byte("s"), 0o644))
	grant, ok := env.b.ProposeFileGrant(env.p, granted)
	require.True(t, ok)

	require.NoError(t, os.Remove(granted))
	require.NoError(t, os.Symlink(secret, granted))

	_, err := env.b.Relax(env.p, grant)
	require.Error(t, err, "the approval named a.txt, not whatever it points to now")
}

func TestGrantsRefuseWhatRunsOutsideTheSandbox(t *testing.T) {
	env := proposeFixture(t)
	for _, path := range []string{
		"/opt",
		"/opt/homebrew/bin",
		"/Applications",
		"/Applications/Tool.app/Contents/MacOS",
		filepath.Join(env.home, "bin"),
		filepath.Join(env.home, "go", "bin"),
		filepath.Join(env.home, "Applications"),
		filepath.Join(env.other, "repo.git"),
		filepath.Join(env.other, "repo.git", "hooks"),
		filepath.Join(env.other, "REPO.GIT", "hooks"),
	} {
		_, err := env.b.Relax(env.p, Grant{Path: path, Access: AccessWrite})
		require.Error(t, err, path)
		_, err = env.b.Relax(env.p, Grant{Path: path, Access: AccessRead})
		require.Error(t, err, path)
		_, err = env.b.Relax(env.p, Grant{Path: filepath.Join(path, "tool"), Access: AccessWrite, File: true})
		require.Error(t, err, path)
	}
}

func TestDirectoryGrantRefusesWhatCoversAProgramDirectory(t *testing.T) {
	env := proposeFixture(t)
	_, err := env.b.Relax(env.p, Grant{Path: filepath.Join(env.home, "go"), Access: AccessWrite})
	require.Error(t, err)
}

func TestGrantsKeepOrdinaryPathsBesideRefusedOnes(t *testing.T) {
	env := proposeFixture(t)
	for _, path := range []string{
		filepath.Join(env.home, "go", "src", "project"),
		filepath.Join(env.home, "Documents"),
		filepath.Join(env.other, "repo.github"),
	} {
		require.NoError(t, os.MkdirAll(path, 0o755))
		_, err := env.b.Relax(env.p, Grant{Path: path, Access: AccessWrite})
		require.NoError(t, err, path)
	}
}

func TestWorkspaceMayStillLiveWhereAGrantMayNot(t *testing.T) {
	env := proposeFixture(t)
	root := filepath.Join(env.other, "mirror.git")
	require.NoError(t, os.MkdirAll(root, 0o755))
	_, err := env.b.Build(ModeAsk, Workspace{Kind: WorkspaceProject, Root: root})
	require.NoError(t, err)
}

func TestDirectoryGrantKeepsBareRepositoryMetadataReadOnly(t *testing.T) {
	env := proposeFixture(t)
	relaxed, err := env.b.Relax(env.p, Grant{Path: env.other, Access: AccessWrite})
	require.NoError(t, err)
	guard := NewPathGuard(relaxed)

	for _, path := range []string{
		filepath.Join(env.other, "repo.git", "hooks", "post-update"),
		filepath.Join(env.other, "repo.git", "config"),
		filepath.Join(env.other, "sub", "REPO.GIT", "hooks", "x"),
	} {
		_, err := guard.CheckWrite(path)
		require.ErrorIs(t, err, ErrPathDenied, path)
		require.ErrorIs(t, guard.WriteFile(path, []byte("x"), 0o755), ErrPathDenied, path)
	}
	_, err = guard.CheckWrite(filepath.Join(env.other, "repo.git", "objects", "ab"))
	require.NoError(t, err)
	_, err = guard.CheckWrite(filepath.Join(env.other, "hooks", "x"))
	require.NoError(t, err)
}

func TestFingerprintCoversBareRepositoryProtection(t *testing.T) {
	root := "/w"
	a := Policy{WritableRoots: []WritableRoot{{Path: root}}}
	b := Policy{WritableRoots: []WritableRoot{{Path: root, ProtectBareGitDirs: true}}}
	require.NotEqual(t, a.Fingerprint(), b.Fingerprint())
}

func TestBuildCommandEnvDropsWhatMakesAnInterpreterRunCode(t *testing.T) {
	blocked := []string{
		"BASH_ENV", "ENV", "ZDOTDIR", "SHELLOPTS", "BASHOPTS", "PS4", "PROMPT_COMMAND",
		"BASH_FUNC_ls%%", "DYLD_FALLBACK_LIBRARY_PATH", "DYLD_INSERT_LIBRARIES", "LD_AUDIT", "LD_PRELOAD",
		"PERL5OPT", "PERL5LIB", "PYTHONSTARTUP", "PYTHONINSPECT", "NODE_OPTIONS", "RUBYOPT", "RUBYLIB",
	}
	explicit := map[string]string{"AWS_SECRET_ACCESS_KEY": "kept", "PYTHONPATH": "kept"}
	for _, name := range blocked {
		explicit[name] = "./evil"
	}
	out := BuildCommandEnv(explicit, nil)
	for _, name := range blocked {
		require.NotContains(t, out, name)
	}
	require.Equal(t, "kept", out["AWS_SECRET_ACCESS_KEY"])
	require.Equal(t, "kept", out["PYTHONPATH"], "shown on the card, not blocked")
}

func TestScriptPathsNamesWhatACommandRuns(t *testing.T) {
	for command, want := range map[string][]string{
		"python3 x.py a b":           {"x.py"},
		"python3 -u tools/x.py":      {"tools/x.py"},
		"bash ./run.sh && echo ok":   {"./run.sh"},
		"./run.sh --fast":            {"./run.sh"},
		"source env.sh; node app.js": {"env.sh", "app.js"},
		". env.sh":                   {"env.sh"},
		"env A=1 ruby x.rb":          {"x.rb"},
		"bash -c 'python3 inner.py'": {"inner.py"},
		"python3 -c 'print(1)'":      nil,
		"python3 -m http.server":     nil,
		"node -e 'x'":                nil,
		"ls a.py":                    nil,
		"cat run.sh | grep x":        nil,
		"bash":                       nil,
		"python3 ~/x.py":             {"/h/x.py"},
		"bash $SCRIPT":               nil,
	} {
		require.Equal(t, want, ScriptPaths(command, "/h"), command)
	}
}
