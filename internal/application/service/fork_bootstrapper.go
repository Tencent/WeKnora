// Package service - forked-session sandbox bootstrap.
//
// ForkBootstrapper implements sandbox.SessionBootstrapper for forked sessions:
// it points the first create at the fork snapshot, then rolls /workspace back
// to the fork point with git. Git tracks the full tree, including input/ and
// output/; object storage is not rewritten into the sandbox.
//
// The provider snapshot is the live sandbox at fork *time*, so reset must
// also drop later-turn git objects. Otherwise `git checkout` of a post-fork
// commit would restore files the working tree just rolled back. Packages and
// other paths outside /workspace stay at the fork moment; that is intentional.
//
// It is all-or-nothing for the filesystem: handing back a sandbox that sits
// at the fork MOMENT rather than the fork POINT would look normal while
// silently working from the wrong baseline. Snapshot deletion is not part of
// that contract — Cube refuses to delete a snapshot while the source and
// child sandboxes still hold runtime refs, and failing the bootstrap for that
// would destroy a correctly rolled-back sandbox.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ForkSnapshotDeleter retires a consumed or abandoned fork snapshot.
type ForkSnapshotDeleter interface {
	DeleteSnapshot(ctx context.Context, snapshotID string) error
}

type forkBootstrapSessionStore interface {
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.Session, error)
	UpdateForkBootstrap(ctx context.Context, sessionID string, b *types.ForkBootstrap) error
	HasOtherUnconsumedForkSnapshot(ctx context.Context, snapshotID, excludeSessionID string) (bool, error)
}

type forkBootstrapMessageStore interface {
	RewriteSandboxCheckpoints(ctx context.Context, sessionID, oldSandboxID, newSandboxID string) error
}

// forkResetTimeout bounds the git rollback. clean -fdx may delete a large
// untracked tree, and gc --prune=now walks leftover objects from later turns,
// so this is more generous than the per-turn checkpoint.
const forkResetTimeout = 90 * time.Second

// ForkBootstrapper provisions forked sessions' first sandbox.
type ForkBootstrapper struct {
	sessions  forkBootstrapSessionStore
	messages  forkBootstrapMessageStore
	runner    SandboxShellRunner
	snapshots ForkSnapshotDeleter
	client    sandbox.RemoteSandboxClient
}

// NewForkBootstrapper wires the bootstrapper.
func NewForkBootstrapper(
	sessions forkBootstrapSessionStore,
	messages forkBootstrapMessageStore,
	runner SandboxShellRunner,
	snapshots ForkSnapshotDeleter,
) *ForkBootstrapper {
	return &ForkBootstrapper{
		sessions:  sessions,
		messages:  messages,
		runner:    runner,
		snapshots: snapshots,
	}
}

// NewForkBootstrapperFromRepos is the exported DI constructor. The sandbox
// client is bound later via WithClient.
func NewForkBootstrapperFromRepos(
	sessions interfaces.SessionRepository,
	messages interfaces.MessageRepository,
) *ForkBootstrapper {
	return NewForkBootstrapper(sessions, messages, nil, nil)
}

// WithClient returns a copy that executes AfterCreate against the client
// that created the handle. AfterCreate runs under the session lifecycle
// lock and must not call Resolve.
func (b *ForkBootstrapper) WithClient(client sandbox.RemoteSandboxClient) sandbox.SessionBootstrapper {
	if b == nil {
		return nil
	}
	cp := *b
	cp.client = client
	return &cp
}

// TemplateOverride returns the fork snapshot for a session whose bootstrap is
// still pending.
func (b *ForkBootstrapper) TemplateOverride(
	ctx context.Context, key sandbox.SessionSandboxKey,
) (string, error) {
	pending, err := b.pendingBootstrap(ctx, key)
	if err != nil {
		return "", err
	}
	if pending == nil {
		return "", nil
	}
	return pending.SnapshotID, nil
}

// OnCreateFailed retires a fork snapshot that Create could not boot.
//
// AfterCreate never runs on this path, so git-reset abandon cannot fire.
// NotFound / invalid template mean the snapshot will not start next time
// either; leave transient errors (timeout, capacity, outage) on the row so
// a later Resolve can retry the same snapshot.
func (b *ForkBootstrapper) OnCreateFailed(
	ctx context.Context, key sandbox.SessionSandboxKey, createErr error,
) {
	if b == nil || !forkSnapshotUnusable(createErr) {
		return
	}
	pending, err := b.pendingBootstrap(ctx, key)
	if err != nil {
		logger.Warnf(ctx, "[ForkBootstrap] load bootstrap after create failure: %v", err)
		return
	}
	if pending == nil {
		return
	}
	logger.Warnf(ctx, "[ForkBootstrap] create from snapshot %s failed; abandoning bootstrap: %v",
		pending.SnapshotID, createErr)
	b.abandon(ctx, key.SessionID, pending)
}

