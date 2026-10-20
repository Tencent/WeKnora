package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestCheckpointKeepsAgentGitOnceWeKnoraRepoExists(t *testing.T) {
	requireGit(t)
	workspace, gitDir, env := splitGitDirs(t)

	require.NoError(t, os.WriteFile(filepath.Join(workspace, "keep.txt"), []byte("v1"), 0o644))
	runWorkspaceGitScript(t, env, checkpointScript(workspace, gitDir, "msg-1"))

	runGitAt(t, env, workspace, "init", "-q")
	runGitAt(t, env, workspace, "config", "user.email", "dev@example.com")
	runGitAt(t, env, workspace, "config", "user.name", "Dev")
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "app.txt"), []byte("mine"), 0o644))
	runGitAt(t, env, workspace, "add", "-A")
	runGitAt(t, env, workspace, "commit", "-q", "-m", "agent")
	agentHEAD := strings.TrimSpace(runGitAt(t, env, workspace, "rev-parse", "HEAD"))

	runWorkspaceGitScript(t, env, checkpointScript(workspace, gitDir, "msg-2"))

	require.DirExists(t, filepath.Join(workspace, ".git"))
	gotAgent := strings.TrimSpace(runGitAt(t, env, workspace, "rev-parse", "HEAD"))
	require.Equal(t, agentHEAD, gotAgent, "WeKnora must not delete or rewrite the agent's work-tree git")
	require.FileExists(t, filepath.Join(gitDir, "HEAD"))
	ls := runGitSplit(t, env, workspace, gitDir, "ls-files")
	require.NotContains(t, ls, ".git/", "checkpoint must not ingest the agent's .git as tracked files")
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

func TestEmptyResetWipesWorkTreeAndReinitializesGitDir(t *testing.T) {
	requireGit(t)
	workspace, gitDir, env := splitGitDirs(t)

	runGitSplit(t, env, workspace, gitDir, "init", "-q")
	runGitSplit(t, env, workspace, gitDir, "config", "user.email", "agent@weknora.local")
	runGitSplit(t, env, workspace, gitDir, "config", "user.name", "WeKnora Agent")
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "keep.txt"), []byte("early"), 0o644))
	runGitSplit(t, env, workspace, gitDir, "add", "-A")
	runGitSplit(t, env, workspace, gitDir, "commit", "-q", "-m", "early")
	early := strings.TrimSpace(runGitSplit(t, env, workspace, gitDir, "rev-parse", "HEAD"))
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "untracked.txt"), []byte("tmp"), 0o644))

	runWorkspaceGitScript(t, env, workspaceEmptyResetScript(workspace, gitDir))

	_, err := os.Stat(filepath.Join(workspace, "keep.txt"))
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(workspace, "untracked.txt"))
	require.True(t, os.IsNotExist(err))
	require.FileExists(t, filepath.Join(gitDir, "HEAD"))
	cmd := exec.Command("git", "--git-dir", gitDir, "--work-tree", workspace, "cat-file", "-t", early)
	cmd.Env = env
	require.Error(t, cmd.Run(), "pre-rewind commits must be unreachable after prune")
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

