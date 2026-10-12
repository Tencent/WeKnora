package adapter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Tencent/WeKnora/internal/localsandbox"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
)

// Adapter presents the local sandbox through the session-scoped capability
// interfaces the agent already consumes. It *is* the session shell, file
// store, and layout provider — not a wrapper around a second remote path.
//
// File operations do NOT go through the OS sandbox: WeKnora itself is trusted
// and already holds the user's privileges, so putting it inside Seatbelt would
// only stop it reading its own database. They are bounded by PathGuard, which
// is derived from the same Policy the backend compiles.
//
// Adapter deliberately does NOT implement DestroySession: a host workspace is
// a real directory the user picked, often shared with other sessions, so
// tearing it down on chat delete has no safe meaning.
type Adapter struct {
	svc *localsandbox.Service
}

// New wraps a localsandbox Service as the session-scoped Manager the agent uses.
func New(svc *localsandbox.Service) *Adapter { return &Adapter{svc: svc} }

// maxListSessionFiles stops WalkDir once the listing is as large as the
// list_sandbox_files tool will ever keep (500). The tool also truncates,
// but that is after the whole tree is already in memory.
const maxListSessionFiles = 500

var (
	_ sandbox.Manager                        = (*Adapter)(nil)
	_ sandbox.SessionCapabilityProvider      = (*Adapter)(nil)
	_ sandbox.SessionWorkspaceLayoutProvider = (*Adapter)(nil)
	_ sandbox.SessionShellExecutor           = (*Adapter)(nil)
	_ sandbox.SessionFileStore               = (*Adapter)(nil)
)

// GetType reports SandboxTypeHost.
func (a *Adapter) GetType() sandbox.SandboxType { return sandbox.SandboxTypeHost }

// GetSandbox is unused: host has no remote instance to expose.
func (a *Adapter) GetSandbox() sandbox.Sandbox { return nil }

// VersionsWorkspace is false on host: the workspace is the user's real
// directory and must not be auto-committed, even if this adapter is injected
// as the process-wide checkpoint runner.
func (a *Adapter) VersionsWorkspace(context.Context, string) bool { return false }

// Cleanup is a no-op; host holds no process-wide resources to tear down.
func (a *Adapter) Cleanup(context.Context) error { return nil }

// Execute is the legacy script entry point. The local sandbox exposes shell
// execution only, so callers must use ExecShellCommand.
func (a *Adapter) Execute(context.Context, *sandbox.ExecuteConfig) (*sandbox.ExecuteResult, error) {
	return nil, fmt.Errorf("localsandbox: script execution is not supported; use shell_exec")
}

// SessionShellExecutor returns the adapter when a service is wired.
func (a *Adapter) SessionShellExecutor() sandbox.SessionShellExecutor {
	if a.svc == nil {
		return nil
	}
	return a
}

// SessionFileStore returns the adapter when a service is wired.
func (a *Adapter) SessionFileStore() sandbox.SessionFileStore {
	if a.svc == nil {
		return nil
	}
	return a
}

// SessionWorkspaceLayout describes this session's host workspace. Tools bind
// the session ID before Description() so the model sees the real Root instead
// of the remote /workspace contract.
func (a *Adapter) SessionWorkspaceLayout(
	ctx context.Context, sessionID string,
) (sandbox.WorkspaceLayout, error) {
	if a == nil || a.svc == nil {
		return sandbox.WorkspaceLayout{}, fmt.Errorf("localsandbox: session workspace is unavailable")
	}
	_, ws, err := a.svc.GuardForSession(ctx, sessionID)
	if err != nil {
		logger.Warnf(ctx, "[LocalSandbox] layout session=%s: %v", sessionID, err)
		return sandbox.WorkspaceLayout{}, err
	}
	layout := LayoutFor(ws)
	logger.Debugf(ctx, "[LocalSandbox] layout session=%s root=%s", sessionID, layout.Root)
	return layout, nil
}

// ExecShellCommand runs command under the OS sandbox for this session.
func (a *Adapter) ExecShellCommand(
	ctx context.Context,
	sessionID, command, workDir string,
	timeout time.Duration,
	env map[string]string,
) (*sandbox.ExecuteResult, error) {
	res, err := a.svc.Run(ctx, localsandbox.RunRequest{
		SessionID: sessionID,
		Command:   command,
		WorkDir:   workDir,
		Timeout:   timeout,
		Env:       env,
	})
	if err != nil {
		logger.Warnf(ctx, "[LocalSandbox] exec session=%s work_dir=%q: %v", sessionID, workDir, err)
		return nil, err
	}
	return executeResult(ctx, sessionID, res), nil
}

func executeResult(ctx context.Context, sessionID string, res *localsandbox.RunResult) *sandbox.ExecuteResult {
	out := &sandbox.ExecuteResult{
		Stdout:   res.Stdout,
		Stderr:   res.Stderr,
		ExitCode: res.Exit.Code,
		Duration: res.Exit.Duration,
		Killed:   res.Exit.Killed,
	}
	if res.Denial.IsDenied() {
		// A denial is a normal, reportable outcome: the model must see why the
		// command failed so it can choose a path inside the workspace.
		logger.Warnf(ctx, "[LocalSandbox] exec denied session=%s reason=%d path=%q",
			sessionID, res.Denial.Reason, res.Denial.Path)
		out.Stderr += "\n[sandbox] denied by workspace policy"
	}
	return out
}

func (a *Adapter) guard(
	ctx context.Context, sessionID string,
) (*localsandbox.PathGuard, localsandbox.Workspace, error) {
	return a.svc.GuardForSession(ctx, sessionID)
}

