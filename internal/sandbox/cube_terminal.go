// Package sandbox: interactive terminal capability for the Cube adapter.
//
// The Cube SDK wraps envd's PTY service (sandbox.Pty) and hands back a
// PtyHandle with an Output channel, so the adapter's job is mostly
// translating between the neutral RemoteTerminal* types and the SDK's.
package sandbox

import (
	"context"
	"strings"
	"sync"
	"time"

	cubesandbox "github.com/tencentcloud/CubeSandbox/sdk/go"
)

// Compile-time proof that the Cube adapter serves the terminal capability.
var _ RemoteTerminalManager = (*CubeRemoteClient)(nil)

// cubeTerminalTimeout bounds the envd stream server-side (Connect-Timeout-Ms)
// and client-side (idle abort). The SDK treats <= 0 as "use its 60s default",
// which would silently kill idle terminals, so WeKnora always passes an
// explicit long window. A session hitting it ends; the frontend reconnects
// and reattaches by PID when the shell is still running.
const cubeTerminalTimeout = 24 * time.Hour

func cubeSandboxTTL(cfg *Config) time.Duration {
	if cfg != nil && cfg.CubeSandboxTTL > 0 {
		return cfg.CubeSandboxTTL
	}
	return DefaultCubeSandboxTTL
}

// OpenTerminal opens an interactive shell PTY inside the sandbox behind
// handle. The returned session keeps streaming until the shell exits, the
// context passed to OpenTerminal is cancelled (the session derives its own
// lifetime from it), or Close is called.
func (c *CubeRemoteClient) OpenTerminal(
	ctx context.Context,
	handle RemoteSandboxHandle,
	opts RemoteTerminalOptions,
) (RemoteTerminalSession, error) {
	if _, err := cubeHandleSandbox("OpenTerminal", handle); err != nil {
		return nil, err
	}
	sandboxID := strings.TrimSpace(handle.ID())

	// Reattach rather than reuse the handle's sandbox so the PTY rides a
	// freshly resolved envd endpoint. The client carries no HTTP-level
	// timeout, and cubeFilesystemTransport exempts /process.Process/, so the
	// stream body is bounded by ctx alone rather than CubeHTTPTimeout.
	sb, err := c.client.Connect(ctx, sandboxID)
	if err != nil {
		return nil, normalizeCubeError("OpenTerminal", err)
	}
	if sb.TrafficAccessToken == "" {
		sb.TrafficAccessToken = handleTrafficAccessToken(handle)
	}

	// The caller's ctx governs the stream (the SDK derives its internal
	// stream context from it); explicit teardown goes through Close.
	ptyHandle, err := openCubePty(ctx, sb, opts)
	if err != nil {
		return nil, normalizeCubeError("OpenTerminal", err)
	}

	session := &cubeTerminalSession{
		sb:       sb,
		handle:   ptyHandle,
		pid:      ptyHandle.PID(),
		out:      make(chan RemoteTerminalEvent, terminalOutputBuffer),
		closedCh: make(chan struct{}),
		ttl:      cubeSandboxTTL(c.config),
	}
	go session.pump()
	startTerminalTTLRefresh(ctx, session.closedCh, session.ttl, func(rctx context.Context) error {
		if session.sb == nil {
			return nil
		}
		return session.sb.SetTimeout(rctx, session.ttl)
	})
	return session, nil
}

func openCubePty(
	ctx context.Context,
	sb *cubesandbox.Sandbox,
	opts RemoteTerminalOptions,
) (*cubesandbox.PtyHandle, error) {
	size := cubesandbox.PtySize{
		Rows: int(terminalRows(opts)),
		Cols: int(terminalCols(opts)),
	}
	createOpts := cubesandbox.PtyCreateOptions{
		User:    terminalUser(opts),
		Cwd:     terminalCwd(opts),
		Envs:    terminalEnvs(opts),
		Timeout: cubeTerminalTimeout,
	}
	if opts.AttachPID > 0 {
		handle, err := sb.Pty().Connect(ctx, int(opts.AttachPID), cubesandbox.PtyConnectOptions{
			Timeout: cubeTerminalTimeout,
		})
		if err == nil {
			return handle, nil
		}
	}
	return sb.Pty().Create(ctx, size, createOpts)
}

// cubeTerminalSession bridges one Cube PtyHandle to the neutral
// RemoteTerminalSession contract. The SDK's Output channel is closed when
// the stream ends, and Wait also surfaces exit code / error state — pump
// uses Wait so the terminal end is reported exactly once.
type cubeTerminalSession struct {
	sb     *cubesandbox.Sandbox
	handle *cubesandbox.PtyHandle
	pid    int

	out       chan RemoteTerminalEvent
	closedCh  chan struct{}
	closeOnce sync.Once
	ttl       time.Duration
	// no cancel field: the SDK's handle owns its stream context and
	// Disconnect tears it down.
}

func (s *cubeTerminalSession) pump() {
	defer close(s.out)

	// Every send goes through emitTerminalEvent: Close() can abandon the
	// consumer at any point, and a bare send would strand this goroutine.
	code, err := s.handle.Wait(func(chunk []byte) {
		emitTerminalEvent(s.out, s.closedCh, RemoteTerminalEvent{Data: chunk})
	})
	select {
	case <-s.closedCh:
		return
	default:
	}
	if err != nil {
		emitTerminalEvent(s.out, s.closedCh,
			RemoteTerminalEvent{Err: normalizeCubeError("terminal stream", err)})
		return
	}
	emitTerminalEvent(s.out, s.closedCh, RemoteTerminalEvent{Exited: true, ExitCode: code})
}

func (s *cubeTerminalSession) Output() <-chan RemoteTerminalEvent { return s.out }

func (s *cubeTerminalSession) PID() uint32 { return uint32(s.pid) }

func (s *cubeTerminalSession) Write(ctx context.Context, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if err := s.handle.SendStdin(ctx, data); err != nil {
		return normalizeCubeError("terminal input", err)
	}
	return nil
}

func (s *cubeTerminalSession) Resize(ctx context.Context, cols, rows uint32) error {
	if cols == 0 || rows == 0 {
		return nil
	}
	if err := s.handle.Resize(ctx, cubesandbox.PtySize{Rows: int(rows), Cols: int(cols)}); err != nil {
		return normalizeCubeError("terminal resize", err)
	}
	return nil
}

// Close disconnects WeKnora from the PTY without killing the remote shell,
// leaving it reattachable via Pty.Connect. Wait unblocks through the
// disconnect without recording a stream error.
func (s *cubeTerminalSession) Close() error {
	// Signal the pump before disconnecting so a blocked onData send cannot
	// race the stream teardown. sync.Once rather than a select/default probe
	// on closedCh: that check-then-close is not atomic, so two concurrent
	// Close calls could both reach close() and panic.
	s.closeOnce.Do(func() { close(s.closedCh) })
	return s.handle.Disconnect()
}
