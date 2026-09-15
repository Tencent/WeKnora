// Package service: session sandbox terminal.
//
// The interactive terminal is a lookup-only entry point into the sandbox
// bound to a session: it resolves the session's pinned config exactly like
// artifact collection does (never "whatever the agent points at today"),
// refuses to provision when no live sandbox exists, and hands a provider-
// neutral PTY handle to the WebSocket layer.
package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// SessionTerminal bundles an opened PTY with the backend that serves it, so
// the WebSocket layer can include the backend in its ready frame.
type SessionTerminal struct {
	Session sandbox.RemoteTerminalSession
	Backend string
	// IdleDisconnect is how long the bridge should wait without PTY
	// activity before closing the socket. Always a positive duration.
	IdleDisconnect time.Duration
}

// SandboxTerminalService opens interactive terminals on session sandboxes.
type SandboxTerminalService struct {
	pinner   *SessionSandboxPinner
	resolver sandbox.TenantSandboxResolver
	fallback sandbox.Manager
	policy   WorkspaceSandboxPolicy
}

// NewSandboxTerminalService wires the terminal service. All dependencies
// are optional individually; with no pinner the service reports "no live
// sandbox" for every session.
func NewSandboxTerminalService(
	pinner *SessionSandboxPinner,
	resolver sandbox.TenantSandboxResolver,
	fallback sandbox.Manager,
	policy WorkspaceSandboxPolicy,
) *SandboxTerminalService {
	return &SandboxTerminalService{
		pinner:   pinner,
		resolver: resolver,
		fallback: fallback,
		policy:   policy,
	}
}

