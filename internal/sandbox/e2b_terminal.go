// Package sandbox: interactive terminal capability for the E2B adapter.
//
// go-e2b wraps envd's PTY service (sandbox.Pty). Output is delivered through
// a Wait-driven callback, so the adapter pumps it into a channel from a
// dedicated goroutine; input and resize are plain unary RPCs on the same
// service.
package sandbox

import (
	"context"
	"errors"
	"strings"
	"sync"

	e2b "github.com/matiasinsaurralde/go-e2b"
)

// Compile-time proof that the E2B adapter serves the terminal capability.
var _ RemoteTerminalManager = (*E2BRemoteClient)(nil)

// OpenTerminal opens an interactive shell PTY inside the sandbox behind
// handle. The returned session keeps streaming until the shell exits, the
// context passed to OpenTerminal is cancelled (the session derives its own
// lifetime from it), or Close is called.
func (c *E2BRemoteClient) OpenTerminal(
	ctx context.Context,
	handle RemoteSandboxHandle,
	opts RemoteTerminalOptions,
) (RemoteTerminalSession, error) {
	if _, err := e2bHandleSandbox("OpenTerminal", handle); err != nil {
		return nil, err
	}
	sandboxID := strings.TrimSpace(handle.ID())

	// Reattach rather than reuse the handle's sandbox: the connect refreshes
	// the sandbox lifetime exactly like RemoteSandboxClient.Connect does,
	// which doubles as the activity signal for TTL-based reclamation. The
	// client carries no HTTP-level timeout, and e2bRPCTimeoutTransport
	// exempts the PTY's Start/Connect routes, so the stream is bounded by
	// ptyCtx alone rather than by E2BHTTPTimeout.
	timeoutSeconds, err := e2bTimeoutSeconds(RemoteTimeoutPolicy{Mode: RemoteTimeoutServerDefault}, c.timeout)
	if err != nil {
		return nil, normalizeE2BError("OpenTerminal", err)
	}
	sbx, err := c.client.Connect(ctx, sandboxID, timeoutSeconds)
	if err != nil {
		return nil, normalizeE2BError("OpenTerminal", err)
	}
	if sbx.TrafficAccessToken == "" {
		sbx.TrafficAccessToken = handleTrafficAccessToken(handle)
	}
	c.inboundTokens.Put(sbx.ID, sbx.TrafficAccessToken)

	// The terminal session — not the SDK's per-command timeout — owns the
	// PTY lifetime: WithPtyTimeout(0) disables the context wrapping so the
	// stream survives as long as ptyCtx below.
	ptyOpts := []e2b.PtyOption{
		e2b.WithPtyTimeout(0),
		e2b.WithPtyCwd(terminalCwd(opts)),
		e2b.WithPtyUser(terminalUser(opts)),
	}
	if envs := terminalEnvs(opts); len(envs) > 0 {
		ptyOpts = append(ptyOpts, e2b.WithPtyEnv(envs))
	}

	ptyCtx, cancel := context.WithCancel(ctx)
	cmdHandle, err := openE2BPty(ptyCtx, sbx, opts, ptyOpts)
	if err != nil {
		cancel()
		return nil, normalizeE2BError("OpenTerminal", err)
	}

	session := &e2bTerminalSession{
		sbx:            sbx,
		handle:         cmdHandle,
		pid:            cmdHandle.PID(),
		out:            make(chan RemoteTerminalEvent, terminalOutputBuffer),
		ctx:            ptyCtx,
		cancel:         cancel,
		closedCh:       make(chan struct{}),
		timeoutSeconds: timeoutSeconds,
	}
	// envd SendInput is a unary RPC. Waiting for it on the WebSocket read
	// path makes typing feel one-RTT-per-key; E2B's data plane is far
	// enough that that is noticeable. Queue and coalesce instead.
	session.input = newPtyInputCoalescer(ptyCtx, session.closedCh, func(rctx context.Context, data []byte) error {
		return session.sbx.Pty.SendInput(rctx, session.pid, data)
	})
	go session.pump()
	if timeoutSeconds > 0 {
		startTerminalTTLRefresh(ptyCtx, session.closedCh, c.timeout, func(rctx context.Context) error {
			return session.sbx.SetTimeoutWithContext(rctx, session.timeoutSeconds)
		})
	}
	return session, nil
}

