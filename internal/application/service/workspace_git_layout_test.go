package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/stretchr/testify/require"
)

func TestSessionGitDirLivesOutsideWorkspace(t *testing.T) {
	require.True(t, strings.HasPrefix(sandbox.SessionGitDir, "/"))
	require.NotEqual(t, sandbox.SessionWorkspaceRoot, sandbox.SessionGitDir)
	require.False(t,
		strings.HasPrefix(sandbox.SessionGitDir, sandbox.SessionWorkspaceRoot+"/"),
		"checkpoint git dir %q must not live under %s",
		sandbox.SessionGitDir, sandbox.SessionWorkspaceRoot)
}

func TestCheckpointScriptUsesExternalGitDir(t *testing.T) {
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{
		ExitCode: 0, Stdout: strings.Repeat("a", 40) + "\n",
	}}
	NewWorkspaceCheckpointer(runner).Checkpoint(context.Background(), "s1", "sbx-1", "msg-1")

	require.Len(t, runner.calls, 1)
	assertWorkspaceGitLayout(t, runner.calls[0])
	require.NotContains(t, runner.calls[0], "git init -q "+sandbox.SessionWorkspaceRoot)
	require.NotContains(t, runner.calls[0], "git -C "+sandbox.SessionWorkspaceRoot)
}

func TestForkAndRewindResetScriptUsesExternalGitDir(t *testing.T) {
	script, err := workspaceResetScript(
		sandbox.SessionWorkspaceRoot, sandbox.SessionGitDir, strings.Repeat("a", 40),
	)
	require.NoError(t, err)
	assertWorkspaceGitLayout(t, script)
	require.NotContains(t, script, sandbox.SessionWorkspaceRoot+"/.git")
	require.NotContains(t, script, "git -C "+sandbox.SessionWorkspaceRoot)
}

func TestCheckpointInitLeavesGitDirOutsideWorkTree(t *testing.T) {
	requireGit(t)
	workspace, gitDir, env := splitGitDirs(t)

	require.NoError(t, os.WriteFile(filepath.Join(workspace, "keep.txt"), []byte("v1"), 0o644))
	runWorkspaceGitScript(t, env, checkpointScript(workspace, gitDir, "msg-1"))

	_, err := os.Stat(filepath.Join(workspace, ".git"))
	require.True(t, os.IsNotExist(err), "work tree must not grow a .git directory")
	require.FileExists(t, filepath.Join(gitDir, "HEAD"))
	body, err := os.ReadFile(filepath.Join(workspace, "keep.txt"))
	require.NoError(t, err)
	require.Equal(t, "v1", string(body))
}

func TestCheckpointMigratesLegacyInTreeRepo(t *testing.T) {
	requireGit(t)
	workspace, gitDir, env := splitGitDirs(t)

	runGitAt(t, env, workspace, "init", "-q")
	runGitAt(t, env, workspace, "config", "user.email", "agent@weknora.local")
	runGitAt(t, env, workspace, "config", "user.name", "WeKnora Agent")
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "keep.txt"), []byte("legacy"), 0o644))
	runGitAt(t, env, workspace, "add", "-A")
	runGitAt(t, env, workspace, "commit", "-q", "-m", "legacy")
	oldSHA := strings.TrimSpace(runGitAt(t, env, workspace, "rev-parse", "HEAD"))

	runWorkspaceGitScript(t, env, checkpointScript(workspace, gitDir, "msg-2"))

	_, err := os.Stat(filepath.Join(workspace, ".git"))
	require.True(t, os.IsNotExist(err), "legacy .git must move out of the work tree")
	require.FileExists(t, filepath.Join(gitDir, "HEAD"))
	got := strings.TrimSpace(runGitSplit(t, env, workspace, gitDir, "rev-parse", "HEAD"))
	require.NotEqual(t, oldSHA, got, "migrated repo should still accept a new checkpoint commit")
	cat := runGitSplit(t, env, workspace, gitDir, "cat-file", "-t", oldSHA)
	require.Equal(t, "commit", strings.TrimSpace(cat), "pre-migration commits must remain reachable")
}

