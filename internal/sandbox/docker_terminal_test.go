package sandbox

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
)

// newTerminalTestClient builds a Docker client with the idle sweep enabled, so
// the terminal's activity-marker refresh actually runs (newTestDockerClient
// disables it). ttl is the sweep window handed to the client.
func newTerminalTestClient(
	t *testing.T, engine *fakeDockerEngine, ttl time.Duration,
) *DockerRemoteClient {
	t.Helper()
	settings, err := dockerSettingsFromConfig(&Config{
		Type:        SandboxTypeDocker,
		DockerImage: "weknora/sandbox:test",
	})
	require.NoError(t, err)
	settings.IdleTTL = ttl
	return newDockerRemoteClientWithAPI(engine, settings)
}

func TestDockerOpenTerminalConfiguresTTYExec(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.execStdout = "ready\n"
	docker := newTestDockerClient(t, engine)

	session, err := docker.OpenTerminal(context.Background(), testHandle("c"),
		RemoteTerminalOptions{
			Cols: 100, Rows: 40,
			User: "user",
			Cwd:  "/workspace",
			Envs: map[string]string{"FOO": "bar"},
		})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	require.Len(t, engine.execOptions, 1)
	created := engine.execOptions[0]
	require.True(t, created.TTY, "a PTY needs TTY=true")
	require.True(t, created.AttachStdin)
	require.True(t, created.AttachStdout)
	require.True(t, created.AttachStderr)
	require.Equal(t, "user", created.User)
	require.Equal(t, "/workspace", created.WorkingDir)
	require.Equal(t, uint(40), created.ConsoleSize.Height)
	require.Equal(t, uint(100), created.ConsoleSize.Width)
	require.Contains(t, created.Env, "FOO=bar")
	require.Equal(t, []string{
		"/bin/sh", "-c",
		"if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi",
	}, created.Cmd, "the PTY must launch an interactive shell, not a bare command")

	require.Len(t, engine.attachedOptions, 1)
	require.True(t, engine.attachedOptions[0].TTY)
}

