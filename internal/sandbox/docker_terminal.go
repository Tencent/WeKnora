// Interactive terminal (PTY) capability for the Docker adapter.
//
// Cube and E2B ride envd's PTY service, so their adapters translate an SDK
// handle. Docker has no such service: the PTY is a plain `exec` created with
// TTY=true whose hijacked connection is a raw byte stream, and window resizes
// go through a separate Engine call. This file bridges that to the neutral
// RemoteTerminalSession contract.
//
// One structural difference from envd: a Docker exec cannot be re-attached.
// /exec/{id}/start runs once and the Engine API has no exec attach endpoint,
// so RemoteTerminalOptions.AttachPID is ignored and every reconnecting browser
// gets a fresh shell. It also cannot be left running: unlike Cube/E2B, closing
// the hijacked connection does NOT end the shell (verified against a live
// daemon — an idle bash and its foreground jobs survive), so abandoning it
// would strand processes in the container. Close therefore terminates the
// shell and every process it started, explicitly. See dockerTerminalKillCmd.

package sandbox

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/moby/moby/client"
)

// Compile-time proof that the Docker adapter serves the terminal capability.
var _ RemoteTerminalManager = (*DockerRemoteClient)(nil)

const (
	// dockerTerminalActivityTimeout bounds the marker-refresh exec. It is a
	// trivial `true`, so this only has to outlast a round trip.
	dockerTerminalActivityTimeout = 15 * time.Second
	// dockerTerminalInspectTimeout bounds the exit-code read that follows the
	// stream ending.
	dockerTerminalInspectTimeout = 5 * time.Second
	// dockerTerminalKillTimeout bounds the best-effort terminate exec issued
	// when a terminal is closed while its shell is still alive.
	dockerTerminalKillTimeout = 10 * time.Second
)

// dockerTerminalIDEnv tags every process of one terminal. The PTY exec gets
// this variable set to a per-open token, and every process it starts inherits
// it — which is what lets teardown find and kill the shell's whole tree from
// inside the container, without having to map ExecInspect's host PID into the
// container's PID namespace.
const dockerTerminalIDEnv = "WEKNORA_TERMINAL_ID"

// dockerTerminalToken mints a per-open marker. It only has to distinguish
// concurrent terminals in one container, so a random 16-byte hex string is
// more than enough and stays shell-safe.
func dockerTerminalToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is a broken host; a time-based token still
		// separates concurrent opens well enough for a best-effort kill.
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

// dockerTerminalEnvs is the PTY's environment. Cube and E2B get TERM, LANG
// and LC_ALL from their SDK; a Docker exec gets nothing but the daemon's
// TERM=xterm, which loses 256 colours and, on an image without a LANG, breaks
// multibyte input. The defaults below mirror the E2B SDK, caller envs win, and
// the per-open marker is always set last so nothing can override it.
func dockerTerminalEnvs(opts RemoteTerminalOptions, terminalID string) map[string]string {
	envs := map[string]string{
		"TERM":   "xterm-256color",
		"LANG":   "C.UTF-8",
		"LC_ALL": "C.UTF-8",
	}
	for key, value := range terminalEnvs(opts) {
		envs[key] = value
	}
	envs[dockerTerminalIDEnv] = terminalID
	return envs
}

// dockerTerminalShellCmd is the argv the PTY runs. bash is preferred for line
// editing and job control; a minimal image that only ships the POSIX shell
// still gets a usable terminal instead of "no such file".
func dockerTerminalShellCmd() []string {
	return []string{
		"/bin/sh", "-c",
		"if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi",
	}
}

