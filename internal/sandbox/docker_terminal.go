// Package sandbox implements the Docker adapter's interactive terminal capability.
//
// Docker Engine does not expose envd's PTY service. Its equivalent is a
// TTY-enabled container exec whose hijacked connection carries raw combined
// output and stdin for the lifetime of the shell.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/moby/moby/client"
)

// Compile-time proof that the Docker adapter serves the terminal capability.
var _ RemoteTerminalManager = (*DockerRemoteClient)(nil)

var dockerTerminalExitInspectTimeout = 2 * time.Second

const dockerTerminalExitInspectPoll = 25 * time.Millisecond

// OpenTerminal starts an interactive bash in the container behind handle.
// AttachPID is deliberately ignored: Docker can reconnect the container, but
// POST /exec/{id}/start cannot attach a second stream to a running exec (see
// DOCKER_REATTACH_SPIKE.md).
func (c *DockerRemoteClient) OpenTerminal(
	ctx context.Context,
	handle RemoteSandboxHandle,
	opts RemoteTerminalOptions,
) (RemoteTerminalSession, error) {
	containerID, err := dockerHandleID("OpenTerminal", handle)
	if err != nil {
		return nil, err
	}

	terminalCtx, cancel := context.WithCancel(ctx)
	createOptions := client.ExecCreateOptions{
		TTY:          true,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		ConsoleSize: client.ConsoleSize{
			Height: uint(terminalRows(opts)),
			Width:  uint(terminalCols(opts)),
		},
		WorkingDir: terminalCwd(opts),
		User:       terminalUser(opts),
		Env:        dockerTerminalEnvs(opts),
		Cmd:        dockerTerminalCommand(),
	}
	created, err := c.api.ExecCreate(terminalCtx, containerID, createOptions)
	if err != nil && dockerContainerNotRunning(err) {
		if readyErr := c.ensureRunning(terminalCtx, containerID, "OpenTerminal"); readyErr != nil {
			cancel()
			return nil, readyErr
		}
		created, err = c.api.ExecCreate(terminalCtx, containerID, createOptions)
	}
	if err != nil {
		cancel()
		return nil, dockerError("OpenTerminal", err)
	}
	if created.ID == "" {
		cancel()
		return nil, dockerError("OpenTerminal", errors.New("daemon returned an empty exec ID"))
	}

	attached, err := c.api.ExecAttach(terminalCtx, created.ID, client.ExecAttachOptions{
		TTY: true,
		ConsoleSize: client.ConsoleSize{
			Height: uint(terminalRows(opts)),
			Width:  uint(terminalCols(opts)),
		},
	})
	if err != nil {
		cancel()
		return nil, dockerError("OpenTerminal", err)
	}
	if attached.Conn == nil || attached.Reader == nil {
		attached.Close()
		cancel()
		return nil, dockerError("OpenTerminal", errors.New("daemon returned an incomplete exec stream"))
	}

	session := &dockerTerminalSession{
		client:   c,
		execID:   created.ID,
		attached: attached,
		out:      make(chan RemoteTerminalEvent, terminalOutputBuffer),
		ctx:      terminalCtx,
		cancel:   cancel,
		closedCh: make(chan struct{}),
	}
	go session.pump()
	go session.watchContext()

	if ttl := c.terminalIdleTTL(handle); ttl > 0 {
		startTerminalTTLRefresh(terminalCtx, session.closedCh, ttl, func(refreshCtx context.Context) error {
			return c.refreshActivity(refreshCtx, containerID, "terminal activity")
		})
	}
	return session, nil
}

// dockerTerminalCommand uses only trusted constants. The activity touch is
// part of the same exec, so opening a terminal does not need a second Engine
// round trip. bash -il loads the prompt wiring already installed by the
// standard sandbox image.
func dockerTerminalCommand() []string {
	return []string{
		"/bin/sh", "-c",
		"touch " + dockerActivityMarker + " 2>/dev/null || true; exec /bin/bash -il",
	}
}

func dockerTerminalEnvs(opts RemoteTerminalOptions) []string {
	envs := terminalEnvs(opts)
	if envs == nil {
		envs = map[string]string{"TERM": "xterm-256color"}
	}
	return dockerEnvSlice(envs)
}