// A Docker exec gets only the daemon's TERM=xterm, so with no caller envs (the
// WebSocket handler never sends any) the adapter itself must supply what the
// Cube/E2B SDKs default: 256 colours and a UTF-8 locale.
func TestDockerOpenTerminalDefaultsInteractiveEnv(t *testing.T) {
	engine := newFakeDockerEngine()
	docker := newTestDockerClient(t, engine)

	session, err := docker.OpenTerminal(context.Background(), testHandle("c"),
		RemoteTerminalOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	require.Len(t, engine.execOptions, 1)
	env := engine.execOptions[0].Env
	require.Contains(t, env, "TERM=xterm-256color")
	require.Contains(t, env, "LANG=C.UTF-8")
	require.Contains(t, env, "LC_ALL=C.UTF-8")
	require.NotEmpty(t, terminalMarkerToken(t, engine.execOptions[0]))
}

// Caller envs override the defaults, but never the teardown marker: a caller
// that could pick it would steer the terminate exec at another terminal.
func TestDockerOpenTerminalCallerEnvOverridesDefaultsButNotMarker(t *testing.T) {
	engine := newFakeDockerEngine()
	docker := newTestDockerClient(t, engine)

	session, err := docker.OpenTerminal(context.Background(), testHandle("c"),
		RemoteTerminalOptions{Envs: map[string]string{
			"TERM":              "screen",
			"LANG":              "zh_CN.UTF-8",
			dockerTerminalIDEnv: "chosen",
		}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	env := engine.execOptions[0].Env
	require.Contains(t, env, "TERM=screen")
	require.Contains(t, env, "LANG=zh_CN.UTF-8")
	require.NotContains(t, env, "TERM=xterm-256color")
	require.NotEqual(t, "chosen", terminalMarkerToken(t, engine.execOptions[0]))
}

// The hijacked conn does not watch ctx. A PTY that stops reading stdin
// back-pressures Write, and without the ctx deadline the bridge's input loop
// would hang until something else tore the session down.
func TestDockerTerminalWriteHonoursContextDeadline(t *testing.T) {
	local, remote := net.Pipe()
	t.Cleanup(func() { _ = remote.Close() })
	engine := newFakeDockerEngine()
	engine.terminalConn = local
	docker := newTestDockerClient(t, engine)

	session, err := docker.OpenTerminal(context.Background(), testHandle("c"),
		RemoteTerminalOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	// Nobody reads remote, so the pipe write blocks like a full PTY.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- session.Write(ctx, []byte("ls\n")) }()
	select {
	case err := <-errCh:
		require.Error(t, err, "a back-pressured write must give up at the ctx deadline")
	case <-time.After(2 * time.Second):
		t.Fatal("Write ignored the ctx deadline")
	}

	// The deadline is per call: a later write without one must not inherit
	// the expired deadline and fail immediately.
	received := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 16)
		n, _ := remote.Read(buf)
		received <- buf[:n]
	}()
	require.NoError(t, session.Write(context.Background(), []byte("pwd\n")))
	require.Equal(t, []byte("pwd\n"), <-received)
}

// The terminate exec is best effort: a daemon refusing to start it must not
// turn Close into an error or a hang.
func TestDockerTerminalCloseToleratesTerminateFailure(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.terminalBlocks = true
	engine.execStartErr = errors.New("daemon refused")
	docker := newTestDockerClient(t, engine)

	session, err := docker.OpenTerminal(context.Background(), testHandle("c"),
		RemoteTerminalOptions{})
	require.NoError(t, err)
	go drainTerminalOutput(session)

	require.NoError(t, session.Close())
	require.Len(t, engine.execStarts, 1, "the terminate exec was attempted")
	require.NoError(t, session.Close(), "a second Close stays a no-op")
	require.Len(t, engine.execStarts, 1)
}

// A TTY attach is a raw byte stream. If the adapter reused the exec path's
// stdcopy demultiplexer here, the first 8 bytes would be eaten as a frame
// header and every keystroke echo would come back garbled.
func TestDockerTerminalStreamsRawBytesAndExitCode(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.execStdout = "hello\r\n"
	engine.execExit = 7
	docker := newTestDockerClient(t, engine)

	session, err := docker.OpenTerminal(context.Background(), testHandle("c"),
		RemoteTerminalOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	var got bytes.Buffer
	var exitCode *int
	deadline := time.After(2 * time.Second)
	for exitCode == nil {
		select {
		case ev, ok := <-session.Output():
			require.True(t, ok, "stream closed without an exit event")
			require.NoError(t, ev.Err)
			if ev.Exited {
				code := ev.ExitCode
				exitCode = &code
				break
			}
			got.Write(ev.Data)
		case <-deadline:
			t.Fatalf("timed out waiting for exit; got %q", got.String())
		}
	}
	require.Equal(t, "hello\r\n", got.String(),
		"TTY output must be raw, not stdcopy-framed")
	require.Equal(t, 7, *exitCode)
}

func TestDockerTerminalWriteResizeAndPID(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.execInspectPID = 4321
	engine.terminalBlocks = true
	docker := newTestDockerClient(t, engine)

	session, err := docker.OpenTerminal(context.Background(), testHandle("c"),
		RemoteTerminalOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	require.Zero(t, session.PID(),
		"ExecInspect's PID is a host PID; it must not reach the browser as a pty_id")

	require.NoError(t, session.Write(context.Background(), []byte("ls\n")))
	require.Equal(t, "ls\n", engine.execStdin.String())

	require.NoError(t, session.Resize(context.Background(), 120, 30))
	require.Len(t, engine.execResizes, 1)
	require.Equal(t, uint(30), engine.execResizes[0].Height)
	require.Equal(t, uint(120), engine.execResizes[0].Width)

	// A zero dimension is the "browser has not measured yet" case; it must
	// not reach the daemon.
	require.NoError(t, session.Resize(context.Background(), 0, 0))
	require.Len(t, engine.execResizes, 1)
}

// RemoteTerminalSession documents Close as safe to call more than once, and
// the bridge can reach it from several goroutines. A check-then-close probe
// on the teardown channel would panic here.
func TestDockerTerminalCloseIsIdempotentUnderConcurrency(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.terminalBlocks = true
	docker := newTestDockerClient(t, engine)

	session, err := docker.OpenTerminal(context.Background(), testHandle("c"),
		RemoteTerminalOptions{})
	require.NoError(t, err)

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		drainTerminalOutput(session)
	}()

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_ = session.Close()
		}()
	}
	close(start)
	wg.Wait()

	select {
	case <-drained:
	case <-time.After(3 * time.Second):
		t.Fatal("Output channel never closed after Close")
	}
}

// Closing a live terminal must terminate the shell: a Docker exec with a TTY
// survives the hijacked connection going away, so a bare disconnect would
// strand the shell (and its jobs) until the container is swept. Close has to
// issue a kill exec itself, matching the terminal's marker env.
func TestDockerTerminalCloseTerminatesLiveShell(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.terminalBlocks = true
	docker := newTestDockerClient(t, engine)

	session, err := docker.OpenTerminal(context.Background(), testHandle("c"),
		RemoteTerminalOptions{})
	require.NoError(t, err)
	// The terminal's own TTY exec, and nothing else yet.
	require.Len(t, engine.execOptions, 1)
	token := terminalMarkerToken(t, engine.execOptions[0])

	go drainTerminalOutput(session)
	require.NoError(t, session.Close())

	require.Len(t, engine.execOptions, 2, "Close must create a kill exec")
	kill := engine.execOptions[1]
	require.False(t, kill.TTY)
	require.Equal(t, DefaultSandboxExecUser, kill.User)
	require.Equal(t, dockerTerminalKillCmd(token), kill.Cmd,
		"the kill must carry the terminal's marker token")

	require.Len(t, engine.execStarts, 1, "the kill exec must actually be started")
	require.True(t, engine.execStarts[0].options.Detach,
		"the kill must run detached so it outlives the request context")
}

// The kill matches on the marker env, not a PID: ExecInspect reports a host
// PID that is meaningless inside the container's PID namespace, so a kill that
// targeted it would silently do nothing (the original bug).
func TestDockerTerminalKillCmdTargetsMarkerEnv(t *testing.T) {
	cmd := dockerTerminalKillCmd("deadbeef")
	require.Equal(t, "/bin/sh", cmd[0])
	require.Equal(t, "-c", cmd[1])
	require.Equal(t, "weknora-terminate", cmd[3])
	require.Equal(t, "deadbeef", cmd[4], "the token travels as argv")
	require.Contains(t, cmd[2], dockerTerminalIDEnv+"=$tok",
		"the script matches the marker env")
	require.Contains(t, cmd[2], "kill -9", "the script must kill, not signal politely")
	require.Contains(t, cmd[2], "/proc/[0-9]*", "it must walk every process")
	require.NotContains(t, cmd[2], "ExecInspect")
}

// drainTerminalOutput consumes a session's output until the channel closes, so
// the pump never stalls on a full buffer while a test drives Close.
func drainTerminalOutput(session RemoteTerminalSession) {
	for event := range session.Output() {
		_ = event
	}
}

// terminalMarkerToken extracts the WEKNORA_TERMINAL_ID value the adapter set on
// a captured ExecCreate, so a test can predict the kill argv.
func terminalMarkerToken(t *testing.T, opts client.ExecCreateOptions) string {
	t.Helper()
	prefix := dockerTerminalIDEnv + "="
	for _, entry := range opts.Env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	t.Fatalf("terminal exec is missing %s: %#v", dockerTerminalIDEnv, opts.Env)
	return ""
}

// The marker must be present and unique so two concurrent terminals in one
// container do not kill each other.
func TestDockerOpenTerminalTagsEachSession(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.execStdout = ""
	docker := newTestDockerClient(t, engine)

	first, err := docker.OpenTerminal(context.Background(), testHandle("c"), RemoteTerminalOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() })
	second, err := docker.OpenTerminal(context.Background(), testHandle("c"), RemoteTerminalOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })

	require.Len(t, engine.execOptions, 2)
	a := terminalMarkerToken(t, engine.execOptions[0])
	b := terminalMarkerToken(t, engine.execOptions[1])
	require.NotEmpty(t, a)
	require.NotEqual(t, a, b, "each terminal needs its own marker")
}

