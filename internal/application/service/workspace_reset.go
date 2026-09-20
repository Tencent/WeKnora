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
	return fmt.Sprintf(`set -e
%s
git_ws reset --hard %s
current=$(git_ws symbolic-ref -q HEAD || true)
for ref in $(git_ws for-each-ref --format='%%(refname)'); do
  [ -z "$ref" ] && continue
  [ "$ref" = "$current" ] && continue
  git_ws update-ref -d "$ref"
done
rm -f "$GIT_DIR"/ORIG_HEAD "$GIT_DIR"/FETCH_HEAD
git_ws reflog expire --expire=now --all
git_ws gc --prune=now
git_ws clean -fdx`, gitWorkspacePreamble(workspace, gitDir), sha), nil
}

// gitWorkspacePreamble is shared by checkpoint and reset so fork and rewind
// cannot drift onto different git layouts. Every git call goes through git_ws:
// GIT_DIR stays outside /workspace, while --work-tree still edits the session
// files. A leftover in-tree .git from before this split is moved once so
// old sandboxes keep their checkpoint SHAs.
func gitWorkspacePreamble(workspace, gitDir string) string {
	return fmt.Sprintf(`WORK_TREE=%[1]s
GIT_DIR=%[2]s
git_ws() {
  git -c safe.directory='*' --git-dir="$GIT_DIR" --work-tree="$WORK_TREE" "$@"
}
mkdir -p "$WORK_TREE"
if [ ! -e "$GIT_DIR" ] && [ -d "$WORK_TREE/.git" ]; then
  mkdir -p "$(dirname "$GIT_DIR")"
  mv "$WORK_TREE/.git" "$GIT_DIR"
elif [ -e "$GIT_DIR" ] && [ -e "$WORK_TREE/.git" ]; then
  rm -rf "$WORK_TREE/.git"
fi
`, shellSingleQuote(workspace), shellSingleQuote(gitDir))
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, `'`, `'"'"'`) + "'"
}

func gitResetFailure(sha, stderr string) error {
	return fmt.Errorf("workspace reset: git reset to %s failed: %s", sha, stderr)
}

// resetWorkspaceToCommit rolls the session's live /workspace back to sha
// through a session-scoped shell runner.
//
// Callers that already hold a sandbox handle (the fork bootstrapper, which
// runs under the lifecycle lock and must not Resolve) exec the script
// themselves; everyone else goes through the runner, which resolves the
// session's bound sandbox.
func resetWorkspaceToCommit(
	ctx context.Context, runner SandboxShellRunner, sessionID, sha string,
) error {
	sha = strings.TrimSpace(sha)
	script, err := workspaceResetScript(sandbox.SessionWorkspaceRoot, sandbox.SessionGitDir, sha)
	if err != nil {
		return err
	}
	if runner == nil {
		return errors.New("workspace reset: no shell runner wired")
	}
	result, err := runner.ExecShellCommand(
		ctx, sessionID, script, sandbox.SessionWorkspaceRoot, workspaceResetTimeout, nil,
	)
	if err != nil {
		return fmt.Errorf("workspace reset: git reset exec: %w", err)
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