// A sandbox provisioned before GIT_DIR moved out of /workspace still has its
// checkpoints in the work tree, and the SHAs on those turns only resolve
// there. Rewind and fork must still be able to reset to them.
func TestAdoptsLegacyWorkTreeCheckpointRepo(t *testing.T) {
	requireGit(t)
	workspace, gitDir, env := splitGitDirs(t)

	// The pre-move layout: checkpoints committed into $WORK_TREE/.git under
	// the checkpointer's identity.
	runGitAt(t, env, workspace, "init", "-q")
	runGitAt(t, env, workspace, "config", "user.email", "agent@weknora.local")
	runGitAt(t, env, workspace, "config", "user.name", "WeKnora Agent")
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "keep.txt"), []byte("early"), 0o644))
	runGitAt(t, env, workspace, "add", "-A")
	runGitAt(t, env, workspace, "commit", "-q", "-m", "turn:msg-1")
	legacy := strings.TrimSpace(runGitAt(t, env, workspace, "rev-parse", "HEAD"))
	if !gitSHAPattern.MatchString(legacy) {
		t.Skip("host git is not using SHA-1 object names")
	}

	require.NoError(t, os.WriteFile(filepath.Join(workspace, "later.txt"), []byte("later"), 0o644))
	runGitAt(t, env, workspace, "add", "-A")
	runGitAt(t, env, workspace, "commit", "-q", "-m", "turn:msg-2")

	script, err := workspaceResetScript(workspace, gitDir, legacy)
	require.NoError(t, err)
	runWorkspaceGitScript(t, env, script)

	require.FileExists(t, filepath.Join(gitDir, "HEAD"), "legacy repo must move to the new git dir")
	_, statErr := os.Stat(filepath.Join(workspace, ".git"))
	require.True(t, os.IsNotExist(statErr), "adopted repo must not be left in the work tree")
	require.Equal(t, legacy, strings.TrimSpace(
		runGitSplit(t, env, workspace, gitDir, "rev-parse", "HEAD"),
	), "reset must land on the pre-move checkpoint instead of failing on a bad object")
	body, err := os.ReadFile(filepath.Join(workspace, "keep.txt"))
	require.NoError(t, err)
	require.Equal(t, "early", string(body))
	_, statErr = os.Stat(filepath.Join(workspace, "later.txt"))
	require.True(t, os.IsNotExist(statErr))
}

// Adoption keys on the checkpointer's committer identity, so a repository the
// agent created for its own work in /workspace is never swallowed.
func TestDoesNotAdoptAgentOwnedWorkTreeRepo(t *testing.T) {
	requireGit(t)
	workspace, gitDir, env := splitGitDirs(t)

	runGitAt(t, env, workspace, "init", "-q")
	runGitAt(t, env, workspace, "config", "user.email", "dev@example.com")
	runGitAt(t, env, workspace, "config", "user.name", "Dev")
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "app.txt"), []byte("mine"), 0o644))
	runGitAt(t, env, workspace, "add", "-A")
	runGitAt(t, env, workspace, "commit", "-q", "-m", "agent")
	agentHEAD := strings.TrimSpace(runGitAt(t, env, workspace, "rev-parse", "HEAD"))

	runWorkspaceGitScript(t, env, checkpointScript(workspace, gitDir, "msg-1"))

	require.DirExists(t, filepath.Join(workspace, ".git"))
	require.Equal(t, agentHEAD, strings.TrimSpace(
		runGitAt(t, env, workspace, "rev-parse", "HEAD"),
	), "the agent's own repository must be left exactly as it was")
	require.FileExists(t, filepath.Join(gitDir, "HEAD"))
	require.NotEqual(t, agentHEAD, strings.TrimSpace(
		runGitSplit(t, env, workspace, gitDir, "rev-parse", "HEAD"),
	))
}

// Once adopted, the work tree has no .git left, so a later turn must not try
// to adopt anything again.
func TestAdoptionIsOnlyAttemptedWhileTheNewGitDirIsMissing(t *testing.T) {
	requireGit(t)
	workspace, gitDir, env := splitGitDirs(t)

	runGitAt(t, env, workspace, "init", "-q")
	runGitAt(t, env, workspace, "config", "user.email", "agent@weknora.local")
	runGitAt(t, env, workspace, "config", "user.name", "WeKnora Agent")
	runGitAt(t, env, workspace, "commit", "-q", "--allow-empty", "-m", "turn:msg-0")
	legacy := strings.TrimSpace(runGitAt(t, env, workspace, "rev-parse", "HEAD"))

	runWorkspaceGitScript(t, env, checkpointScript(workspace, gitDir, "msg-1"))
	require.Equal(t, "commit", strings.TrimSpace(
		runGitSplit(t, env, workspace, gitDir, "cat-file", "-t", legacy),
	), "the adopted history must still be reachable")

	// The agent now starts its own repository in the work tree.
	runGitAt(t, env, workspace, "init", "-q")
	runGitAt(t, env, workspace, "config", "user.email", "agent@weknora.local")
	runGitAt(t, env, workspace, "config", "user.name", "WeKnora Agent")
	runGitAt(t, env, workspace, "commit", "-q", "--allow-empty", "-m", "agent")
	agentHEAD := strings.TrimSpace(runGitAt(t, env, workspace, "rev-parse", "HEAD"))

	runWorkspaceGitScript(t, env, checkpointScript(workspace, gitDir, "msg-2"))

	require.DirExists(t, filepath.Join(workspace, ".git"))
	require.Equal(t, agentHEAD, strings.TrimSpace(runGitAt(t, env, workspace, "rev-parse", "HEAD")))
}

