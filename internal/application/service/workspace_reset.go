// Package service - rolling a session sandbox's /workspace back to a commit.
//
// Two callers share this: ForkBootstrapper rolls a forked session's first
// sandbox back to the fork point, and SessionRewindService rolls the session's
// own live sandbox back to a rewind point. Both need identical semantics, so
// the script and the exec wrapper live here rather than in either one.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
)

// workspaceResetTimeout bounds the git rollback. clean -fdx may delete a large
// untracked tree, and gc --prune=now walks leftover objects from later turns,
// so this is more generous than the per-turn checkpoint.
const workspaceResetTimeout = 90 * time.Second

// errWorkspaceCommitMissing: the requested checkpoint is not in this
// session's repository (it was rebuilt, deleted, or belongs elsewhere).
// Detected before anything is modified.
var errWorkspaceCommitMissing = errors.New("workspace reset: checkpoint commit is not in the session repository")

const exitCommitMissing = 3

// workspaceResetScript renders the rollback of workspace to sha.
//
// reset --hard restores tracked files (including input/ and output/). Other
// refs, ORIG_HEAD, and reflogs still name later-turn commits, so those are
// deleted and gc'd before clean -fdx; otherwise the working tree would look
// right while `git checkout` could resurrect later files. -x then drops
// leftover untracked files that are not in that commit.
func workspaceResetScript(workspace, gitDir, sha string) (string, error) {
	if !gitSHAPattern.MatchString(sha) {
		return "", fmt.Errorf("workspace reset: invalid commit sha %q", truncateForLog(sha))
	}
	return "set -e\n" + gitWorkspacePreamble(workspace, gitDir) +
		"git_ws reset --hard " + sha + "\n" +
		gitWorkspacePruneAndClean(), nil
}

// workspaceResetScriptFor rolls l back to sha. The commit is checked first so
// a missing checkpoint fails before any file changes.
func workspaceResetScriptFor(l sandbox.WorkspaceLayout, sha string) (string, error) {
	if !gitSHAPattern.MatchString(sha) {
		return "", fmt.Errorf("workspace reset: invalid commit sha %q", truncateForLog(sha))
	}
	return "set -e\n" + gitWorkspacePreambleFor(l) +
		fmt.Sprintf("git_ws cat-file -e \"%s^{commit}\" 2>/dev/null || exit %d\n", sha, exitCommitMissing) +
		"git_ws reset --hard " + sha + "\n" +
		gitWorkspacePruneAndClean(), nil
}

// workspaceEmptyResetScript is the start-over path: kept history has no SHA,
// so we commit an empty tree and run the same reset+prune+clean as a SHA rewind.
func workspaceEmptyResetScript(workspace, gitDir string) string {
	return "set -e\n" + gitWorkspacePreamble(workspace, gitDir) + gitWorkspaceEnsureRepo() + `
empty=$(git_ws commit-tree "$(git_ws mktree </dev/null)" -m 'rewind: empty')
git_ws reset --hard "$empty"
` + gitWorkspacePruneAndClean()
}

func workspaceEmptyResetScriptFor(l sandbox.WorkspaceLayout) string {
	return "set -e\n" + gitWorkspacePreambleFor(l) + gitWorkspaceEnsureRepo() + `
empty=$(git_ws commit-tree "$(git_ws mktree </dev/null)" -m 'rewind: empty')
git_ws reset --hard "$empty"
` + gitWorkspacePruneAndClean()
}

// gitWorkspaceEnsureRepo is the checkpointer's first-use init. Empty rewind
// reuses it so a sandbox that never checkpointed can still reset to empty.
func gitWorkspaceEnsureRepo() string {
	return `git_ws rev-parse --git-dir >/dev/null 2>&1 || {
  mkdir -p "$(dirname "$GIT_DIR")"
  git_ws init -q
  git_ws config user.email agent@weknora.local
  git_ws config user.name 'WeKnora Agent'
}
`
}

func gitWorkspacePruneAndClean() string {
	return `current=$(git_ws symbolic-ref -q HEAD || true)
for ref in $(git_ws for-each-ref --format='%(refname)'); do
  [ -z "$ref" ] && continue
  [ "$ref" = "$current" ] && continue
  git_ws update-ref -d "$ref"
done
rm -f "$GIT_DIR"/ORIG_HEAD "$GIT_DIR"/FETCH_HEAD
git_ws reflog expire --expire=now --all
git_ws gc --prune=now
git_ws clean -fdx
`
}

// gitWorkspacePreamble is shared by checkpoint and reset so fork and rewind
// cannot drift onto different git layouts. GIT_DIR is outside /workspace;
// --work-tree still edits the session files.
func gitWorkspacePreamble(workspace, gitDir string) string {
	return gitWorkspaceVars(workspace, gitDir) + gitWorkspaceAdoptLegacyRepo()
}

func gitWorkspaceVars(workspace, gitDir string) string {
	return fmt.Sprintf(`WORK_TREE=%[1]s
GIT_DIR=%[2]s
git_ws() {
  git -c safe.directory='*' --git-dir="$GIT_DIR" --work-tree="$WORK_TREE" "$@"
}
mkdir -p "$WORK_TREE"
`, shellSingleQuote(workspace), shellSingleQuote(gitDir))
}

