// Package sandbox: session-scoped capability interfaces.
//
// The Sandbox / Manager pair intentionally hides provider identity (Cube,
// E2B, Docker) from the application layer. Higher layers should never
// branch on Manager.GetType() to decide whether a feature is supported —
// that couples them to a specific backend.
//
// Instead, session-scoped features (shell execution, per-session file
// inspection, attachment staging) are advertised via the capability
// interfaces below. A manager may satisfy the underlying methods yet still
// return nil from the accessors on SessionCapabilityProvider when the
// current runtime configuration cannot honour that capability.
package sandbox

import (
	"context"
	"time"
)

// SessionShellExecutor executes ad-hoc shell commands inside a session-
// scoped remote sandbox. SessionBoundManager surfaces it while Cube, E2B,
// or Docker is active.
type SessionShellExecutor interface {
	ExecShellCommand(
		ctx context.Context,
		sessionID string,
		command string,
		workDir string,
		timeout time.Duration,
		env map[string]string,
	) (*ExecuteResult, error)
}

// SessionFileStore is the effective per-session filesystem view a manager
// offers callers that need to inspect, stage, or clean up files inside the
// session's remote sandbox. It is intentionally provider-neutral: entries
// use RemoteDirEntry / RemoteStatEntry, so E2B and Cube can implement it
// without touching the caller.
type SessionFileStore interface {
	// EnsureSessionDir creates dir in the session's live sandbox. Silent
	// no-op when no sandbox is bound yet; the next Execute call will
	// materialise the directory during script upload.
	EnsureSessionDir(ctx context.Context, sessionID, dir string) error

	// ListSessionFiles walks dir recursively and returns file entries.
	// Returns nil (no error) when the session has no live sandbox so
	// callers can treat "no sandbox" and "empty output" uniformly.
	ListSessionFiles(ctx context.Context, sessionID, dir string) ([]RemoteDirEntry, error)

	// StatSessionFile returns metadata for a single file. Errors when the
	// session has no bound sandbox — callers of this method already hold a
	// path from a prior ListSessionFiles call.
	StatSessionFile(ctx context.Context, sessionID, path string) (*RemoteStatEntry, error)

	// ReadSessionFile downloads a file's contents. Same "no sandbox
	// bound" contract as StatSessionFile.
	ReadSessionFile(ctx context.Context, sessionID, path string) ([]byte, error)

	// WriteSessionInputFile writes a durable attachment path into the
	// session's remote sandbox, provisioning the sandbox on first call.
	WriteSessionInputFile(ctx context.Context, sessionID, filePath string, content []byte) error

	// WriteSessionWorkspaceFile writes a model-authored file under
	// /workspace. /workspace/input stays read-only (attachments); everything
	// else under /workspace is accepted so generated scripts do not have to
	// travel through shell_exec heredocs.
	WriteSessionWorkspaceFile(ctx context.Context, sessionID, filePath string, content []byte) error

	// WriteSessionWorkspaceFiles writes many workspace files after preparing
	// the session layout once. Host-skill staging must use this instead of
	// looping WriteSessionWorkspaceFile.
	WriteSessionWorkspaceFiles(ctx context.Context, sessionID string, files []SessionWorkspaceFile) error

	// RemoveSessionInputPath deletes a staged attachment. No-op when the
	// session has no live sandbox.
	RemoveSessionInputPath(ctx context.Context, sessionID, targetPath string) error
}

// SessionWorkspaceFile is one path/content pair for WriteSessionWorkspaceFiles.
type SessionWorkspaceFile struct {
	Path    string
	Content []byte
}

// SessionCapabilityProvider is implemented by managers that MAY offer
// session-scoped capabilities. Accessors return nil when the current
// runtime configuration cannot support that capability. Application code
// should gate feature registration on non-nil accessor returns.
type SessionCapabilityProvider interface {
	SessionShellExecutor() SessionShellExecutor
	SessionFileStore() SessionFileStore
}

// SessionInstallShellExecutor runs install/maintenance shell commands, which
// need the skills image root. It is a separate interface from
// SessionShellExecutor so reaching outside /workspace is something a caller
// must ask for by name: ordinary chat sessions keep the /workspace-only
// contract even though they already run as root.
type SessionInstallShellExecutor interface {
	ExecShellCommandWithOptions(
		ctx context.Context,
		sessionID string,
		command string,
		opts ShellExecOptions,
	) (*ExecuteResult, error)
}

// SessionFileReader reads one file out of a session's sandbox. It is the
// single-method slice of SessionFileStore that callers which only ever read
// need, so a manager offering just this much is enough for them.
type SessionFileReader interface {
	ReadSessionFile(ctx context.Context, sessionID, path string) ([]byte, error)
}

// SessionDestroyer releases the remote sandbox bound to a session, leaving the
// session record itself alone. Like RemoteSnapshotManager it is an optional
// capability: stateless backends have nothing to release.
type SessionDestroyer interface {
	DestroySession(ctx context.Context, sessionID string) error
}

// SessionInstallCapabilityProvider is implemented by managers that can run
// install-mode shell commands. Like the other accessors it returns nil when
// the current runtime cannot honour the capability.
type SessionInstallCapabilityProvider interface {
	SessionInstallShellExecutor() SessionInstallShellExecutor
}

// SessionTerminalManager opens interactive PTYs on the sandbox bound to a
// session. Like the file store it is provider-neutral: the WebSocket
// handler bridges browser terminal frames to it without knowing whether
// E2B or Cube serves the session.
type SessionTerminalManager interface {
	// OpenSessionTerminal connects to the session's currently bound sandbox
	// and opens a PTY. It is lookup-only: when no live sandbox is bound it
	// returns ErrNoLiveSessionSandbox instead of provisioning one, because
	// the terminal entry point lacks the agent's config-pin context and
	// must not create microVMs as a side effect. A backend that cannot
	// stream PTYs returns ErrTerminalUnsupported, not "no sandbox".
	OpenSessionTerminal(ctx context.Context, sessionID string, opts RemoteTerminalOptions) (RemoteTerminalSession, error)
}

// SessionTerminalProvider is implemented by managers that MAY offer
// interactive terminals. The accessor returns nil when the current runtime
// cannot honour the capability.
type SessionTerminalProvider interface {
	SessionTerminalManager() SessionTerminalManager
}

// SessionTurnHolder marks the start and end of one chat turn on a session's
// sandbox. While the turn is open, a stale image mark waits: the first
// resolve of the turn may rebuild, later resolves of the same turn keep the
// sandbox so /workspace scratch and in-flight execs survive an admin install.
type SessionTurnHolder interface {
	BeginSessionTurn(ctx context.Context, sessionID string) error
	EndSessionTurn(ctx context.Context, sessionID string) error
}
