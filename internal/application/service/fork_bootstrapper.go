// Package service - forked-session sandbox bootstrap.
//
// ForkBootstrapper implements sandbox.SessionBootstrapper for forked sessions:
// it points the first create at the fork snapshot, then rolls /workspace back
// to the fork point with git. Git tracks the full tree, including input/ and
// output/; object storage is not rewritten into the sandbox.
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
// untracked tree, so it is more generous than the per-turn checkpoint.
const forkResetTimeout = 60 * time.Second

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

	b.rewriteCopiedCheckpoints(ctx, key.SessionID, pending.SourceSandboxID, handle)

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
		logger.Warnf(ctx, "[ForkBootstrap] lookup shared snapshot %s failed; leaving it: %v", pending.SnapshotID, err)
	} else if keep {
		logger.Infof(ctx, "[ForkBootstrap] snapshot %s still referenced by another unopened fork; deferring delete", pending.SnapshotID)
	} else if err := b.deleteSnapshot(ctx, pending.SnapshotID); err != nil {
		logger.Warnf(ctx, "[ForkBootstrap] delete snapshot %s failed after reset; sandbox kept: %v", pending.SnapshotID, err)
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

func forkResetScript(sha string) string {
	return fmt.Sprintf(`set -e
git config --global safe.directory %[1]s
git -C %[1]s reset --hard %[2]s
git -C %[1]s clean -fdx`, sandbox.SessionWorkspaceRoot, sha)
}

func gitResetFailure(sha, stderr string) error {
	return fmt.Errorf("fork bootstrap: git reset to %s failed: %s", sha, stderr)
}

// resetWorkspace rolls /workspace back to sha.
//
// clean -fdx still runs after reset: input/ and output/ are tracked, so
// reset --hard restores their fork-point tree. -x drops leftover untracked
// files from the live snapshot that are not in that commit.
func (b *ForkBootstrapper) resetWorkspace(
	ctx context.Context, sessionID, sha string, handle sandbox.RemoteSandboxHandle,
) error {
	script := forkResetScript(sha)

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
) {
	if b.messages == nil || handle == nil {
		return
	}
	newID := strings.TrimSpace(handle.ID())
	oldID := strings.TrimSpace(oldSandboxID)
	if newID == "" || oldID == "" || newID == oldID {
		return
	}
	if err := b.messages.RewriteSandboxCheckpoints(ctx, sessionID, oldID, newID); err != nil {
		logger.Warnf(ctx, "[ForkBootstrap] rewrite sandbox checkpoints for %s failed: %v", sessionID, err)
	}
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
			logger.Warnf(cleanupCtx, "[ForkBootstrap] lookup shared snapshot %s failed; leaving it: %v", pending.SnapshotID, err)
		} else if keep {
			logger.Infof(cleanupCtx, "[ForkBootstrap] snapshot %s still referenced by another unopened fork; deferring delete", pending.SnapshotID)
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
