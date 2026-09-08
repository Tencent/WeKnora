// Package sandbox: interactive terminal (PTY) capability.
//
// The terminal is an optional, provider-neutral capability alongside
// RemoteSnapshotManager. SessionBoundManager surfaces it only when the
// active backend implements PTY streaming; the WebSocket handler in the
// application layer bridges browser terminals to it.
package sandbox

import (
	"context"
	"strings"
	"time"
)

// RemoteTerminalOptions carries the neutral parameters for opening a PTY.
// Adapters translate these into their provider SDK's equivalent knobs.
type RemoteTerminalOptions struct {
	// Cols/Rows is the initial terminal size. Zero falls back to 80x24.
	Cols uint32
	Rows uint32

	// Cwd is the shell's working directory. Empty means the sandbox
	// default (/workspace).
	Cwd string

	// User is the account the shell runs as. Empty means
	// DefaultSandboxExecUser.
	User string

	// Envs are extra environment variables. TERM is always forced to a
	// 256-colour value by the adapters so full-screen programs work.
	Envs map[string]string

	// AttachPID, when non-zero, asks the provider to reattach to an
	// already-running PTY instead of creating a new shell. A failed
	// reattach falls back to Create.
	AttachPID uint32
}

// RemoteTerminalEvent is one event on a terminal's output stream. The
// channel returned by RemoteTerminalSession.Output closes when the terminal
// session ends (process exit, disconnect, or provider error).
type RemoteTerminalEvent struct {
	// Data is raw PTY output bytes. Nil for non-data events.
	Data []byte

	// Exited is true when the remote shell process terminated. ExitCode is
	// meaningful only then.
	Exited bool
	// ExitCode is the shell's exit code (may be unavailable; -1 fallback).
	ExitCode int

	// Err is set when the stream ended abnormally (transport failure,
	// provider error). A normal process exit leaves it nil.
	Err error
}

// RemoteTerminalSession is a neutral handle to one live PTY. Implementations
// MUST be safe for concurrent Write/Resize calls alongside the Output
// consumer goroutine.
type RemoteTerminalSession interface {
	// Output streams PTY output events. The channel is closed exactly once
	// when the terminal session ends; consumers must not send to it.
	Output() <-chan RemoteTerminalEvent

	// PID is the remote shell's process ID, when the provider exposes one.
	PID() uint32

	// Write feeds raw bytes (keystrokes) into the PTY.
	Write(ctx context.Context, data []byte) error

	// Resize changes the PTY window size.
	Resize(ctx context.Context, cols, rows uint32) error

	// Close disconnects WeKnora from the PTY without killing the remote
	// process: the shell stays in the sandbox so a provider-native
	// reconnect can re-attach. Safe to call more than once.
	Close() error
}

// emitTerminalEvent hands one event to the Output consumer, giving up as
// soon as teardown starts.
//
// Adapters MUST route every send through it. A bare send races Close: once
// the consumer has stopped draining (it returns on the first error/exit
// event) and the buffer is full, the send blocks forever, the pump goroutine
// never reaches its `defer close(out)`, and the provider stream leaks with
// it.
func emitTerminalEvent(
	out chan<- RemoteTerminalEvent,
	closed <-chan struct{},
	event RemoteTerminalEvent,
) {
	select {
	case out <- event:
	case <-closed:
	}
}

// terminalOutputBuffer bounds queued PTY output between pump dispatches. A
// slow consumer applies backpressure to the envd stream rather than losing
// bytes.
const terminalOutputBuffer = 256

// The terminal*(opts) helpers below resolve one neutral option to the value
// an adapter should hand its SDK. They live here, not in a provider file,
// because every adapter needs them: removing one backend must not take the
// other's defaults with it.

func terminalCols(opts RemoteTerminalOptions) uint32 {
	if opts.Cols > 0 {
		return opts.Cols
	}
	return 80
}

func terminalRows(opts RemoteTerminalOptions) uint32 {
	if opts.Rows > 0 {
		return opts.Rows
	}
	return 24
}

func terminalCwd(opts RemoteTerminalOptions) string {
	if cwd := strings.TrimSpace(opts.Cwd); cwd != "" {
		return cwd
	}
	return "/workspace"
}

func terminalUser(opts RemoteTerminalOptions) string {
	if user := strings.TrimSpace(opts.User); user != "" {
		return user
	}
	return DefaultSandboxExecUser
}