func forkSnapshotUnusable(err error) bool {
	return sandbox.IsRemoteNotFound(err) || sandbox.IsRemoteInvalidRequest(err)
}

// AfterCreate rolls the new sandbox back to the fork point.
func (b *ForkBootstrapper) AfterCreate(
	ctx context.Context, key sandbox.SessionSandboxKey, handle sandbox.RemoteSandboxHandle,
) error {
	pending, err := b.pendingBootstrap(ctx, key)
	if err != nil {
		return err
	}
	if pending == nil {
		return nil
	}

	if err := b.resetWorkspace(ctx, key.SessionID, pending.CommitSHA, handle); err != nil {
		// Retire the bootstrap so the next resolve provisions an ordinary
		// sandbox instead of retrying a rollback that just failed.
		b.abandon(ctx, key.SessionID, pending)
		return err
	}

	if err := b.rewriteCopiedCheckpoints(ctx, key.SessionID, pending.SourceSandboxID, handle); err != nil {
		// Git reset succeeded, so the snapshot is still the right disk.
		// Leave bootstrap unconsumed: lifecycle destroys this sandbox and
		// the next Resolve retries from the same snapshot instead of
		// marking consumed with the parent's sandbox ID (SANDBOX_REPLACED
		// on a nested fork).
		return err
	}

	now := time.Now().UTC()
	consumed := *pending
	consumed.ConsumedAt = &now
	if err := b.sessions.UpdateForkBootstrap(ctx, key.SessionID, &consumed); err != nil {
		return fmt.Errorf("mark fork bootstrap consumed: %w", err)
	}

	// Cube (and sometimes Docker/E2B) refuses to delete a snapshot while the
	// source sandbox and the sandbox just created from it still hold runtime
	// refs. Failing here would make lifecycle destroy a correctly rolled-back
	// sandbox, then the next Resolve would boot from the same snapshot and
	// hit the same delete — a create/destroy loop. Snapshot GC is best-effort;
	// the reaper retries leftover IDs after the refs drop.
	if keep, err := b.snapshotStillShared(ctx, key.SessionID, pending.SnapshotID); err != nil {
		logger.Warnf(ctx, "[ForkBootstrap] lookup shared snapshot %s failed; leaving it: %v",
			pending.SnapshotID, err)
	} else if keep {
		logger.Infof(ctx, "[ForkBootstrap] snapshot %s still referenced by another unopened fork; deferring delete",
			pending.SnapshotID)
	} else if err := b.deleteSnapshot(ctx, pending.SnapshotID); err != nil {
		logger.Warnf(ctx, "[ForkBootstrap] delete snapshot %s failed after reset; sandbox kept: %v",
			pending.SnapshotID, err)
	}
	return nil
}

func (b *ForkBootstrapper) pendingBootstrap(
	ctx context.Context, key sandbox.SessionSandboxKey,
) (*types.ForkBootstrap, error) {
	if b == nil || b.sessions == nil {
		return nil, nil
	}
	session, err := b.sessions.GetByID(ctx, key.TenantID, key.SessionID)
	if err != nil {
		return nil, fmt.Errorf("load session for fork bootstrap: %w", err)
	}
	if session == nil || session.ForkBootstrap == nil {
		return nil, nil
	}
	if session.ForkBootstrap.Consumed() {
		return nil, nil
	}
	if strings.TrimSpace(session.ForkBootstrap.SnapshotID) == "" {
		return nil, nil
	}
	return session.ForkBootstrap, nil
}

func (b *ForkBootstrapper) snapshotStillShared(ctx context.Context, sessionID, snapshotID string) (bool, error) {
	if b == nil || b.sessions == nil {
		return false, nil
	}
	return b.sessions.HasOtherUnconsumedForkSnapshot(ctx, snapshotID, sessionID)
}

func forkResetScript(workspace, sha string) (string, error) {
	if !gitSHAPattern.MatchString(sha) {
		return "", fmt.Errorf("fork bootstrap: invalid commit sha %q", truncateForLog(sha))
	}
	ws := shellSingleQuote(workspace)
	return fmt.Sprintf(`set -e
git config --global safe.directory %[1]s
git -C %[1]s reset --hard %[2]s
current=$(git -C %[1]s symbolic-ref -q HEAD || true)
for ref in $(git -C %[1]s for-each-ref --format='%%(refname)'); do
  [ -z "$ref" ] && continue
  [ "$ref" = "$current" ] && continue
  git -C %[1]s update-ref -d "$ref"
done
rm -f %[1]s/.git/ORIG_HEAD %[1]s/.git/FETCH_HEAD
git -C %[1]s reflog expire --expire=now --all
git -C %[1]s gc --prune=now
git -C %[1]s clean -fdx`, ws, sha), nil
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, `'`, `'"'"'`) + "'"
}