// EnsureSessionDir creates dir inside the session workspace when the policy allows it.
func (a *Adapter) EnsureSessionDir(ctx context.Context, sessionID, dir string) error {
	guard, ws, err := a.guard(ctx, sessionID)
	if err != nil {
		return err
	}
	unlock := a.svc.LockRoot(ws.Root)
	defer unlock()
	target := absoluteIn(ws.Root, dir)
	if err := guard.MkdirAll(target, 0o755); err != nil {
		if errors.Is(err, localsandbox.ErrPathDenied) {
			logger.Warnf(ctx, "[LocalSandbox] mkdir denied session=%s path=%q: %v", sessionID, dir, err)
		} else {
			logger.Errorf(ctx, "[LocalSandbox] mkdir session=%s path=%s: %v", sessionID, target, err)
		}
		return err
	}
	logger.Debugf(ctx, "[LocalSandbox] mkdir session=%s path=%s", sessionID, target)
	return nil
}

// ListSessionFiles walks dir under the session workspace, truncated at maxListSessionFiles.
func (a *Adapter) ListSessionFiles(
	ctx context.Context, sessionID, dir string,
) ([]sandbox.RemoteDirEntry, error) {
	guard, ws, err := a.guard(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	resolved, err := guard.CheckRead(absoluteIn(ws.Root, dir))
	if err != nil {
		logger.Warnf(ctx, "[LocalSandbox] list denied session=%s path=%q: %v", sessionID, dir, err)
		return nil, err
	}
	logger.Debugf(ctx, "[LocalSandbox] list session=%s path=%s", sessionID, resolved)

	var out []sandbox.RemoteDirEntry
	err = filepath.WalkDir(resolved, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if len(out) >= maxListSessionFiles {
			return filepath.SkipAll
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, sandbox.RemoteDirEntry{
			Name:    d.Name(),
			Path:    p,
			Type:    sandbox.RemoteEntryFile,
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
		return nil
	})
	if err != nil {
		logger.Warnf(ctx, "[LocalSandbox] list session=%s path=%s: %v", sessionID, resolved, err)
		return nil, err
	}
	logger.Debugf(ctx, "[LocalSandbox] listed session=%s path=%s entries=%d", sessionID, resolved, len(out))
	return out, nil
}

// StatSessionFile returns metadata for p when the policy allows reading it.
func (a *Adapter) StatSessionFile(
	ctx context.Context, sessionID, p string,
) (*sandbox.RemoteStatEntry, error) {
	guard, ws, err := a.guard(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	target := absoluteIn(ws.Root, p)
	info, err := guard.Lstat(target)
	if err != nil {
		if errors.Is(err, localsandbox.ErrPathDenied) {
			logger.Warnf(ctx, "[LocalSandbox] stat denied session=%s path=%q: %v", sessionID, p, err)
		}
		return nil, err
	}
	entryType := sandbox.RemoteEntryFile
	if info.IsDir() {
		entryType = sandbox.RemoteEntryDir
	}
	return &sandbox.RemoteStatEntry{
		Path: target, Type: entryType, Size: info.Size(), ModTime: info.ModTime(),
	}, nil
}

// ReadSessionFile returns the contents of p when the policy allows reading it.
func (a *Adapter) ReadSessionFile(ctx context.Context, sessionID, p string) ([]byte, error) {
	guard, ws, err := a.guard(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	target := absoluteIn(ws.Root, p)
	data, err := guard.ReadFile(target)
	if err != nil {
		if errors.Is(err, localsandbox.ErrPathDenied) {
			logger.Warnf(ctx, "[LocalSandbox] read denied session=%s path=%q: %v", sessionID, p, err)
		}
		return nil, err
	}
	logger.Debugf(ctx, "[LocalSandbox] read session=%s path=%s", sessionID, target)
	return data, nil
}

// WriteSessionWorkspaceFile writes content to p when the policy allows it.
func (a *Adapter) WriteSessionWorkspaceFile(
	ctx context.Context, sessionID, p string, content []byte,
) error {
	guard, ws, err := a.guard(ctx, sessionID)
	if err != nil {
		return err
	}
	unlock := a.svc.LockRoot(ws.Root)
	defer unlock()
	target := absoluteIn(ws.Root, p)
	if err := guard.WriteFile(target, content, 0o644); err != nil {
		if errors.Is(err, localsandbox.ErrPathDenied) {
			logger.Warnf(ctx, "[LocalSandbox] write denied session=%s path=%q: %v", sessionID, p, err)
		} else {
			logger.Errorf(ctx, "[LocalSandbox] write session=%s path=%s: %v", sessionID, target, err)
		}
		return err
	}
	logger.Infof(ctx, "[LocalSandbox] wrote session=%s path=%s bytes=%d", sessionID, target, len(content))
	return nil
}

// WriteSessionWorkspaceFiles writes each file in turn, stopping at the first error.
func (a *Adapter) WriteSessionWorkspaceFiles(
	ctx context.Context, sessionID string, files []sandbox.SessionWorkspaceFile,
) error {
	for _, f := range files {
		if err := a.WriteSessionWorkspaceFile(ctx, sessionID, f.Path, f.Content); err != nil {
			return err
		}
	}
	return nil
}

// WriteSessionInputFile is a no-op error on host: the work directory is the
// user's own folder, and attachments are not copied into it.
func (a *Adapter) WriteSessionInputFile(
	context.Context, string, string, []byte,
) error {
	return errNoHostAttachmentDir
}

// RemoveSessionInputPath is unused on host: there is no attachment directory to clean.
func (a *Adapter) RemoveSessionInputPath(context.Context, string, string) error {
	return errNoHostAttachmentDir
}

var errNoHostAttachmentDir = fmt.Errorf("localsandbox: this workspace has no attachment directory")

func absoluteIn(root, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(root, p)
}