// OpenTerminal opens an interactive shell PTY inside the container behind
// handle. The returned session streams until the shell exits, the context
// passed to OpenTerminal is cancelled, or Close is called — the last two end
// it identically (through Close, which also terminates the shell).
func (c *DockerRemoteClient) OpenTerminal(
	ctx context.Context,
	handle RemoteSandboxHandle,
	opts RemoteTerminalOptions,
) (RemoteTerminalSession, error) {
	id, err := dockerHandleID("OpenTerminal", handle)
	if err != nil {
		return nil, err
	}

	terminalID := dockerTerminalToken()
	execOpts := client.ExecCreateOptions{
		Cmd:          dockerTerminalShellCmd(),
		User:         dockerExecUser(opts.User),
		WorkingDir:   terminalCwd(opts),
		Env:          dockerEnvSlice(dockerTerminalEnvs(opts, terminalID)),
		TTY:          true,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		ConsoleSize: client.ConsoleSize{
			Height: uint(terminalRows(opts)),
			Width:  uint(terminalCols(opts)),
		},
	}
	created, err := c.api.ExecCreate(ctx, id, execOpts)
	if err != nil && dockerContainerNotRunning(err) {
		if readyErr := c.ensureRunning(ctx, id, "OpenTerminal"); readyErr != nil {
			return nil, readyErr
		}
		created, err = c.api.ExecCreate(ctx, id, execOpts)
	}
	if err != nil {
		return nil, dockerError("OpenTerminal", err)
	}

	// ExecAttach both starts the exec and hijacks its stdio. With TTY=true the
	// hijacked stream is raw bytes — no stdcopy 8-byte framing — so the pump
	// below reads it directly.
	attached, err := c.api.ExecAttach(ctx, created.ID, client.ExecAttachOptions{TTY: true})
	if err != nil {
		return nil, dockerError("OpenTerminal", err)
	}

	// The exit code has to be readable after the WebSocket handler's request
	// context is gone, so the session keeps a detached copy for its one
	// inspect call at stream end.
	inspectCtx := context.WithoutCancel(ctx)
	session := &dockerTerminalSession{
		api:         c.api,
		containerID: id,
		execID:      created.ID,
		terminalID:  terminalID,
		conn:        attached.Conn,
		reader:      attached.Reader,
		out:         make(chan RemoteTerminalEvent, terminalOutputBuffer),
		closedCh:    make(chan struct{}),
		inspectCtx:  inspectCtx,
	}
	go session.pump()

	// The caller's context is part of the session's lifetime, matching Cube and
	// E2B. Nothing else can honour it: the pump only reads the hijacked stream
	// and the moby hijack stops watching the context once the connection is
	// upgraded, so without this watcher a caller that only cancels its context
	// (never calling Close) would leave the shell running — and, worse, the TTL
	// refresh below rides a detached context, so it would keep the container
	// from ever being idle-reclaimed. Cancellation goes through the same Close
	// path as everything else, so it reaps the shell too.
	go func() {
		select {
		case <-ctx.Done():
			_ = session.Close()
		case <-session.closedCh:
		}
	}()

	// A PTY never runs the exec wrapper that touches the idle-sweep activity
	// marker, so without this a session that only ever sits in the terminal
	// would be reclaimed mid-use at its TTL. Exec refreshes the marker on its
	// way through the wrapper; a zero IdleTTL (sweeping disabled) no-ops. The
	// refresh is deliberately on the detached context: it must survive the
	// request context the handler used, and stops when closedCh closes.
	startTerminalTTLRefresh(inspectCtx, session.closedCh, c.settings.IdleTTL,
		func(rctx context.Context) error {
			_, err := c.Exec(rctx, handle, RemoteExecRequest{
				Command: "true",
				Timeout: dockerTerminalActivityTimeout,
			})
			return err
		})
	return session, nil
}

// dockerTerminalSession bridges one hijacked TTY exec stream to the neutral
// RemoteTerminalSession contract.
type dockerTerminalSession struct {
	api dockerEngineAPI
	// containerID is where the terminate exec is created; the shell lives in
	// this container and the kill has to run there too.
	containerID string
	execID      string
	// terminalID is the per-open marker set in the shell's environment; the
	// terminate exec matches it to find the shell and its descendants.
	terminalID string
	conn       net.Conn
	reader     *bufio.Reader

	out      chan RemoteTerminalEvent
	closedCh chan struct{}
	// closeOnce guards both closedCh and conn so concurrent Close calls are
	// safe (RemoteTerminalSession documents Close as idempotent).
	closeOnce sync.Once
	// killOnce serialises the terminate exec: pump reaching its end and an
	// explicit Close can both arrive, and the kill must run at most once.
	killOnce   sync.Once
	inspectCtx context.Context
}

// pump copies raw PTY output into the event channel until the stream ends.
func (s *dockerTerminalSession) pump() {
	defer close(s.out)

	var readErr error
	buf := make([]byte, 32*1024)
	for {
		n, err := s.reader.Read(buf)
		if n > 0 {
			// The daemon reuses the buffer, so copy before emitting.
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			emitTerminalEvent(s.out, s.closedCh, RemoteTerminalEvent{Data: chunk})
		}
		if err != nil {
			readErr = err
			break
		}
	}

	// Close tears the stream down on purpose; that is not a terminal failure
	// and must not be reported as one.
	select {
	case <-s.closedCh:
		return
	default:
	}

	switch {
	case readErr == nil || errors.Is(readErr, io.EOF):
		// The daemon ended the stream itself (the shell exited). Its exit code
		// is the only useful thing left to report.
		emitTerminalEvent(s.out, s.closedCh,
			RemoteTerminalEvent{Exited: true, ExitCode: s.inspectExitCode()})
	default:
		emitTerminalEvent(s.out, s.closedCh,
			RemoteTerminalEvent{Err: dockerError("terminal stream", readErr)})
	}
	// The stream is over; release the marker-refresh loop now instead of
	// waiting for the handler to get around to Close.
	s.teardown()
}

func (s *dockerTerminalSession) inspectExitCode() int {
	ctx, cancel := context.WithTimeout(s.inspectCtx, dockerTerminalInspectTimeout)
	defer cancel()
	inspected, err := s.api.ExecInspect(ctx, s.execID, client.ExecInspectOptions{})
	if err != nil {
		return -1
	}
	return inspected.ExitCode
}

