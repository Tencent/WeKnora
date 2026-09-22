// Package service provides lazy startup and dialling for the sandbox graphical desktop.
//
// The desktop is not started with the sandbox: E2B runs envd as init and never
// executes the image CMD, and Cube's ENTRYPOINT is already envd's. One
// idempotent script driven over Exec covers both, heals a sandbox after
// pause/resume, and costs nothing for sessions that never open the tab.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// desktopStartTimeout must exceed start-desktop.sh's own ~40s ceiling
// (10s waiting for X, 30s waiting for websockify). Cutting it shorter kills
// the Exec while the script still holds its flock, and the next attempt then
// waits on a lock nobody will release until the lease expires.
const desktopStartTimeout = 60 * time.Second

const desktopProbeTimeout = 15 * time.Second

var (
	// ErrDesktopUnsupported means this session can never show a desktop as
	// currently configured: either the backend cannot relay one, or the
	// config's image has no start-desktop.sh.
	ErrDesktopUnsupported = errors.New("sandbox desktop is not supported")

	// ErrDesktopStartFailed means the image is right but the desktop did not
	// come up. Retryable.
	ErrDesktopStartFailed = errors.New("sandbox desktop failed to start")
)

// SessionDesktop is one live desktop relay leg to a sandbox.
type SessionDesktop struct {
	Conn    *websocket.Conn
	Backend sandbox.SandboxType

	// SandboxID is what the handler compares against the previous desktop of
	// this session to detect a rebuild. Without it a skill install silently
	// swaps the desktop out from under the user.
	SandboxID string

	// IdleDisconnect is the workspace's terminal idle window, reused for the
	// desktop: both exist so a sandbox can reach its provider TTL once the
	// human walked away. Zero means the handler should use the built-in default.
	IdleDisconnect time.Duration

	// StartTTLRefresh extends the provider idle timeout while the relay
	// ctx is live. Nil when the backend has no timeout to refresh.
	StartTTLRefresh func(ctx context.Context)
}

// SandboxDesktopService opens desktops on session sandboxes. It shares
// SandboxTerminalService's dependencies deliberately: the desktop must land on
// the exact sandbox the chat turn and the terminal use, which is what makes
// template/snapshot consistency automatic.
type SandboxDesktopService struct {
	terminals *SandboxTerminalService
}

// NewSandboxDesktopService wires the desktop service on top of the terminal
// service's resolution path.
func NewSandboxDesktopService(terminals *SandboxTerminalService) *SandboxDesktopService {
	return &SandboxDesktopService{terminals: terminals}
}

// EnsureSessionDesktop starts the desktop listeners and dials websockify.
//
// A zero provision pin is lookup-only: a running session sandbox is
// attached (and XFCE is lazily started if needed), a paused one returns
// sandbox.ErrSandboxPaused, and a missing one returns
// sandbox.ErrNoLiveSessionSandbox. A non-zero pin is the confirmed-click
// path and may create or resume, the same contract as EnsureSessionTerminal.
func (s *SandboxDesktopService) EnsureSessionDesktop(
	ctx context.Context,
	sessionID string,
	provision SandboxPin,
) (*SessionDesktop, error) {
	if s == nil || s.terminals == nil {
		return nil, sandbox.ErrNoLiveSessionSandbox
	}

	// 1. Resolve. Lookup-only skips create/resume; a confirmed config ID
	//    provisions through the same path a chat turn uses. Either way the
	//    inbound token is registered on THIS replica, which is what keeps
	//    the gateway from answering 401.
	mgr, err := s.resolveRunningManager(ctx, sessionID, provision)
	if err != nil {
		return nil, err
	}

	provider, ok := mgr.(sandbox.SessionDesktopProvider)
	if !ok {
		return nil, ErrDesktopUnsupported
	}
	desktops := provider.SessionDesktopManager()
	if desktops == nil {
		return nil, ErrDesktopUnsupported
	}

	exec, ok := mgr.(sessionShellOptionsExecutor)
	if !ok {
		return nil, ErrDesktopUnsupported
	}

	secret, err := readDesktopSecret(ctx, exec, sessionID)
	if err != nil {
		return nil, err
	}

	// Dial. The secret stays in memory for exactly one handshake and is
	// never sent to the browser.
	dialed, err := desktops.OpenSessionDesktop(ctx, sessionID, sandbox.RemoteDesktopOptions{
		BasicAuthUser:     sandbox.DesktopBasicAuthUser,
		BasicAuthPassword: secret,
	})
	if err != nil {
		if errors.Is(err, sandbox.ErrDesktopUnsupported) {
			return nil, ErrDesktopUnsupported
		}
		return nil, err
	}
	return &SessionDesktop{
		Conn:            dialed.Conn,
		Backend:         mgr.GetType(),
		SandboxID:       dialed.SandboxID,
		IdleDisconnect:  terminalIdleDisconnectFromManager(mgr),
		StartTTLRefresh: dialed.StartTTLRefresh,
	}, nil
}

