package sandbox

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
)

type terminalDockerEngine struct {
	*fakeDockerEngine

	mu                 sync.Mutex
	daemonConn         net.Conn
	attachOptions      []client.ExecAttachOptions
	attachErr          error
	attachHadDeadline  bool
	createHadDeadline  bool
	inspectResult      client.ExecInspectResult
	inspectErr         error
	inspectHadDeadline bool
	resizeOptions      []client.ExecResizeOptions
	resizeErr          error
	resizeHadDeadline  bool
}

func (f *terminalDockerEngine) ExecCreate(
	ctx context.Context, id string, options client.ExecCreateOptions,
) (client.ExecCreateResult, error) {
	f.mu.Lock()
	_, f.createHadDeadline = ctx.Deadline()
	f.mu.Unlock()
	return f.fakeDockerEngine.ExecCreate(ctx, id, options)
}

func newTerminalDockerEngine() *terminalDockerEngine {
	return &terminalDockerEngine{
		fakeDockerEngine: newFakeDockerEngine(),
		inspectResult: client.ExecInspectResult{
			ID:      "exec-1",
			Running: true,
		},
	}
}

func (f *terminalDockerEngine) ExecAttach(
	ctx context.Context, _ string, options client.ExecAttachOptions,
) (client.ExecAttachResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attachOptions = append(f.attachOptions, options)
	_, f.attachHadDeadline = ctx.Deadline()
	if f.attachErr != nil {
		return client.ExecAttachResult{}, f.attachErr
	}
	clientConn, daemonConn := net.Pipe()
	f.daemonConn = daemonConn
	return client.ExecAttachResult{HijackedResponse: client.HijackedResponse{
		Conn:   clientConn,
		Reader: bufio.NewReader(clientConn),
	}}, nil
}

func (f *terminalDockerEngine) ExecInspect(
	ctx context.Context, _ string, _ client.ExecInspectOptions,
) (client.ExecInspectResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, f.inspectHadDeadline = ctx.Deadline()
	return f.inspectResult, f.inspectErr
}

func (f *terminalDockerEngine) ExecResize(
	ctx context.Context, _ string, options client.ExecResizeOptions,
) (client.ExecResizeResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, f.resizeHadDeadline = ctx.Deadline()
	f.resizeOptions = append(f.resizeOptions, options)
	return client.ExecResizeResult{}, f.resizeErr
}

func (f *terminalDockerEngine) setInspect(result client.ExecInspectResult, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inspectResult = result
	f.inspectErr = err
}

func (f *terminalDockerEngine) connection(t *testing.T) net.Conn {
	t.Helper()
	require.Eventually(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.daemonConn != nil
	}, time.Second, 5*time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.daemonConn
}

func newDockerTerminalTestClient(t *testing.T, api dockerEngineAPI) *DockerRemoteClient {
	t.Helper()
	settings, err := dockerSettingsFromConfig(&Config{
		Type:        SandboxTypeDocker,
		DockerImage: "weknora/sandbox:test",
	})
	require.NoError(t, err)
	settings.IdleTTL = 0
	return newDockerRemoteClientWithAPI(api, settings)
}

func openDockerTerminalForTest(
	t *testing.T, api *terminalDockerEngine, opts RemoteTerminalOptions,
) (*DockerRemoteClient, RemoteTerminalSession) {
	t.Helper()
	terminalClient := newDockerTerminalTestClient(t, api)
	session, err := terminalClient.OpenTerminal(
		context.Background(), &dockerSandboxHandle{id: "container-1"}, opts,
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = session.Close()
		if conn := api.connection(t); conn != nil {
			_ = conn.Close()
		}
	})
	return terminalClient, session
}

func TestDockerOpenTerminalUsesNativeTTYOptions(t *testing.T) {
	api := newTerminalDockerEngine()
	terminalClient, session := openDockerTerminalForTest(t, api, RemoteTerminalOptions{
		Cols:      132,
		Rows:      43,
		Cwd:       "/workspace/output",
		User:      "user",
		Envs:      map[string]string{"LANG": "C.UTF-8"},
		AttachPID: 999,
	})

	require.True(t, terminalClient.Capabilities().SupportsTerminals)
	require.False(t, terminalClient.Capabilities().SupportsTerminalReconnect)
	require.Zero(t, session.PID(), "Docker host PID must not be exposed as a PTY reconnect token")
	require.Len(t, api.execOptions, 1)
	created := api.execOptions[0]
	require.True(t, created.TTY)
	require.True(t, created.AttachStdin)
	require.True(t, created.AttachStdout)
	require.True(t, created.AttachStderr)
	require.Equal(t, uint(43), created.ConsoleSize.Height)
	require.Equal(t, uint(132), created.ConsoleSize.Width)
	require.Equal(t, "/workspace/output", created.WorkingDir)
	require.Equal(t, "user", created.User)
	require.ElementsMatch(t, []string{"LANG=C.UTF-8", "TERM=xterm-256color"}, created.Env)
	require.Equal(t, dockerTerminalCommand(), created.Cmd)
	require.Len(t, api.attachOptions, 1)
	require.True(t, api.attachOptions[0].TTY)
	require.Equal(t, uint(43), api.attachOptions[0].ConsoleSize.Height)
	require.Equal(t, uint(132), api.attachOptions[0].ConsoleSize.Width)
}

