package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// sessionSandboxPinReader is the pin lookup used to choose the manager that
// owns a session's binding. *SessionSandboxPinner is the production
// implementation; tests use a stub so this does not need a database.
type sessionSandboxPinReader interface {
	Read(ctx context.Context, sessionID string) (SandboxPin, error)
}

// PinnedSessionSandbox resolves the session's pinned sandbox manager at
// request time. The process-wide sandbox.Manager is DisabledManager, so
// checkpoint and BoundSandboxID must go through the same pin + tenant
// resolver path ArtifactCollector and SandboxTerminalService already use.
type PinnedSessionSandbox struct {
	pinner   sessionSandboxPinReader
	resolver sandbox.TenantSandboxResolver
	fallback sandbox.Manager
}

// NewPinnedSessionSandbox wires request-time sandbox access. Any dependency
// may be nil; BoundSandboxID then reports ok=false and ExecShellCommand
// fails, which WorkspaceCheckpointer treats as "no checkpoint".
func NewPinnedSessionSandbox(
	pinner sessionSandboxPinReader,
	resolver sandbox.TenantSandboxResolver,
	fallback sandbox.Manager,
) *PinnedSessionSandbox {
	return &PinnedSessionSandbox{
		pinner:   pinner,
		resolver: resolver,
		fallback: fallback,
	}
}

func (a *PinnedSessionSandbox) manager(ctx context.Context, sessionID string) sandbox.Manager {
	if a == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	if a.pinner == nil {
		return nil
	}
	pin, err := a.pinner.Read(ctx, sessionID)
	if err != nil || pin.IsZero() {
		return nil
	}
	// The workspace comes from the pin, not the request. Fork runs from a plain
	// POST as the session owner, while a shared agent's sandbox lives on the
	// lending workspace's config — reading it as the session owner found
	// nothing and silently degraded the fork to SNAPSHOT_UNSUPPORTED.
	sessionTenantID, _ := types.TenantIDFromContext(ctx)
	mgr, err := resolveTenantSandboxForConfig(
		ctx, a.resolver, a.fallback, pin.TenantOr(sessionTenantID), pin.ConfigID, nil,
	)
	if err != nil {
		return nil
	}
	return mgr
}

// BoundSandboxID reports the sandbox currently bound to sessionID without
// provisioning one. A session with no pin or no live binding reports
// ok=false.
func (a *PinnedSessionSandbox) BoundSandboxID(ctx context.Context, sessionID string) (string, bool) {
	mgr := a.manager(ctx, sessionID)
	type lookup interface {
		BoundSandboxID(context.Context, string) (string, bool)
	}
	if l, ok := mgr.(lookup); ok {
		return l.BoundSandboxID(ctx, sessionID)
	}
	return "", false
}

// ExecShellCommand runs a command on the session's pinned sandbox manager.
func (a *PinnedSessionSandbox) ExecShellCommand(
	ctx context.Context,
	sessionID, command, workDir string,
	timeout time.Duration,
	env map[string]string,
) (*sandbox.ExecuteResult, error) {
	mgr := a.manager(ctx, sessionID)
	runner, ok := mgr.(SandboxShellRunner)
	if !ok || runner == nil {
		return nil, errors.New("sandbox: no shell runner for session")
	}
	return runner.ExecShellCommand(ctx, sessionID, command, workDir, timeout, env)
}

// ExecShellCommandWithOptions forwards maintenance options to the pinned
// manager, preserving lookup-only execution and the expected sandbox identity.
func (a *PinnedSessionSandbox) ExecShellCommandWithOptions(
	ctx context.Context, sessionID, command string, opts sandbox.ShellExecOptions,
) (*sandbox.ExecuteResult, error) {
	mgr := a.manager(ctx, sessionID)
	runner, ok := mgr.(sandbox.SessionInstallShellExecutor)
	if !ok || runner == nil {
		return nil, errors.New("sandbox: no maintenance shell runner for session")
	}
	return runner.ExecShellCommandWithOptions(ctx, sessionID, command, opts)
}

// HasActiveTurn reports whether the session's pinned manager currently holds a
// turn lease. Managers that do not expose the method are treated as not busy.
func (a *PinnedSessionSandbox) HasActiveTurn(ctx context.Context, sessionID string) (bool, error) {
	mgr := a.manager(ctx, sessionID)
	type turnChecker interface {
		HasActiveTurn(context.Context, string) (bool, error)
	}
	if checker, ok := mgr.(turnChecker); ok {
		return checker.HasActiveTurn(ctx, sessionID)
	}
	return false, nil
}

// TryLockRewind takes an exclusive rewind lock through the session's pinned
// manager. Managers that do not expose the method succeed as a no-op.
func (a *PinnedSessionSandbox) TryLockRewind(ctx context.Context, sessionID string) (func(), error) {
	mgr := a.manager(ctx, sessionID)
	type locker interface {
		TryLockRewind(context.Context, string) (func(), error)
	}
	if l, ok := mgr.(locker); ok {
		return l.TryLockRewind(ctx, sessionID)
	}
	return func() {}, nil
}

// HasRewindLock reports whether rewind currently holds the session on the
// pinned manager. Managers that do not expose the method are treated as free.
func (a *PinnedSessionSandbox) HasRewindLock(ctx context.Context, sessionID string) (bool, error) {
	mgr := a.manager(ctx, sessionID)
	type rewindLockReader interface {
		HasRewindLock(context.Context, string) (bool, error)
	}
	if reader, ok := mgr.(rewindLockReader); ok {
		return reader.HasRewindLock(ctx, sessionID)
	}
	return false, nil
}

// CreateForkSnapshot snapshots the session's already-bound sandbox through the
// pinned manager. Managers that do not expose the method return an error so
// fork can degrade to SNAPSHOT_UNSUPPORTED.
func (a *PinnedSessionSandbox) CreateForkSnapshot(
	ctx context.Context, sessionID, name string,
) (string, error) {
	mgr := a.manager(ctx, sessionID)
	type snapshotter interface {
		CreateForkSnapshot(context.Context, string, string) (string, error)
	}
	if s, ok := mgr.(snapshotter); ok {
		return s.CreateForkSnapshot(ctx, sessionID, name)
	}
	return "", errors.New("sandbox: fork snapshot is not supported")
}

// DeleteForkSnapshot removes a fork snapshot through the session's pinned
// manager. Managers that do not expose the method return an error so the
// caller can keep the lease for the reaper.
func (a *PinnedSessionSandbox) DeleteForkSnapshot(
	ctx context.Context, sessionID, snapshotID string,
) error {
	mgr := a.manager(ctx, sessionID)
	type deleter interface {
		DeleteForkSnapshot(context.Context, string, string) error
	}
	if d, ok := mgr.(deleter); ok {
		return d.DeleteForkSnapshot(ctx, sessionID, snapshotID)
	}
	type snapshotDeleter interface {
		DeleteSnapshot(context.Context, string) error
	}
	if d, ok := mgr.(snapshotDeleter); ok {
		return d.DeleteSnapshot(ctx, snapshotID)
	}
	return errors.New("sandbox: fork snapshot delete is not supported")
}

var _ SandboxShellRunner = (*PinnedSessionSandbox)(nil)

var _ SessionForkSandboxPort = (*PinnedSessionSandbox)(nil)

var _ SessionRewindSandboxPort = (*PinnedSessionSandbox)(nil)

var _ SessionForkSandboxPort = (*sandbox.SessionBoundManager)(nil)

var _ SessionRewindSandboxPort = (*sandbox.SessionBoundManager)(nil)
