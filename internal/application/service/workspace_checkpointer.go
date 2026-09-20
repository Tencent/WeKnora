// Package service - per-turn /workspace git checkpoints.
//
// WorkspaceCheckpointer commits the session sandbox's /workspace at the end of
// every agent turn so session fork can later roll a forked sandbox back to a
// specific turn. It mirrors ArtifactCollector's shape deliberately: a narrow
// injectable interface for the sandbox side, and strict best-effort semantics
// so an auxiliary feature can never break a reply.
//
// Contract:
//   - Never returns an error. A failed checkpoint yields nil, the message
//     simply carries no checkpoint, and that fork point degrades.
//   - Never lazy-creates a sandbox: an empty sandboxID short-circuits.
//   - Idempotent inside the sandbox: the script initialises the repository
//     only when absent, so pause/resume and sandbox rebuilds self-heal.
package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// SandboxShellRunner is the narrow subset of sandbox behaviour the
// checkpointer needs. *sandbox.SessionBoundManager satisfies it in production;
// tests use a fake to assert on the emitted script without a real sandbox.
type SandboxShellRunner interface {
	ExecShellCommand(
		ctx context.Context,
		sessionID, command, workDir string,
		timeout time.Duration,
		env map[string]string,
	) (*sandbox.ExecuteResult, error)
}

// workspaceCheckpointTimeout bounds the git round-trip. `git add -A` walks the
// whole working tree, so this is generous relative to a normal commit but
// still short enough that a wedged sandbox cannot stall the completion path.
const workspaceCheckpointTimeout = 30 * time.Second

// gitSHAPattern matches a full 40-character hex object name. The script's last
// stdout line must look like one; anything else means git printed a warning or
// the script diverged, and we refuse to record a checkpoint we cannot trust.
var gitSHAPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// WorkspaceCheckpointer commits /workspace after each agent turn.
type WorkspaceCheckpointer struct {
	runner SandboxShellRunner
}

// NewWorkspaceCheckpointer wires a checkpointer. A nil runner yields a
// checkpointer that degrades to "no checkpoint", matching the graceful
// degradation contract used by ArtifactCollector.
func NewWorkspaceCheckpointer(runner SandboxShellRunner) *WorkspaceCheckpointer {
	return &WorkspaceCheckpointer{runner: runner}
}

// checkpointScript renders the idempotent init-and-commit script.
//
// Ordering matters in two places:
//
//   - `safe.directory` comes first. /workspace is owned by uid 1000 while
//     shell_exec runs as root by default, and git refuses to touch a
//     dubiously-owned repository — including from the rev-parse probe below.
//     Plain assignment rather than `--add`: safe.directory is a multi-valued
//     key, so --add would append one duplicate line to ~/.gitconfig per turn.
//
//   - `--allow-empty` is mandatory. A turn that only answered a question
//     without touching a file would otherwise produce no commit, leaving that
//     message with no checkpoint and making it unusable as a fork point. With
//     it, every turn has exactly one checkpoint and fork boundaries are always
//     resolvable.
func checkpointScript(workspace, messageID string) string {
	return fmt.Sprintf(`set -e
git config --global safe.directory %[1]s
git -C %[1]s rev-parse --git-dir >/dev/null 2>&1 || {
  git init -q %[1]s
  git -C %[1]s config user.email agent@weknora.local
  git -C %[1]s config user.name 'WeKnora Agent'
}
git -C %[1]s add -A
git -C %[1]s commit -q --allow-empty -m 'turn:%[2]s'
git -C %[1]s rev-parse HEAD`, workspace, messageID)
}

// Checkpoint commits /workspace and returns the resulting checkpoint, or nil
// when anything at all went wrong. It never returns an error by design: the
// caller runs on the turn-completion path and must not be blocked here.
func (c *WorkspaceCheckpointer) Checkpoint(
	ctx context.Context, sessionID, sandboxID, messageID string,
) *types.SandboxCheckpoint {
	if c == nil || c.runner == nil {
		return nil
	}
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(messageID) == "" {
		return nil
	}
	// No bound sandbox means there is nothing to snapshot, and a checkpoint
	// without a sandbox ID could never be validated at fork time anyway.
	if strings.TrimSpace(sandboxID) == "" {
		return nil
	}

	var result *sandbox.ExecuteResult
	var err error
	command := checkpointScript(sandbox.SessionWorkspaceRoot, messageID)
	if runner, ok := c.runner.(sandbox.SessionInstallShellExecutor); ok {
		// Git operates on the existing workspace. Do not prepare artifact/input
		// directories or let a late checkpoint provision a replacement sandbox.
		result, err = runner.ExecShellCommandWithOptions(ctx, sessionID, command, sandbox.ShellExecOptions{
			WorkDir: sandbox.SessionWorkspaceRoot, Timeout: workspaceCheckpointTimeout,
			SkipWorkspacePrep: true, ExpectedSandboxID: sandboxID,
		})
	} else {
		result, err = c.runner.ExecShellCommand(ctx, sessionID, command,
			sandbox.SessionWorkspaceRoot, workspaceCheckpointTimeout, nil)
	}
	if err != nil {
		logger.Warnf(ctx, "[WorkspaceCheckpointer] exec failed session=%s message=%s: %v",
			sessionID, messageID, err)
		return nil
	}
	if result == nil {
		logger.Warnf(ctx, "[WorkspaceCheckpointer] nil result session=%s message=%s",
			sessionID, messageID)
		return nil
	}
	if result.ExitCode != 0 {
		logger.Warnf(ctx,
			"[WorkspaceCheckpointer] git exited %d session=%s message=%s stderr=%s",
			result.ExitCode, sessionID, messageID, truncateForLog(result.Stderr))
		return nil
	}

	sha := lastNonEmptyLine(result.Stdout)
	if !gitSHAPattern.MatchString(sha) {
		logger.Warnf(ctx,
			"[WorkspaceCheckpointer] unexpected stdout tail session=%s message=%s tail=%q",
			sessionID, messageID, truncateForLog(sha))
		return nil
	}

	return &types.SandboxCheckpoint{
		SandboxID:   sandboxID,
		CommitSHA:   sha,
		CommittedAt: time.Now().UTC(),
	}
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

func truncateForLog(s string) string {
	const limit = 256
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}
