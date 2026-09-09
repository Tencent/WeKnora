package sandbox

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTerminalRequestDefaultsAndValidation(t *testing.T) {
	req, err := normalizeTerminalRequest(CommandTerminalRequest{Command: "printf 'quoted; command'\n"})
	require.NoError(t, err)
	require.Equal(t, "printf 'quoted; command'\n", req.Command)
	require.Equal(t, 120*time.Second, req.Timeout)
	require.Equal(t, 60, req.CPUSeconds)
	require.Equal(t, int64(512*1024*1024), req.MemoryBytes)
	require.Equal(t, uint16(80), req.Cols)
	require.Equal(t, uint16(24), req.Rows)
	for _, invalid := range []CommandTerminalRequest{
		{},
		{Command: " \n"},
		{Command: "x\x00y"},
		{Command: strings.Repeat("x", 64*1024+1)},
		{Command: "x", Timeout: -1},
		{Command: "x", Timeout: MaxTerminalTimeout + 1},
		{Command: "x", CPUSeconds: -1},
		{Command: "x", MemoryBytes: -1},
	} {
		_, err := normalizeTerminalRequest(invalid)
		require.Error(t, err)
	}
	req, err = normalizeTerminalRequest(
		CommandTerminalRequest{
			Command:     "x",
			Timeout:     MaxTerminalTimeout,
			CPUSeconds:  1,
			MemoryBytes: 64 * 1024 * 1024,
			Cols:        120,
			Rows:        40,
		},
	)
	require.NoError(t, err)
	require.Equal(t, MaxTerminalTimeout, req.Timeout)
	require.Equal(t, 1, req.CPUSeconds)
	require.Equal(t, uint16(120), req.Cols)
}

func TestTerminalTraceAndErrorsNeverFormatSecrets(t *testing.T) {
	secret := "INLINE_SECRET_DO_NOT_TRACE"
	command := "export API_TOKEN=" + secret + "; printf done"
	input := terminalSpanInput(CommandTerminalRequest{Command: command})
	raw, err := json.Marshal(input)
	require.NoError(t, err)
	require.NotContains(t, string(raw), secret)
	require.NotContains(t, string(raw), "API_TOKEN")
	require.NotContains(t, input, "command")
	require.Equal(t, len(command), input["command_bytes"])
	providerErr := fmt.Errorf("provider echoed command %s credential %s: %w", command, secret, context.Canceled)
	safe := terminalProviderError("start", providerErr)
	require.NotContains(t, safe.Error(), secret)
	require.NotContains(t, fmt.Sprintf("%+v", safe), command)
	require.ErrorIs(t, safe, context.Canceled)
	require.Nil(t, terminalProviderError("stream", nil))
}

func TestTerminalCapabilitySurvivesTracingWrappers(t *testing.T) {
	for _, client := range []RemoteSandboxClient{&DockerRemoteClient{}, &E2BRemoteClient{}, &CubeRemoteClient{}} {
		wrapped := wrapLangfuseRemoteClient(client)
		provider, ok := remoteCommandTerminalFrom(wrapped)
		require.True(t, ok)
		require.Same(t, client, provider)
		mgr := &SessionBoundManager{client: wrapped, bindings: NewMemorySessionSandboxBindingStore()}
		_, ok = CommandTerminalProviderFrom(mgr)
		require.True(t, ok)
		require.NoError(t, mgr.Cleanup(context.Background()))
		_, ok = CommandTerminalProviderFrom(mgr)
		require.False(t, ok)
	}
	_, ok := CommandTerminalProviderFrom(&DefaultManager{})
	require.False(t, ok)
	_, ok = CommandTerminalProviderFrom(nil)
	require.False(t, ok)
	var mgr *SessionBoundManager
	_, ok = CommandTerminalProviderFrom(mgr)
	require.False(t, ok)
}

func TestTerminalBufferBoundAndBinaryRead(t *testing.T) {
	term := &commandTerminal{}
	term.readable = sync.NewCond(&term.mu)
	payload := bytes.Repeat([]byte{0, 255, '\r', '\n'}, terminalBufferLimit/4)
	n, err := term.Write(payload)
	require.NoError(t, err)
	require.Equal(t, len(payload), n)
	_, err = term.Write([]byte{1})
	require.ErrorIs(t, err, ErrTerminalOutputLimit)
	term.finished = true
	output, err := io.ReadAll(term)
	require.NoError(t, err)
	require.Equal(t, payload, output)
}