func gitResetFailure(sha, stderr string) error {
	return fmt.Errorf("fork bootstrap: git reset to %s failed: %s", sha, stderr)
}

// resetWorkspace rolls /workspace back to sha.
//
// reset --hard restores tracked files (including input/ and output/). Other
// refs, ORIG_HEAD, and reflogs still name later-turn commits from the live
// snapshot, so those are deleted and gc'd before clean -fdx; otherwise the
// working tree would look right while `git checkout` could resurrect later
// files. -x then drops leftover untracked files that are not in that commit.
func (b *ForkBootstrapper) resetWorkspace(
	ctx context.Context, sessionID, sha string, handle sandbox.RemoteSandboxHandle,
) error {
	script, err := forkResetScript(sandbox.SessionWorkspaceRoot, strings.TrimSpace(sha))
	if err != nil {
		return err
	}

	if b.client != nil && handle != nil {
		result, err := b.client.Exec(ctx, handle, sandbox.RemoteExecRequest{
			Command: script,
			Shell:   true,
			WorkDir: sandbox.SessionWorkspaceRoot,
			Timeout: forkResetTimeout,
			User:    sandbox.DefaultSandboxExecUser,
		})
		if err != nil {
			return fmt.Errorf("fork bootstrap: git reset exec: %w", err)
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

	if b.runner == nil {
		return errors.New("fork bootstrap: no shell runner wired")
	}
	result, err := b.runner.ExecShellCommand(
		ctx, sessionID, script, sandbox.SessionWorkspaceRoot, forkResetTimeout, nil,
	)
	if err != nil {
		return fmt.Errorf("fork bootstrap: git reset exec: %w", err)
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

func (b *ForkBootstrapper) rewriteCopiedCheckpoints(
	ctx context.Context, sessionID, oldSandboxID string, handle sandbox.RemoteSandboxHandle,
) error {
	if b.messages == nil || handle == nil {
		return nil
	}
	newID := strings.TrimSpace(handle.ID())
	oldID := strings.TrimSpace(oldSandboxID)
	if newID == "" || oldID == "" || newID == oldID {
		return nil
	}
	if err := b.messages.RewriteSandboxCheckpoints(ctx, sessionID, oldID, newID); err != nil {
		return fmt.Errorf("fork bootstrap: rewrite sandbox checkpoints: %w", err)
	}
	return nil
}

// abandon retires a bootstrap that could not be applied.
//
// Clear the bootstrap pointer first. Deleting the snapshot while the row still
// names it is the leak-safe order the reaper uses in reverse: if clear fails,
// the snapshot ID remains so a later pass can retry.
func (b *ForkBootstrapper) abandon(ctx context.Context, sessionID string, pending *types.ForkBootstrap) {
	cleanupCtx := context.WithoutCancel(ctx)
	if err := b.sessions.UpdateForkBootstrap(cleanupCtx, sessionID, nil); err != nil {
		logger.Warnf(cleanupCtx, "[ForkBootstrap] clear bootstrap of %s failed: %v", sessionID, err)
		return
	}
	if pending != nil {
		if keep, err := b.snapshotStillShared(cleanupCtx, sessionID, pending.SnapshotID); err != nil {
			logger.Warnf(cleanupCtx, "[ForkBootstrap] lookup shared snapshot %s failed; leaving it: %v",
				pending.SnapshotID, err)
		} else if keep {
			logger.Infof(cleanupCtx,
				"[ForkBootstrap] snapshot %s still referenced by another unopened fork; deferring delete",
				pending.SnapshotID)
		} else if err := b.deleteSnapshot(cleanupCtx, pending.SnapshotID); err != nil {
			logger.Warnf(cleanupCtx, "[ForkBootstrap] delete snapshot %s failed: %v", pending.SnapshotID, err)
		}
	}
}

func (b *ForkBootstrapper) deleteSnapshot(ctx context.Context, snapshotID string) error {
	if strings.TrimSpace(snapshotID) == "" {
		return nil
	}
	var err error
	if b.snapshots != nil {
		err = b.snapshots.DeleteSnapshot(ctx, snapshotID)
	} else if b.client != nil {
		if mgr, ok := sandbox.SnapshotManagerFrom(b.client); ok {
			err = mgr.DeleteSnapshot(ctx, snapshotID)
		}
	}
	if err == nil || sandbox.IsRemoteNotFound(err) {
		return nil
	}
	return err
}

var _ sandbox.SessionBootstrapper = (*ForkBootstrapper)(nil)

var _ sandbox.SessionBootstrapperWithClient = (*ForkBootstrapper)(nil)
var _ sandbox.SessionCreateFailureHandler = (*ForkBootstrapper)(nil)