// Cancelling the caller's context must end the session, exactly like an
// explicit Close. Nothing else in the adapter honours the context — the pump
// only reads the hijacked stream and the refresh rides a detached context — so
// without a watcher a cancelled context would leak the shell and pin the
// container (its marker stays fresh forever).
func TestDockerTerminalContextCancelEndsSession(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.terminalBlocks = true
	docker := newTestDockerClient(t, engine)

	// Signal the terminate exec through a channel: the watcher runs it on its
	// own goroutine, so reading the exec slices here would race.
	killCreated := make(chan struct{})
	var once sync.Once
	engine.execCreateHook = func(opts client.ExecCreateOptions) {
		if !opts.TTY {
			once.Do(func() { close(killCreated) })
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	session, err := docker.OpenTerminal(ctx, testHandle("c"), RemoteTerminalOptions{})
	require.NoError(t, err)
	go drainTerminalOutput(session)

	cancel()

	select {
	case <-session.(*dockerTerminalSession).closedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling the context did not end the terminal session")
	}
	select {
	case <-killCreated:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling the context did not run the terminate exec")
	}
}

// A PTY never runs the exec wrapper that refreshes the idle-sweep activity
// marker, so the terminal has to do it itself or the container is reclaimed
// mid-use at its TTL.
func TestDockerTerminalRefreshesActivityMarker(t *testing.T) {
	prev := terminalTTLRefreshMin
	terminalTTLRefreshMin = 20 * time.Millisecond
	t.Cleanup(func() { terminalTTLRefreshMin = prev })

	engine := newFakeDockerEngine()
	engine.terminalBlocks = true
	docker := newTerminalTestClient(t, engine, 30*time.Minute)

	// The refresh is a plain (non-TTY) exec; observe it through the hook so
	// the background goroutine never races the test on the execOptions slice.
	var once sync.Once
	refreshed := make(chan struct{})
	engine.execCreateHook = func(options client.ExecCreateOptions) {
		if !options.TTY {
			once.Do(func() { close(refreshed) })
		}
	}

	session, err := docker.OpenTerminal(context.Background(), testHandle("c"),
		RemoteTerminalOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	select {
	case <-refreshed:
	case <-time.After(2 * time.Second):
		t.Fatal("expected a marker-refresh exec while the terminal is open")
	}
}

// OpenTerminal must fail with a classified error, not a panic, on a bad handle.
func TestDockerOpenTerminalRejectsEmptyHandle(t *testing.T) {
	docker := newTestDockerClient(t, newFakeDockerEngine())
	_, err := docker.OpenTerminal(context.Background(), nil, RemoteTerminalOptions{})
	require.Error(t, err)
}