func terminalTestFrame(flags byte, raw string) []byte {
	frame := make([]byte, 5+len(raw))
	frame[0] = flags
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(raw)))
	copy(frame[5:], raw)
	return frame
}

func TestTerminalEnvdDocumentedJSONFrames(t *testing.T) {
	for _, test := range []struct {
		raw   string
		check func(*testing.T, *terminalEnvdEvent)
	}{
		{`{"event":{"start":{"pid":45}}}`, func(t *testing.T, event *terminalEnvdEvent) {
			require.Equal(t, uint32(45), event.Start.PID)
		}},
		{`{"event":{"data":{"pty":"AP/+DQ=="}}}`, func(t *testing.T, event *terminalEnvdEvent) {
			require.Equal(t, []byte{0, 255, 254, 13}, event.Data.PTY)
		}},
		{
			raw: `{"event":{"end":{"exitCode":7,"exited":true,"error":"exit status 7"}}}`,
			check: func(t *testing.T, event *terminalEnvdEvent) {
				require.Equal(t, 7, event.End.ExitCode)
			},
		},
		{`{"event":{"keepalive":{}}}`, func(t *testing.T, event *terminalEnvdEvent) { require.Nil(t, event.End) }},
	} {
		event, err := readTerminalEnvdEvent(bytes.NewReader(terminalTestFrame(0, test.raw)))
		require.NoError(t, err)
		test.check(t, event)
	}
	_, err := readTerminalEnvdEvent(bytes.NewReader(terminalTestFrame(2, `{}`)))
	require.ErrorIs(t, err, io.EOF)
	_, err = readTerminalEnvdEvent(
		bytes.NewReader(terminalTestFrame(2, `{"error":{"code":"permission_denied","message":"SECRET"}}`)),
	)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "SECRET")
	for _, invalid := range [][]byte{
		{0, 0, 16, 0, 1}, // oversized frame is rejected before allocation/read
		terminalTestFrame(1, `{}`), terminalTestFrame(0, `not JSON`),
		{0, 0, 0, 0, 8, '{'},
	} {
		_, err := readTerminalEnvdEvent(bytes.NewReader(invalid))
		require.Error(t, err)
		require.False(t, errors.Is(err, io.EOF))
	}
}