// gitWorkspacePreambleFor is gitWorkspacePreamble for a layout. Only a
// per-session machine adopts a legacy $WORK_TREE/.git; on a shared machine
// that directory is an agent's own repository and must be left alone.
func gitWorkspacePreambleFor(l sandbox.WorkspaceLayout) string {
	if l.IsSharedRemote() {
		return gitWorkspaceVars(l.Root, l.GitDir)
	}
	return gitWorkspacePreamble(l.Root, l.GitDir)
}

// workspaceLayoutFor asks the runner for the session's layout; runners that
// do not advertise one keep the /workspace contract.
func workspaceLayoutFor(ctx context.Context, runner any, sessionID string) (sandbox.WorkspaceLayout, error) {
	provider, ok := runner.(sandbox.SessionWorkspaceLayoutProvider)
	if !ok || provider == nil {
		return sandbox.RemoteWorkspaceLayout(), nil
	}
	l, err := provider.SessionWorkspaceLayout(ctx, sessionID)
	if err != nil {
		return sandbox.WorkspaceLayout{}, err
	}
	l = l.Normalized()
	if !l.HasRoot() || l.GitDir == "" {
		return sandbox.WorkspaceLayout{}, errors.New("workspace reset: session layout has no versioned workspace")
	}
	return l, nil
}

// gitWorkspaceAdoptLegacyRepo migrates a sandbox that still keeps its
// checkpoints in the work tree.
//
// Before GIT_DIR moved out of /workspace, checkpoints committed into
// $WORK_TREE/.git, and the SHAs recorded on those turns only resolve there.
// Without this, a sandbox provisioned before the move would find no repo at
// the new path, start an empty one, and then fail every fork or rewind that
// targets a pre-move checkpoint with "bad object".
//
// It runs in the shared preamble so checkpoint, reset and empty reset all
// adopt the old store. Two guards keep it from touching anything else: the
// new GIT_DIR must be absent (a migrated sandbox never re-enters this), and
// the work-tree repo must carry the checkpointer's committer identity, so an
// agent's own `git init` in /workspace is left alone.
func gitWorkspaceAdoptLegacyRepo() string {
	return `if ! git_ws rev-parse --git-dir >/dev/null 2>&1; then
  legacy_git_dir="$WORK_TREE/.git"
  legacy_owner=$(git -c safe.directory='*' --git-dir="$legacy_git_dir" config user.email 2>/dev/null || true)
  if [ "$legacy_owner" = agent@weknora.local ]; then
    mkdir -p "$(dirname "$GIT_DIR")"
    mv "$legacy_git_dir" "$GIT_DIR"
  fi
fi
`
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, `'`, `'"'"'`) + "'"
}

func gitResetFailure(sha, stderr string) error {
	return fmt.Errorf("workspace reset: git reset to %s failed: %s", sha, stderr)
}

func execWorkspaceReset(
	ctx context.Context, runner SandboxShellRunner, sessionID, script, expectedSandboxID, workDir string,
) (*sandbox.ExecuteResult, error) {
	if runner == nil {
		return nil, errors.New("workspace reset: no shell runner wired")
	}
	if withOpts, ok := runner.(sandbox.SessionInstallShellExecutor); ok {
		return withOpts.ExecShellCommandWithOptions(ctx, sessionID, script, sandbox.ShellExecOptions{
			WorkDir:           workDir,
			Timeout:           workspaceResetTimeout,
			SkipWorkspacePrep: true,
			ExpectedSandboxID: expectedSandboxID,
		})
	}
	return runner.ExecShellCommand(
		ctx, sessionID, script, workDir, workspaceResetTimeout, nil,
	)
}

// resetWorkspaceToCommit rolls the session's live /workspace back to sha
// through a session-scoped shell runner.
//
// Callers that already hold a sandbox handle (the fork bootstrapper, which
// runs under the lifecycle lock and must not Resolve) exec the script
// themselves; everyone else goes through the runner. expectedSandboxID makes
// that path lookup-only so rewind cannot provision a replacement VM.
func resetWorkspaceToCommit(
	ctx context.Context, runner SandboxShellRunner, sessionID, sha, expectedSandboxID string,
) error {
	sha = strings.TrimSpace(sha)
	layout, err := workspaceLayoutFor(ctx, runner, sessionID)
	if err != nil {
		return err
	}
	script, err := workspaceResetScriptFor(layout, sha)
	if err != nil {
		return err
	}
	result, err := execWorkspaceReset(ctx, runner, sessionID, script, expectedSandboxID, layout.Root)
	if err != nil {
		return fmt.Errorf("workspace reset: git reset exec: %w", err)
	}
	if result != nil && result.ExitCode == exitCommitMissing {
		return errWorkspaceCommitMissing
	}
	if result == nil || result.ExitCode != 0 {
		stderr := ""
		if result != nil {
			stderr = result.Stderr
		}
		return gitResetFailure(sha, stderr)
	}
	return nil
}

func resetWorkspaceToEmpty(
	ctx context.Context, runner SandboxShellRunner, sessionID, expectedSandboxID string,
) error {
	layout, err := workspaceLayoutFor(ctx, runner, sessionID)
	if err != nil {
		return err
	}
	script := workspaceEmptyResetScriptFor(layout)
	result, err := execWorkspaceReset(ctx, runner, sessionID, script, expectedSandboxID, layout.Root)
	if err != nil {
		return fmt.Errorf("workspace reset: empty workspace exec: %w", err)
	}
	if result == nil || result.ExitCode != 0 {
		stderr := ""
		if result != nil {
			stderr = result.Stderr
		}
		return fmt.Errorf("workspace reset: empty workspace failed: %s", stderr)
	}
	return nil
}