// resolveRunningManager returns a manager whose session sandbox is live.
// With a zero provision pin it will not create or resume: a paused binding
// is ErrSandboxPaused so the overlay can ask first. DesktopEnabled is
// checked before provision (and before Exec in EnsureSessionDesktop) so a
// CLI image cannot be created or woken only to miss start-desktop.sh.
func (s *SandboxDesktopService) resolveRunningManager(
	ctx context.Context,
	sessionID string,
	provision SandboxPin,
) (sandbox.Manager, error) {
	t := s.terminals
	lookupOnly := provision.IsZero()
	mgr, _, err := t.resolveSessionManager(ctx, sessionID)
	if err == nil {
		if err := requireDesktopCapable(mgr); err != nil {
			return nil, err
		}
		if lookupOnly {
			if err := requireRunningSessionSandbox(ctx, mgr, sessionID); err != nil {
				return nil, err
			}
		}
		return mgr, nil
	}
	if !errors.Is(err, sandbox.ErrNoLiveSessionSandbox) || lookupOnly {
		return nil, err
	}
	// provision.TenantID owns provision.ConfigID — the lending workspace for a
	// shared agent, which is where its config actually exists.
	sessionTenantID, _ := types.TenantIDFromContext(ctx)
	mgr, _, err = resolveSandboxForExecution(
		ctx, t.resolver, t.fallback, t.pinner,
		provision.TenantOr(sessionTenantID), sessionID, provision.ConfigID, t.policy,
	)
	if err != nil {
		return nil, err
	}
	if mgr == nil || mgr.GetType() == sandbox.SandboxTypeDisabled {
		return nil, sandbox.ErrNoLiveSessionSandbox
	}
	if err := requireDesktopCapable(mgr); err != nil {
		return nil, err
	}
	if err := t.provisionOnManager(ctx, mgr, sessionID); err != nil {
		return nil, err
	}
	return mgr, nil
}

// requireDesktopCapable refuses before Exec or provision: a CLI image has no
// start-desktop.sh, and creating/waking a sandbox only to learn that is a bill.
func requireDesktopCapable(mgr any) error {
	provider, ok := mgr.(sandbox.SessionDesktopProvider)
	if !ok || provider.SessionDesktopManager() == nil {
		return ErrDesktopUnsupported
	}
	return nil
}

// sessionShellOptionsExecutor is SessionBoundManager.ExecShellCommandWithOptions
// without dragging in SessionInstallShellExecutor's install-mode name.
type sessionShellOptionsExecutor interface {
	ExecShellCommandWithOptions(
		context.Context, string, string, sandbox.ShellExecOptions,
	) (*sandbox.ExecuteResult, error)
}

func desktopSkipPrepOpts(timeout time.Duration) sandbox.ShellExecOptions {
	return sandbox.ShellExecOptions{Timeout: timeout, SkipWorkspacePrep: true}
}

func readDesktopSecret(
	ctx context.Context,
	exec sessionShellOptionsExecutor,
	sessionID string,
) (string, error) {
	secret, err := runDesktopEnsure(ctx, exec, sessionID)
	if err == nil {
		return secret, nil
	}
	if errors.Is(err, ErrDesktopUnsupported) {
		return "", err
	}
	logger.Warnf(ctx, "[sandbox-desktop] ensure failed; resetting stack session=%s: %v",
		sessionID, err)
	if _, resetErr := exec.ExecShellCommandWithOptions(ctx, sessionID,
		sandbox.DesktopResetListenersCmd(), desktopSkipPrepOpts(desktopProbeTimeout)); resetErr != nil {
		return "", fmt.Errorf("%w: reset desktop stack: %v", ErrDesktopStartFailed, resetErr)
	}
	return runDesktopEnsure(ctx, exec, sessionID)
}

func runDesktopEnsure(
	ctx context.Context,
	exec sessionShellOptionsExecutor,
	sessionID string,
) (string, error) {
	result, err := exec.ExecShellCommandWithOptions(ctx, sessionID,
		sandbox.DesktopEnsureCmd(), desktopSkipPrepOpts(desktopStartTimeout))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrDesktopStartFailed, err)
	}
	if sandbox.IsDesktopEnsureUnsupported(result) {
		logger.Warnf(ctx,
			"[sandbox-desktop] %s missing in session=%s sandbox; config claims desktop support",
			sandbox.DesktopStartScript, sessionID)
		return "", ErrDesktopUnsupported
	}
	if result == nil || !result.IsSuccess() {
		logger.Warnf(ctx, "[sandbox-desktop] ensure failed session=%s: %s",
			sessionID, desktopExecDiagnostic(result))
		return "", ErrDesktopStartFailed
	}
	secret, ok := sandbox.ParseDesktopEnsureSecret(result.Stdout)
	if !ok {
		logger.Warnf(ctx, "[sandbox-desktop] ensure stdout was not READY session=%s: %s",
			sessionID, desktopExecDiagnostic(result))
		return "", ErrDesktopStartFailed
	}
	return secret, nil
}

// desktopExecDiagnostic trims script output for one log line. Never fall back
// to stdout: a successful ensure prints READY <secret> there. Timeouts have
// empty stdio and put ErrTimeout in Error (remoteExecuteResult).
func desktopExecDiagnostic(result *sandbox.ExecuteResult) string {
	if result == nil {
		return "no result"
	}
	out := strings.TrimSpace(result.Stderr)
	if out == "" && (result.Killed || strings.TrimSpace(result.Error) != "") {
		out = strings.TrimSpace(result.Error)
	}
	if len(out) > 512 {
		out = out[len(out)-512:]
	}
	if result.Killed {
		return fmt.Sprintf("exit=%d killed=true %s", result.ExitCode, out)
	}
	return fmt.Sprintf("exit=%d %s", result.ExitCode, out)
}

type runningSessionSandboxAsserter interface {
	RequireRunningSessionSandbox(context.Context, string) error
}

func requireRunningSessionSandbox(
	ctx context.Context,
	mgr sandbox.Manager,
	sessionID string,
) error {
	if a, ok := mgr.(runningSessionSandboxAsserter); ok {
		return a.RequireRunningSessionSandbox(ctx, sessionID)
	}
	return nil
}