func TestDockerTerminalStreamsRawPTYAndAcceptsInputAndResize(t *testing.T) {
	api := newTerminalDockerEngine()
	_, session := openDockerTerminalForTest(t, api, RemoteTerminalOptions{})
	conn := api.connection(t)

	raw := []byte("\x1b[31m红色🙂\x1b[0m\r\n")
	writeDone := make(chan error, 1)
	go func() {
		_, err := conn.Write(raw)
		writeDone <- err
	}()
	select {
	case event := <-session.Output():
		require.NoError(t, event.Err)
		require.False(t, event.Exited)
		require.Equal(t, raw, event.Data, "TTY output must not pass through stdcopy demultiplexing")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for raw PTY output")
	}
	require.NoError(t, <-writeDone)

	input := []byte("echo hello\n\x03")
	readInput := make(chan []byte, 1)
	go func() {
		got := make([]byte, len(input))
		_, _ = io.ReadFull(conn, got)
		readInput <- got
	}()
	require.NoError(t, session.Write(context.Background(), input))
	require.Equal(t, input, <-readInput, "Ctrl-C must remain the raw 0x03 PTY byte")

	require.NoError(t, session.Resize(context.Background(), 120, 35))
	api.mu.Lock()
	require.Equal(t, []client.ExecResizeOptions{{Height: 35, Width: 120}}, api.resizeOptions)
	api.mu.Unlock()
}

func TestDockerTerminalReportsExitAndClosesOutputOnce(t *testing.T) {
	api := newTerminalDockerEngine()
	_, session := openDockerTerminalForTest(t, api, RemoteTerminalOptions{})
	api.setInspect(client.ExecInspectResult{ID: "exec-1", ExitCode: 23}, nil)
	require.NoError(t, api.connection(t).Close())

	event, ok := <-session.Output()
	require.True(t, ok)
	require.True(t, event.Exited)
	require.Equal(t, 23, event.ExitCode)
	require.NoError(t, event.Err)
	_, ok = <-session.Output()
	require.False(t, ok)
	require.NoError(t, session.Close())
	require.NoError(t, session.Close())
}

func TestDockerTerminalEOFWhileExecRunsIsNotReportedAsExit(t *testing.T) {
	previous := dockerTerminalExitInspectTimeout
	dockerTerminalExitInspectTimeout = 60 * time.Millisecond
	t.Cleanup(func() { dockerTerminalExitInspectTimeout = previous })

	api := newTerminalDockerEngine()
	_, session := openDockerTerminalForTest(t, api, RemoteTerminalOptions{})
	require.NoError(t, api.connection(t).Close())

	event, ok := <-session.Output()
	require.True(t, ok)
	require.Error(t, event.Err)
	require.False(t, event.Exited, "transport EOF must not masquerade as shell exit")
}