func TestResetPrunesLaterCommitsFromExternalGitDir(t *testing.T) {
	requireGit(t)
	workspace, gitDir, env := splitGitDirs(t)

	runGitSplit(t, env, workspace, gitDir, "init", "-q")
	runGitSplit(t, env, workspace, gitDir, "config", "user.email", "agent@weknora.local")
	runGitSplit(t, env, workspace, gitDir, "config", "user.name", "WeKnora Agent")
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "keep.txt"), []byte("early"), 0o644))
	runGitSplit(t, env, workspace, gitDir, "add", "-A")
	runGitSplit(t, env, workspace, gitDir, "commit", "-q", "-m", "early")
	early := strings.TrimSpace(runGitSplit(t, env, workspace, gitDir, "rev-parse", "HEAD"))
	if !gitSHAPattern.MatchString(early) {
		t.Skip("host git is not using SHA-1 object names")
	}

	require.NoError(t, os.WriteFile(filepath.Join(workspace, "secret.txt"), []byte("later-secret"), 0o644))
	runGitSplit(t, env, workspace, gitDir, "add", "-A")
	runGitSplit(t, env, workspace, gitDir, "commit", "-q", "-m", "later")
	later := strings.TrimSpace(runGitSplit(t, env, workspace, gitDir, "rev-parse", "HEAD"))
	require.NotEqual(t, early, later)

	script, err := workspaceResetScript(workspace, gitDir, early)
	require.NoError(t, err)
	runWorkspaceGitScript(t, env, script)

	cmd := exec.Command("git", "--git-dir", gitDir, "--work-tree", workspace, "cat-file", "-t", later)
	cmd.Env = env
	require.Error(t, cmd.Run(), "later-turn commit must be unreachable after prune")
	_, err = os.Stat(filepath.Join(workspace, "secret.txt"))
	require.True(t, os.IsNotExist(err))
	body, err := os.ReadFile(filepath.Join(workspace, "keep.txt"))
	require.NoError(t, err)
	require.Equal(t, "early", string(body))
	_, err = os.Stat(filepath.Join(workspace, ".git"))
	require.True(t, os.IsNotExist(err))
}

func TestCheckpointSurvivesWipedWorkTree(t *testing.T) {
	requireGit(t)
	workspace, gitDir, env := splitGitDirs(t)

	require.NoError(t, os.WriteFile(filepath.Join(workspace, "keep.txt"), []byte("v1"), 0o644))
	runWorkspaceGitScript(t, env, checkpointScript(workspace, gitDir, "msg-1"))
	first := strings.TrimSpace(runGitSplit(t, env, workspace, gitDir, "rev-parse", "HEAD"))

	require.NoError(t, os.RemoveAll(workspace))
	_, err := os.Stat(filepath.Join(workspace, ".git"))
	require.True(t, os.IsNotExist(err))

	runWorkspaceGitScript(t, env, checkpointScript(workspace, gitDir, "msg-2"))

	require.DirExists(t, workspace)
	_, err = os.Stat(filepath.Join(workspace, ".git"))
	require.True(t, os.IsNotExist(err))
	got := strings.TrimSpace(runGitSplit(t, env, workspace, gitDir, "rev-parse", "HEAD"))
	require.NotEqual(t, first, got)
	require.Equal(t, "commit", strings.TrimSpace(
		runGitSplit(t, env, workspace, gitDir, "cat-file", "-t", first),
	), "wiping /workspace must not drop earlier checkpoint objects")
}

func TestResetMigratesLegacyInTreeRepoThenPrunes(t *testing.T) {
	requireGit(t)
	workspace, gitDir, env := splitGitDirs(t)

	runGitAt(t, env, workspace, "init", "-q")
	runGitAt(t, env, workspace, "config", "user.email", "agent@weknora.local")
	runGitAt(t, env, workspace, "config", "user.name", "WeKnora Agent")
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "keep.txt"), []byte("early"), 0o644))
	runGitAt(t, env, workspace, "add", "-A")
	runGitAt(t, env, workspace, "commit", "-q", "-m", "early")
	early := strings.TrimSpace(runGitAt(t, env, workspace, "rev-parse", "HEAD"))
	if !gitSHAPattern.MatchString(early) {
		t.Skip("host git is not using SHA-1 object names")
	}
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "secret.txt"), []byte("later-secret"), 0o644))
	runGitAt(t, env, workspace, "add", "-A")
	runGitAt(t, env, workspace, "commit", "-q", "-m", "later")

	script, err := workspaceResetScript(workspace, gitDir, early)
	require.NoError(t, err)
	runWorkspaceGitScript(t, env, script)

	_, err = os.Stat(filepath.Join(workspace, ".git"))
	require.True(t, os.IsNotExist(err), "fork/rewind of a pre-migration sandbox must move .git out")
	require.FileExists(t, filepath.Join(gitDir, "HEAD"))
	_, err = os.Stat(filepath.Join(workspace, "secret.txt"))
	require.True(t, os.IsNotExist(err))
}

func assertWorkspaceGitLayout(t *testing.T, script string) {
	t.Helper()
	require.Contains(t, script, "--git-dir=")
	require.Contains(t, script, sandbox.SessionGitDir)
	require.Contains(t, script, "--work-tree=")
	require.Contains(t, script, sandbox.SessionWorkspaceRoot)
	require.NotContains(t, script, sandbox.SessionWorkspaceRoot+"/.git")
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

func splitGitDirs(t *testing.T) (workspace, gitDir string, env []string) {
	t.Helper()
	workspace = filepath.Join(t.TempDir(), "workspace")
	gitDir = filepath.Join(t.TempDir(), "workspace.git")
	require.NoError(t, os.MkdirAll(workspace, 0o755))
	return workspace, gitDir, isolatedGitEnv(t)
}

func runWorkspaceGitScript(t *testing.T, env []string, script string) {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

func runGitAt(t *testing.T, env []string, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return string(out)
}

func runGitSplit(t *testing.T, env []string, workspace, gitDir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{
		"--git-dir", gitDir, "--work-tree", workspace,
	}, args...)...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return string(out)
}