// layoutRunner records the checkpoint command. It does not embed
// hostShellRunner: that fake returns VersionsWorkspace=false and would skip
// the checkpoint before the layout is consulted.
type layoutRunner struct {
	layout   sandbox.WorkspaceLayout
	cmds     []string
	dirs     []string
	exitCode int
}

func (r *layoutRunner) SessionWorkspaceLayout(context.Context, string) (sandbox.WorkspaceLayout, error) {
	return r.layout, nil
}

func (r *layoutRunner) ExecShellCommand(
	_ context.Context, _ string, command, workDir string, _ time.Duration, _ map[string]string,
) (*sandbox.ExecuteResult, error) {
	r.cmds = append(r.cmds, command)
	r.dirs = append(r.dirs, workDir)
	return &sandbox.ExecuteResult{ExitCode: r.exitCode, Stdout: strings.Repeat("a", 40) + "\n"}, nil
}

func (r *layoutRunner) VersionsWorkspace(context.Context, string) bool { return true }

func TestCheckpointUsesTheSessionLayout(t *testing.T) {
	l, err := sandbox.UserWorkspaceLayout("0f8fad5b-d9cb-469f-a165-70867728950e")
	require.NoError(t, err)
	runner := &layoutRunner{layout: l}
	cp := NewWorkspaceCheckpointer(runner).Checkpoint(
		context.Background(), l.Root[len("/workspace/sessions/"):], "sb-1", "m-1")
	require.NotNil(t, cp)
	require.Contains(t, runner.cmds[0], "WORK_TREE='"+l.Root+"'")
	require.Contains(t, runner.cmds[0], "GIT_DIR='"+l.GitDir+"'")
	require.NotContains(t, runner.cmds[0], "legacy_git_dir", "a shared machine never adopts a work-tree .git")
	require.Equal(t, l.Root, runner.dirs[0])
}

func TestCheckpointScriptForRemoteLayoutIsUnchanged(t *testing.T) {
	require.Equal(t,
		checkpointScript(sandbox.SessionWorkspaceRoot, sandbox.SessionGitDir, "m-1"),
		checkpointScriptFor(sandbox.RemoteWorkspaceLayout(), "m-1"))
}

func TestResetScriptChecksTheCommitFirst(t *testing.T) {
	script, err := workspaceResetScriptFor(sandbox.RemoteWorkspaceLayout(), strings.Repeat("b", 40))
	require.NoError(t, err)
	require.Contains(t, script, `cat-file -e "`+strings.Repeat("b", 40)+`^{commit}" 2>/dev/null || exit 3`)
	require.Less(t, strings.Index(script, "cat-file -e"), strings.Index(script, "reset --hard"))
}

func TestResetMapsMissingCommitToErrWorkspaceCommitMissing(t *testing.T) {
	l, err := sandbox.UserWorkspaceLayout("0f8fad5b-d9cb-469f-a165-70867728950e")
	require.NoError(t, err)
	runner := &layoutRunner{layout: l, exitCode: 3}
	err = resetWorkspaceToCommit(context.Background(), runner, "s", strings.Repeat("c", 40), "sb")
	require.ErrorIs(t, err, errWorkspaceCommitMissing)
	require.Contains(t, runner.cmds[0], "GIT_DIR='"+l.GitDir+"'")
	require.Contains(t, runner.cmds[0], "WORK_TREE='"+l.Root+"'")
	require.Equal(t, l.Root, runner.dirs[0])
}