func TestDockerTerminalNormalizesAttachInspectAndResizeErrors(t *testing.T) {
	t.Run("invalid handle", func(t *testing.T) {
		terminalClient := newDockerTerminalTestClient(t, newTerminalDockerEngine())
		_, err := terminalClient.OpenTerminal(context.Background(), nil, RemoteTerminalOptions{})
		var remoteErr *RemoteError
		require.ErrorAs(t, err, &remoteErr)
		require.Equal(t, RemoteErrorKindInvalidRequest, remoteErr.Kind)
	})

	t.Run("create", func(t *testing.T) {
		api := newTerminalDockerEngine()
		api.execErr = errors.New("create failed")
		terminalClient := newDockerTerminalTestClient(t, api)
		_, err := terminalClient.OpenTerminal(
			context.Background(), &dockerSandboxHandle{id: "container-1"}, RemoteTerminalOptions{},
		)
		var remoteErr *RemoteError
		require.ErrorAs(t, err, &remoteErr)
		require.Equal(t, SandboxTypeDocker, remoteErr.Provider)
	})

	t.Run("attach", func(t *testing.T) {
		api := newTerminalDockerEngine()
		api.attachErr = errors.New("attach failed")
		terminalClient := newDockerTerminalTestClient(t, api)
		_, err := terminalClient.OpenTerminal(
			context.Background(), &dockerSandboxHandle{id: "container-1"}, RemoteTerminalOptions{},
		)
		var remoteErr *RemoteError
		require.ErrorAs(t, err, &remoteErr)
		require.Equal(t, SandboxTypeDocker, remoteErr.Provider)
	})

	t.Run("inspect", func(t *testing.T) {
		api := newTerminalDockerEngine()
		_, session := openDockerTerminalForTest(t, api, RemoteTerminalOptions{})
		api.setInspect(client.ExecInspectResult{}, errors.New("inspect failed"))
		require.NoError(t, api.connection(t).Close())
		event := <-session.Output()
		var remoteErr *RemoteError
		require.ErrorAs(t, event.Err, &remoteErr)
		require.Equal(t, SandboxTypeDocker, remoteErr.Provider)
	})

	t.Run("resize", func(t *testing.T) {
		api := newTerminalDockerEngine()
		api.resizeErr = errors.New("resize failed")
		_, session := openDockerTerminalForTest(t, api, RemoteTerminalOptions{})
		err := session.Resize(context.Background(), 90, 30)
		var remoteErr *RemoteError
		require.ErrorAs(t, err, &remoteErr)
		require.Equal(t, SandboxTypeDocker, remoteErr.Provider)
	})
}

func TestDockerTerminalCloseAndContextCancellationAreIdempotent(t *testing.T) {
	api := newTerminalDockerEngine()
	terminalClient := newDockerTerminalTestClient(t, api)
	ctx, cancel := context.WithCancel(context.Background())
	session, err := terminalClient.OpenTerminal(
		ctx, &dockerSandboxHandle{id: "container-1"}, RemoteTerminalOptions{},
	)
	require.NoError(t, err)
	conn := api.connection(t)
	go func() { _, _ = io.Copy(io.Discard, conn) }()

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_ = session.Write(context.Background(), []byte("x"))
		}()
		go func(i int) {
			defer wg.Done()
			<-start
			_ = session.Resize(context.Background(), uint32(80+i), 24)
		}(i)
	}
	close(start)
	cancel()
	wg.Wait()
	require.NoError(t, session.Close())
	require.NoError(t, session.Close())

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for event := range session.Output() {
			_ = event
		}
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Output did not close after context cancellation")
	}
	require.Error(t, session.Write(context.Background(), []byte("after-close")))
	require.Error(t, session.Resize(context.Background(), 80, 24))
}

func TestDockerTerminalUsesBoundedRPCsButNotForAttachStream(t *testing.T) {
	api := newTerminalDockerEngine()
	terminalClient := newDockerTerminalTestClient(t, withDockerRPCTimeout(api, 20*time.Millisecond))
	session, err := terminalClient.OpenTerminal(
		context.Background(), &dockerSandboxHandle{id: "container-1"}, RemoteTerminalOptions{},
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	require.NoError(t, session.Resize(context.Background(), 100, 30))
	api.setInspect(client.ExecInspectResult{ID: "exec-1", ExitCode: 0}, nil)
	require.NoError(t, api.connection(t).Close())
	require.True(t, (<-session.Output()).Exited)

	api.mu.Lock()
	createHadDeadline := api.createHadDeadline
	attachHadDeadline := api.attachHadDeadline
	inspectHadDeadline := api.inspectHadDeadline
	resizeHadDeadline := api.resizeHadDeadline
	api.mu.Unlock()
	require.True(t, createHadDeadline, "ExecCreate is a short Docker RPC")
	require.False(t, attachHadDeadline,
		"ExecAttach must use the terminal/session context, not DockerHTTPTimeout")
	require.True(t, inspectHadDeadline, "ExecInspect is a short Docker RPC")
	require.True(t, resizeHadDeadline, "ExecResize is a short Docker RPC")
}

func TestDockerTerminalDefaultsIncludeColorTerm(t *testing.T) {
	envs := dockerTerminalEnvs(RemoteTerminalOptions{})
	require.Contains(t, envs, "TERM=xterm-256color")
	require.True(t, strings.Contains(strings.Join(dockerTerminalCommand(), " "), "/bin/bash -il"))
}

var _ dockerEngineAPI = (*terminalDockerEngine)(nil)