func (c *DockerRemoteClient) terminalIdleTTL(handle RemoteSandboxHandle) time.Duration {
	if handle != nil {
		if seconds, err := strconv.Atoi(handle.Metadata()[dockerIdleTTLLabel]); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return c.settings.IdleTTL
}

// dockerTerminalSession bridges one Engine exec stream to the neutral
// RemoteTerminalSession contract. Docker has no same-exec reconnect, so PID
// intentionally returns zero rather than exposing a daemon-host PID as a fake
// provider PTY identifier.
type dockerTerminalSession struct {
	client   *DockerRemoteClient
	execID   string
	attached client.ExecAttachResult

	out      chan RemoteTerminalEvent
	ctx      context.Context
	cancel   context.CancelFunc
	closedCh chan struct{}

	writeMu       sync.Mutex
	closedOnce    sync.Once
	transportOnce sync.Once
}

func (s *dockerTerminalSession) pump() {
	defer close(s.out)
	defer s.signalClosed()
	defer s.closeTransport()

	buffer := make([]byte, 32*1024)
	for {
		n, readErr := s.attached.Reader.Read(buffer)
		if n > 0 {
			chunk := append([]byte(nil), buffer[:n]...)
			emitTerminalEvent(s.out, s.closedCh, RemoteTerminalEvent{Data: chunk})
		}
		if readErr == nil {
			continue
		}
		s.reportStreamEnd(readErr)
		return
	}
}

func (s *dockerTerminalSession) reportStreamEnd(readErr error) {
	if s.isClosed() || s.ctx.Err() != nil {
		return
	}
	if !errors.Is(readErr, io.EOF) {
		emitTerminalEvent(s.out, s.closedCh, RemoteTerminalEvent{
			Err: dockerError("terminal stream", readErr),
		})
		return
	}

	inspected, err := s.waitForExit()
	if err != nil {
		if s.isClosed() || s.ctx.Err() != nil {
			return
		}
		emitTerminalEvent(s.out, s.closedCh, RemoteTerminalEvent{
			Err: dockerError("terminal inspect", err),
		})
		return
	}
	if inspected.Running {
		emitTerminalEvent(s.out, s.closedCh, RemoteTerminalEvent{Err: &RemoteError{
			Kind:     RemoteErrorKindUnavailable,
			Provider: SandboxTypeDocker,
			Op:       "terminal stream",
			Message:  "terminal transport ended while the Docker exec is still running",
			Cause:    readErr,
		}})
		return
	}
	emitTerminalEvent(s.out, s.closedCh, RemoteTerminalEvent{
		Exited:   true,
		ExitCode: inspected.ExitCode,
	})
}

func (s *dockerTerminalSession) waitForExit() (client.ExecInspectResult, error) {
	inspectCtx, cancel := context.WithTimeout(s.ctx, dockerTerminalExitInspectTimeout)
	defer cancel()
	for {
		inspected, err := s.client.api.ExecInspect(inspectCtx, s.execID, client.ExecInspectOptions{})
		if err != nil || !inspected.Running {
			return inspected, err
		}
		timer := time.NewTimer(dockerTerminalExitInspectPoll)
		select {
		case <-inspectCtx.Done():
			timer.Stop()
			return inspected, fmt.Errorf("wait for Docker terminal exit: %w", inspectCtx.Err())
		case <-s.closedCh:
			timer.Stop()
			return inspected, context.Canceled
		case <-timer.C:
		}
	}
}

func (s *dockerTerminalSession) watchContext() {
	select {
	case <-s.ctx.Done():
		_ = s.Close()
	case <-s.closedCh:
	}
}

func (s *dockerTerminalSession) signalClosed() {
	s.closedOnce.Do(func() {
		close(s.closedCh)
		s.cancel()
	})
}

func (s *dockerTerminalSession) closeTransport() {
	s.transportOnce.Do(s.attached.Close)
}

func (s *dockerTerminalSession) isClosed() bool {
	select {
	case <-s.closedCh:
		return true
	default:
		return false
	}
}

func (s *dockerTerminalSession) Output() <-chan RemoteTerminalEvent { return s.out }

func (s *dockerTerminalSession) PID() uint32 { return 0 }

func (s *dockerTerminalSession) Write(ctx context.Context, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.isClosed() {
		return dockerInvalidRequest("terminal input", "terminal is closed")
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.isClosed() {
		return dockerInvalidRequest("terminal input", "terminal is closed")
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := s.attached.Conn.SetWriteDeadline(deadline); err != nil {
			return dockerError("terminal input", err)
		}
		defer func() { _ = s.attached.Conn.SetWriteDeadline(time.Time{}) }()
	}
	for len(data) > 0 {
		n, err := s.attached.Conn.Write(data)
		if err != nil {
			return dockerError("terminal input", err)
		}
		if n <= 0 {
			return dockerError("terminal input", io.ErrShortWrite)
		}
		data = data[n:]
	}
	return nil
}

func (s *dockerTerminalSession) Resize(ctx context.Context, cols, rows uint32) error {
	if cols == 0 || rows == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.isClosed() {
		return dockerInvalidRequest("terminal resize", "terminal is closed")
	}
	_, err := s.client.api.ExecResize(ctx, s.execID, client.ExecResizeOptions{
		Height: uint(rows),
		Width:  uint(cols),
	})
	if err != nil {
		return dockerError("terminal resize", err)
	}
	return nil
}

// Close tears down only the local Engine stream. The real-daemon spike proves
// that Docker leaves the exec running but offers no second attach transport;
// the terminal capability advertises that limitation and the activity
// heartbeat stops so the existing container idle lifecycle remains bounded.
func (s *dockerTerminalSession) Close() error {
	s.signalClosed()
	s.closeTransport()
	return nil
}

var _ RemoteTerminalSession = (*dockerTerminalSession)(nil)