func openE2BPty(
	ctx context.Context,
	sbx *e2b.Sandbox,
	opts RemoteTerminalOptions,
	ptyOpts []e2b.PtyOption,
) (*e2b.CommandHandle, error) {
	if opts.AttachPID > 0 {
		handle, err := sbx.Pty.Connect(ctx, opts.AttachPID, ptyOpts...)
		if err == nil {
			return handle, nil
		}
	}
	return sbx.Pty.Create(ctx, terminalCols(opts), terminalRows(opts), ptyOpts...)
}

// e2bTerminalSession bridges one go-e2b PTY handle to the neutral
// RemoteTerminalSession contract.
type e2bTerminalSession struct {
	sbx    *e2b.Sandbox
	handle *e2b.CommandHandle
	pid    uint32

	out            chan RemoteTerminalEvent
	ctx            context.Context
	cancel         context.CancelFunc
	closedCh       chan struct{}
	closeOnce      sync.Once
	timeoutSeconds int
	input          *ptyInputCoalescer
}

// pump drains the PTY event stream. go-e2b dispatches output only while
// Wait runs, so the Wait call lives here for the whole session. The output
// channel closes exactly once, when pump returns.
func (s *e2bTerminalSession) pump() {
	defer close(s.out)

	result, err := s.handle.Wait(s.ctx, e2b.WithWaitOnPty(func(chunk []byte) {
		s.emit(RemoteTerminalEvent{Data: chunk})
	}))

	// Close (or OpenTerminal-context cancellation) tears the stream down;
	// that is teardown, not a terminal failure — report nothing further.
	select {
	case <-s.closedCh:
		return
	default:
	}

	if err != nil {
		var exitErr *e2b.CommandExitError
		if errors.As(err, &exitErr) {
			// The shell exited with a non-zero status. That is still a
			// normal terminal end from the consumer's point of view.
			s.emit(RemoteTerminalEvent{Exited: true, ExitCode: exitErr.ExitCode})
			return
		}
		s.emit(RemoteTerminalEvent{Err: normalizeE2BError("terminal stream", err)})
		return
	}
	code := -1
	if result != nil {
		code = result.ExitCode
	}
	s.emit(RemoteTerminalEvent{Exited: true, ExitCode: code})
}

func (s *e2bTerminalSession) emit(event RemoteTerminalEvent) {
	emitTerminalEvent(s.out, s.closedCh, event)
}

func (s *e2bTerminalSession) Output() <-chan RemoteTerminalEvent { return s.out }

func (s *e2bTerminalSession) PID() uint32 { return s.pid }

func (s *e2bTerminalSession) Write(ctx context.Context, data []byte) error {
	if s.input == nil {
		if len(data) == 0 {
			return nil
		}
		if err := s.sbx.Pty.SendInput(ctx, s.pid, data); err != nil {
			return normalizeE2BError("terminal input", err)
		}
		return nil
	}
	if err := s.input.Write(ctx, data); err != nil {
		return normalizeE2BError("terminal input", err)
	}
	return nil
}

func (s *e2bTerminalSession) Resize(ctx context.Context, cols, rows uint32) error {
	if cols == 0 || rows == 0 {
		return nil
	}
	if err := s.sbx.Pty.Resize(ctx, s.pid, cols, rows); err != nil {
		return normalizeE2BError("terminal resize", err)
	}
	return nil
}

// Close disconnects WeKnora from the PTY without killing the remote shell,
// leaving it reattachable via Pty.Connect. The pump goroutine observes the
// disconnect and closes the output channel silently.
func (s *e2bTerminalSession) Close() error {
	s.closeOnce.Do(func() {
		close(s.closedCh)
		s.cancel()
	})
	return s.handle.Disconnect()
}