// terminalEnvs merges caller envs over the interactive defaults the SDK
// already applies (TERM/LANG/LC_ALL), never downgrading TERM.
func terminalEnvs(opts RemoteTerminalOptions) map[string]string {
	if len(opts.Envs) == 0 {
		return nil
	}
	merged := make(map[string]string, len(opts.Envs)+1)
	for key, value := range opts.Envs {
		merged[key] = value
	}
	if merged["TERM"] == "" {
		merged["TERM"] = "xterm-256color"
	}
	return merged
}

// handleTrafficAccessToken extracts the traffic token without assuming a
// concrete handle type beyond the optional carrier interface.
func handleTrafficAccessToken(handle RemoteSandboxHandle) string {
	carrier, ok := handle.(RemoteInboundTokenCarrier)
	if !ok {
		return ""
	}
	return carrier.TrafficAccessToken()
}

// RemoteTerminalManager is the optional capability interface for backends
// that can open interactive PTYs inside a running sandbox. Mirrors
// RemoteSnapshotManager: advertised via RemoteSandboxCapabilities
// (SupportsTerminals) and narrowed with TerminalManagerFrom.
type RemoteTerminalManager interface {
	// OpenTerminal opens a new PTY inside the sandbox behind handle.
	OpenTerminal(ctx context.Context, handle RemoteSandboxHandle, opts RemoteTerminalOptions) (RemoteTerminalSession, error)
}

// TerminalManagerFrom narrows a client to its terminal capability. It
// returns false for providers that cannot stream PTYs, so callers can
// report an unsupported-backend error instead of failing mid-session.
//
// Both signals must agree: the type assertion finds the methods, and
// SupportsTerminals is the advertised capability (see SnapshotManagerFrom
// for the rationale).
func TerminalManagerFrom(client RemoteSandboxClient) (RemoteTerminalManager, bool) {
	if client == nil {
		return nil, false
	}
	mgr, ok := client.(RemoteTerminalManager)
	if !ok {
		return nil, false
	}
	if !client.Capabilities().SupportsTerminals {
		return nil, false
	}
	return mgr, true
}

// DefaultTerminalIdleDisconnect is how long an interactive terminal may sit
// without keystrokes or PTY output before WeKnora closes the WebSocket.
// Closing the socket stops TTL refresh so the sandbox can pause on its own
// provider timeout. 0 in a stored workspace config means this default.
const DefaultTerminalIdleDisconnect = 15 * time.Minute

const (
	minTerminalIdleDisconnect = 60 * time.Second
	maxTerminalIdleDisconnect = 24 * time.Hour
)

// EffectiveTerminalIdleDisconnect clamps a stored workspace value onto the
// range the terminal bridge will honour. 0 (unset) uses the built-in default
// so existing configs get an idle disconnect without a migration.
func EffectiveTerminalIdleDisconnect(d time.Duration) time.Duration {
	if d <= 0 {
		return DefaultTerminalIdleDisconnect
	}
	if d < minTerminalIdleDisconnect {
		return minTerminalIdleDisconnect
	}
	if d > maxTerminalIdleDisconnect {
		return maxTerminalIdleDisconnect
	}
	return d
}

// terminalTTLRefreshMin is the floor for how often an open terminal
// refreshes the provider sandbox idle timeout. Tests lower it.
var terminalTTLRefreshMin = 15 * time.Second

const terminalTTLRefreshMax = 2 * time.Minute

func terminalTTLRefreshInterval(ttl time.Duration) time.Duration {
	interval := ttl / 3
	if interval < terminalTTLRefreshMin {
		return terminalTTLRefreshMin
	}
	if interval > terminalTTLRefreshMax {
		return terminalTTLRefreshMax
	}
	return interval
}

func startTerminalTTLRefresh(
	ctx context.Context,
	closed <-chan struct{},
	ttl time.Duration,
	refresh func(context.Context) error,
) {
	if refresh == nil || ttl <= 0 {
		return
	}
	interval := terminalTTLRefreshInterval(ttl)
	go func() {
		doRefresh := func() {
			rctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			_ = refresh(rctx)
			cancel()
		}
		// Cube Connect does not bump idle TTL; refresh immediately so a
		// terminal opened near expiry is not waiting a full interval.
		select {
		case <-closed:
			return
		case <-ctx.Done():
			return
		default:
			doRefresh()
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-closed:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				doRefresh()
			}
		}
	}()
}