func (s *dockerTerminalSession) Output() <-chan RemoteTerminalEvent { return s.out }

// PID is always 0. ExecInspect only knows the host PID, which means nothing
// inside the container and must not reach the browser; and since the exec
// cannot be re-attached there is no identifier worth handing out for a
// reconnect either. 0 keeps the frontend from sending a pty_id back.
func (s *dockerTerminalSession) PID() uint32 { return 0 }

// Write feeds keystrokes into the PTY. net.Conn is safe for concurrent use, so
// no lock is needed against Resize/Close.
//
// The hijacked conn does not see ctx, so its deadline is applied explicitly:
// a PTY whose foreground program stops reading stdin back-pressures this
// write, and without a deadline the caller's input loop would hang with it.
// Only the bridge's input pump writes, so setting the deadline here does not
// race another writer.
func (s *dockerTerminalSession) Write(ctx context.Context, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = s.conn.SetWriteDeadline(deadline)
		defer func() { _ = s.conn.SetWriteDeadline(time.Time{}) }()
	}
	if _, err := s.conn.Write(data); err != nil {
		return dockerError("terminal input", err)
	}
	return nil
}

func (s *dockerTerminalSession) Resize(ctx context.Context, cols, rows uint32) error {
	if cols == 0 || rows == 0 {
		return nil
	}
	if _, err := s.api.ExecResize(ctx, s.execID, client.ExecResizeOptions{
		Height: uint(rows),
		Width:  uint(cols),
	}); err != nil {
		return dockerError("terminal resize", err)
	}
	return nil
}

// Close detaches WeKnora from the PTY and, because Docker has no re-attach to
// preserve, terminates the shell inside the container. Unlike Cube/E2B,
// closing the hijacked connection does not end the exec — the daemon keeps it
// (and any foreground jobs) alive — so a bare disconnect would strand the
// process until the container is swept. Safe to call more than once.
func (s *dockerTerminalSession) Close() error {
	s.teardown()
	s.terminate()
	return nil
}

// terminate kills the exec's shell and every process it started, best effort.
// It runs from Close only, but Close follows every session end, including a
// shell that already exited on its own: then the shell is gone and the kill
// only reaches background jobs it left behind. Runs at most once.
//
// A failed kill leaves the shell running until the container is swept, so it
// is logged: nothing else would ever surface the leak.
func (s *dockerTerminalSession) terminate() {
	s.killOnce.Do(func() {
		ctx, cancel := context.WithTimeout(s.inspectCtx, dockerTerminalKillTimeout)
		defer cancel()
		created, err := s.api.ExecCreate(ctx, s.containerID, client.ExecCreateOptions{
			Cmd:  dockerTerminalKillCmd(s.terminalID),
			User: dockerExecUser(""),
		})
		if err != nil {
			logger.Warnf(ctx, "[sandbox-terminal] docker terminate exec create failed container=%s exec=%s: %v",
				s.containerID, s.execID, err)
			return
		}
		// Detached: the kill outlives this context and needs no output.
		if _, err := s.api.ExecStart(ctx, created.ID, client.ExecStartOptions{Detach: true}); err != nil {
			logger.Warnf(ctx, "[sandbox-terminal] docker terminate exec start failed container=%s exec=%s: %v",
				s.containerID, s.execID, err)
		}
	})
}

// dockerTerminalKillCmd builds the argv that terminates an abandoned PTY shell
// together with every process it started.
//
// It matches on the marker environment variable rather than a PID because
// ExecInspect reports the *host* PID, which is meaningless inside the
// container's PID namespace — a kill exec running there cannot see /proc/<pid>.
// Every process the shell starts inherits WEKNORA_TERMINAL_ID, so walking
// /proc and killing each carrier catches the shell and its whole job tree
// (a running foreground job is in its own process group, so killing the shell
// or its group alone would orphan it). A shell that already exited has no
// carriers left, which is why no existence check is needed before running it.
//
// /proc is read directly instead of shelling out to pgrep/pkill, which minimal
// sandbox images do not ship; the entry is compared with a shell `case` rather
// than grep for the same reason.
func dockerTerminalKillCmd(token string) []string {
	script := `tok=$1
for d in /proc/[0-9]*; do
q=${d#/proc/}
e=$(tr '\0' '\n' < "$d/environ" 2>/dev/null) || continue
case "$e" in
*"` + dockerTerminalIDEnv + `=$tok"*) kill -9 "$q" 2>/dev/null ;;
esac
done
exit 0`
	return []string{"/bin/sh", "-c", script, "weknora-terminate", token}
}

// teardown closes the stream and the marker-refresh loop exactly once. Both
// Close and the end of pump call it, so it is the single choke point for the
// idempotent-close contract.
func (s *dockerTerminalSession) teardown() {
	s.closeOnce.Do(func() {
		close(s.closedCh)
		_ = s.conn.Close()
	})
}