func TestTerminalEnvdBinaryInputRemainsBytes(t *testing.T) {
	payload := []byte{0, 255, 254, 128, '\r', '\n'}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/process.Process/SendInput", r.URL.Path)
		var request struct {
			Input struct {
				PTY []byte `json:"pty"`
			} `json:"input"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.Equal(t, payload, request.Input.PTY)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	client := &terminalEnvdClient{http: server.Client(), base: server.URL}
	err := client.unary(context.Background(), "SendInput", struct {
		Input struct {
			PTY []byte `json:"pty"`
		} `json:"input"`
	}{Input: struct {
		PTY []byte `json:"pty"`
	}{PTY: payload}})
	require.NoError(t, err)
}

func TestWorkbenchRuntimeProbeFailuresFailCapabilitiesClosed(t *testing.T) {
	failures := []struct {
		name         string
		result       *RemoteExecResult
		wantTerminal bool
		wantFiles    bool
	}{
		{name: "python", result: &RemoteExecResult{ExitCode: 127}},
		{
			name: "bash",
			result: &RemoteExecResult{
				Stdout: `{"contract":"weknora-workbench-runtime/v1",` +
					`"terminal":false,"files":true,"missing_terminal":"bash"}`,
			},
			wantFiles: true,
		},
		{
			name: "proc",
			result: &RemoteExecResult{
				Stdout: `{"contract":"weknora-workbench-runtime/v1",` +
					`"terminal":false,"files":false,"missing_terminal":"proc","missing_files":"proc"}`,
			},
		},
		{
			name: "prctl",
			result: &RemoteExecResult{
				Stdout: `{"contract":"weknora-workbench-runtime/v1",` +
					`"terminal":false,"files":true,"missing_terminal":"prctl"}`,
			},
			wantFiles: true,
		},
	}
	for _, failure := range failures {
		t.Run(failure.name, func(t *testing.T) {
			mgr, client, ctx := workbenchHarness(t, false)
			_, terminal := CommandTerminalProviderFrom(mgr)
			_, files := WorkbenchFileProviderFrom(mgr)
			require.True(t, terminal)
			require.True(t, files)
			creates, connects, _, lists, _ := client.counts()
			require.Zero(t, creates)
			require.Zero(t, connects)
			require.Zero(t, lists)

			client.probeResult = failure.result
			err := mgr.EnsureWorkbenchSession(ctx, "workbench-test")
			if failure.wantTerminal || failure.wantFiles {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, ErrWorkbenchRuntimeIncompatible)
			}
			require.Equal(t, 1, client.probeCalls)
			status, err := mgr.WorkbenchRuntimeStatus(ctx, "workbench-test")
			require.NoError(t, err)
			require.True(t, status.Known)
			require.Equal(t, failure.wantTerminal, status.Terminal)
			require.Equal(t, failure.wantFiles, status.Files)
			if !failure.wantTerminal {
				_, err = mgr.OpenSessionCommandTerminal(ctx, "workbench-test", CommandTerminalRequest{Command: "true"})
				require.ErrorIs(t, err, ErrWorkbenchRuntimeIncompatible)
			}
			if !failure.wantFiles {
				_, err = mgr.WorkbenchFiles(ctx, "workbench-test", WorkbenchFileRequest{Operation: "list"})
				require.ErrorIs(t, err, ErrWorkbenchRuntimeIncompatible)
			}

			client.probeResult = &RemoteExecResult{
				Stdout: `{"contract":"weknora-workbench-runtime/v1","terminal":true,"files":true}`,
			}
			require.NoError(t, mgr.EnsureWorkbenchSession(ctx, "workbench-test"))
			require.Equal(t, 2, client.probeCalls)
			status, err = mgr.WorkbenchRuntimeStatus(ctx, "workbench-test")
			require.NoError(t, err)
			require.True(t, status.Terminal)
			require.True(t, status.Files)
		})
	}
}

func TestWorkbenchInitializationRetriesSandboxReclaimedAfterResolve(t *testing.T) {
	for _, kind := range []RemoteErrorKind{
		RemoteErrorKindNotFound,
		RemoteErrorKindConflict,
	} {
		t.Run(string(kind), func(t *testing.T) {
			mgr, client, ctx := workbenchHarness(t, true)
			workspaceAttempts := 0
			client.run = func(
				_ context.Context,
				handle RemoteSandboxHandle,
				_ RemoteExecRequest,
			) (*RemoteExecResult, error) {
				workspaceAttempts++
				if workspaceAttempts == 1 {
					require.NoError(t, client.Delete(ctx, handle.ID()))
					return nil, NewRemoteError(
						client.Provider(),
						"Exec",
						kind,
						"sandbox reclaimed by idle sweep",
						nil,
					)
				}
				return &RemoteExecResult{}, nil
			}

			require.NoError(t, mgr.EnsureWorkbenchSession(ctx, "workbench-test"))
			require.Equal(t, 2, workspaceAttempts)
			require.Equal(t, 1, client.probeCalls)
			creates, _, _, _, deletes := client.counts()
			require.Equal(t, 2, creates)
			require.Equal(t, 1, deletes)

			key, err := mgr.sessionKey(ctx, "workbench-test")
			require.NoError(t, err)
			binding, err := mgr.bindings.Get(ctx, key)
			require.NoError(t, err)
			require.Equal(t, "docker-2", binding.SandboxID)
			leaser := mgr.bindings.(sessionTurnLeaseStore)
			active, _, err := leaser.TurnState(ctx, key)
			require.NoError(t, err)
			require.False(t, active, "initialization must release its temporary turn")
		})
	}
}

func TestWorkbenchRuntimeStateCacheIsBoundedAndExpires(t *testing.T) {
	now := time.Date(2026, time.September, 8, 12, 0, 0, 0, time.UTC)
	cache := newWorkbenchRuntimeStateCache(3, time.Minute)
	cache.now = func() time.Time { return now }

	var latestRuntime workbenchRuntimeKey
	var latestSandbox workbenchSandboxRuntimeKey
	for index := 0; index < 4; index++ {
		latestRuntime = workbenchRuntimeKey{
			provider: SandboxTypeDocker,
			configID: fmt.Sprintf("config-%d", index),
		}
		latestSandbox = workbenchSandboxRuntimeKey{
			runtime: latestRuntime,
			id:      fmt.Sprintf("sandbox-%d", index),
		}
		cache.store(
			latestSandbox,
			workbenchRuntimeSupport{terminal: true, files: true},
		)
		now = now.Add(time.Second)
	}

	cache.mu.Lock()
	require.Len(t, cache.sandboxes, 3)
	cache.mu.Unlock()
	_, found := cache.loadSandbox(latestSandbox)
	require.True(t, found)

	now = now.Add(2 * time.Minute)
	_, found = cache.loadSandbox(latestSandbox)
	require.False(t, found)
}

func TestWorkbenchRuntimeProbeFailureIsIsolatedToSandbox(t *testing.T) {
	mgr, client, ctx := workbenchHarness(t, false)
	require.NoError(t, mgr.EnsureWorkbenchSession(ctx, "healthy-session"))
	_, err := mgr.WorkbenchFiles(ctx, "healthy-session", WorkbenchFileRequest{Operation: "list"})
	require.NoError(t, err)

	client.probeResult = &RemoteExecResult{ExitCode: 127}
	require.ErrorIs(t, mgr.EnsureWorkbenchSession(ctx, "broken-session"), ErrWorkbenchRuntimeIncompatible)
	client.probeResult = nil
	_, err = mgr.WorkbenchFiles(ctx, "healthy-session", WorkbenchFileRequest{Operation: "list"})
	require.NoError(t, err)
	require.Equal(t, 2, client.probeCalls, "healthy sandbox retains its own cached result")

	status, err := mgr.WorkbenchRuntimeStatus(ctx, "healthy-session")
	require.NoError(t, err)
	require.Equal(t, WorkbenchRuntimeStatus{Known: true, Terminal: true, Files: true}, status)
	status, err = mgr.WorkbenchRuntimeStatus(ctx, "broken-session")
	require.NoError(t, err)
	require.Equal(t, WorkbenchRuntimeStatus{Known: true}, status)

	require.NoError(t, mgr.EnsureWorkbenchSession(ctx, "new-session"))
	require.NoError(t, mgr.EnsureWorkbenchSession(ctx, "broken-session"))
	status, err = mgr.WorkbenchRuntimeStatus(ctx, "broken-session")
	require.NoError(t, err)
	require.True(t, status.Terminal)
	require.True(t, status.Files)
}

func TestWorkbenchRuntimeStatusDoesNotContactProvider(t *testing.T) {
	mgr, client, ctx := workbenchHarness(t, false)
	status, err := mgr.WorkbenchRuntimeStatus(ctx, "unknown-session")
	require.NoError(t, err)
	require.False(t, status.Known)
	creates, connects, gets, lists, deletes := client.counts()
	require.Zero(t, creates+connects+gets+lists+deletes)
	require.Zero(t, client.probeCalls)

	require.NoError(t, mgr.EnsureWorkbenchSession(ctx, "workbench-test"))
	beforeCreates, beforeConnects, beforeGets, beforeLists, beforeDeletes := client.counts()
	status, err = mgr.WorkbenchRuntimeStatus(ctx, "workbench-test")
	require.NoError(t, err)
	require.True(t, status.Known)
	creates, connects, gets, lists, deletes = client.counts()
	require.Equal(t, []int{beforeCreates, beforeConnects, beforeGets, beforeLists, beforeDeletes},
		[]int{creates, connects, gets, lists, deletes})
	require.Equal(t, 1, client.probeCalls)
}

func TestTerminalRuntimePythonProbeSuite(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("runtime contract requires Linux")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(
		ctx, python, "-I", filepath.Join(filepath.Dir(source), "terminal_runtime_probe_test.py"),
	).CombinedOutput()
	require.NoError(t, err, "%s", out)
	t.Logf("%s", out)
}