// OpenSessionTerminal attaches to the session's currently bound sandbox
// without ever creating or resuming one. This is the path a panel open and
// an automatic reconnect take: a running sandbox is attached, a bound
// sandbox that is not confirmed running reports ErrSandboxPaused, and a
// missing one reports ErrNoLiveSessionSandbox. Opening a panel must not
// conjure or wake a microVM.
//
// The WebSocket handler turns those errors into SANDBOX_PAUSED /
// SANDBOX_NOT_BOUND frames the UI answers with an explicit button. That
// confirmed click is what calls EnsureSessionTerminal.
//
// Error contract (the WebSocket handler maps these onto protocol error
// frames):
//   - sandbox.ErrNoLiveSessionSandbox — the session has no bound sandbox.
//   - sandbox.ErrSandboxPaused — the bound sandbox is paused; resume needs
//     an explicit click (EnsureSessionTerminal with AllowResume).
//   - ErrTerminalUnsupported — the resolved backend cannot stream PTYs
//     (Docker, disabled manager).
//   - any other error — resolution or provider failure.
func (s *SandboxTerminalService) OpenSessionTerminal(
	ctx context.Context,
	sessionID string,
	opts sandbox.RemoteTerminalOptions,
) (*SessionTerminal, error) {
	opts.AllowResume = false
	mgr, _, err := s.resolveSessionManager(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return s.openOnManager(ctx, mgr, sessionID, opts)
}

// resolveSessionManager mirrors ArtifactCollector.sessionSource: pin first,
// never the agent's current choice.
func (s *SandboxTerminalService) resolveSessionManager(
	ctx context.Context,
	sessionID string,
) (sandbox.Manager, string, error) {
	if s.pinner == nil {
		return nil, "", sandbox.ErrNoLiveSessionSandbox
	}
	configID, err := s.pinner.Read(ctx, sessionID)
	if err != nil {
		return nil, "", err
	}
	if configID == "" {
		return nil, "", sandbox.ErrNoLiveSessionSandbox
	}
	tenantID, _ := types.TenantIDFromContext(ctx)
	mgr, err := resolveTenantSandboxForConfig(
		ctx, s.resolver, s.fallback, tenantID, configID, s.policy,
	)
	if err != nil {
		return nil, configID, err
	}
	if mgr == nil {
		return nil, configID, sandbox.ErrNoLiveSessionSandbox
	}
	return mgr, configID, nil
}

// EnsureSessionTerminal opens a PTY on the session's sandbox, provisioning
// one when the session has none and the caller supplies a sandbox config ID.
//
// Only call this for a confirmed user action. It creates and bills real
// infrastructure, so the lookup-only OpenSessionTerminal is what a page load
// or a background reconnect must use.
//
// Provisioning reuses the exact chat-flow path — the resolved agent's sandbox
// config, the workspace kill switch, and the pin claim in
// resolveSandboxForExecution — so a terminal-created sandbox is
// indistinguishable from one created by a conversation turn. The WebSocket
// handler must resolve the agent the same way a chat turn does (own agent
// or shared agent from another workspace) and pass that config ID here.
// A no-op shell command drives the lazy creation, which also seeds the
// workspace layout.
//
// With no sandboxConfigID the call stays lookup-only and reports
// sandbox.ErrNoLiveSessionSandbox, which the WebSocket handler maps onto
// the SANDBOX_NOT_BOUND guidance frame.
func (s *SandboxTerminalService) EnsureSessionTerminal(
	ctx context.Context,
	sessionID string,
	sandboxConfigID string,
	opts sandbox.RemoteTerminalOptions,
) (*SessionTerminal, error) {
	opts.AllowResume = true
	mgr, _, err := s.resolveSessionManager(ctx, sessionID)
	if err == nil {
		terminal, terr := s.openOnManager(ctx, mgr, sessionID, opts)
		if terr == nil {
			return terminal, nil
		}
		// The pin names the config but the sandbox itself is gone
		// (reclaimed out of band). Resume of a paused instance already
		// happened above via AllowResume. Rebuild only when there is
		// nothing left to connect to.
		if errors.Is(terr, sandbox.ErrNoLiveSessionSandbox) {
			if perr := s.provisionOnManager(ctx, mgr, sessionID); perr == nil {
				if terminal, retryErr := s.openOnManager(ctx, mgr, sessionID, opts); retryErr == nil {
					return terminal, nil
				} else {
					terr = retryErr
				}
			}
		}
		return nil, terr
	}
	if !errors.Is(err, sandbox.ErrNoLiveSessionSandbox) || strings.TrimSpace(sandboxConfigID) == "" {
		return nil, err
	}

	tenantID, _ := types.TenantIDFromContext(ctx)
	mgr, _, err = resolveSandboxForExecution(
		ctx, s.resolver, s.fallback, s.pinner, tenantID, sessionID, strings.TrimSpace(sandboxConfigID), s.policy,
	)
	if err != nil {
		return nil, err
	}
	if mgr == nil || mgr.GetType() == sandbox.SandboxTypeDisabled {
		return nil, sandbox.ErrNoLiveSessionSandbox
	}

	// Drive the lazy session-sandbox creation with a no-op command. This
	// provisions the sandbox AND pins it, exactly like the first agent turn
	// would.
	if err := s.provisionOnManager(ctx, mgr, sessionID); err != nil {
		return nil, err
	}

	return s.openOnManager(ctx, mgr, sessionID, opts)
}

// provisionOnManager drives the manager's lazy session-sandbox creation with
// a no-op shell command. The command both provisions the sandbox and seeds the
// workspace layout, matching what the first agent turn does.
func (s *SandboxTerminalService) provisionOnManager(
	ctx context.Context,
	mgr sandbox.Manager,
	sessionID string,
) error {
	executor, ok := mgr.(sandbox.SessionShellExecutor)
	if !ok {
		// No shell executor means the backend cannot provision (Docker as a
		// session backend, disabled manager): report "no live sandbox"
		// rather than a confusing provisioning failure.
		return sandbox.ErrNoLiveSessionSandbox
	}
	if _, err := executor.ExecShellCommand(ctx, sessionID, "true", "", 60*time.Second, nil); err != nil {
		logger.Warnf(ctx, "[sandbox-terminal] provision sandbox for session %s failed: %v", sessionID, err)
		return err
	}
	return nil
}

// openOnManager opens a terminal on an already-resolved manager.
func (s *SandboxTerminalService) openOnManager(
	ctx context.Context,
	mgr sandbox.Manager,
	sessionID string,
	opts sandbox.RemoteTerminalOptions,
) (*SessionTerminal, error) {
	terminal, ok := terminalManagerFromManager(mgr)
	if !ok {
		logger.Warnf(ctx,
			"[sandbox-terminal] backend %s does not support terminals (session %s)",
			mgr.GetType(), sessionID)
		return nil, ErrTerminalUnsupported
	}
	session, err := terminal.OpenSessionTerminal(ctx, sessionID, opts)
	if err != nil {
		return nil, err
	}
	return &SessionTerminal{
		Session:        session,
		Backend:        string(mgr.GetType()),
		IdleDisconnect: terminalIdleDisconnectFromManager(mgr),
	}, nil
}

func terminalIdleDisconnectFromManager(mgr sandbox.Manager) time.Duration {
	type idleSource interface {
		TerminalIdleDisconnect() time.Duration
	}
	if src, ok := mgr.(idleSource); ok {
		return src.TerminalIdleDisconnect()
	}
	return sandbox.DefaultTerminalIdleDisconnect
}

// terminalManagerFromManager narrows a manager to its terminal capability.
func terminalManagerFromManager(mgr sandbox.Manager) (sandbox.SessionTerminalManager, bool) {
	provider, ok := mgr.(sandbox.SessionTerminalProvider)
	if !ok {
		return nil, false
	}
	terminal := provider.SessionTerminalManager()
	if terminal == nil {
		return nil, false
	}
	return terminal, true
}

// ErrTerminalUnsupported reports that the session's sandbox backend cannot
// serve interactive terminals. Same sentinel as sandbox.ErrTerminalUnsupported
// so errors.Is matches either name.
var ErrTerminalUnsupported = sandbox.ErrTerminalUnsupported
