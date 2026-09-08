package sandbox

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTerminalRequestDefaultsAndValidation(t *testing.T) {
	req, err := normalizeTerminalRequest(TerminalRequest{Command: "printf 'quoted; command'\n"})
	require.NoError(t, err)
	require.Equal(t, "printf 'quoted; command'\n", req.Command)
	require.Equal(t, 120*time.Second, req.Timeout)
	require.Equal(t, 60, req.CPUSeconds)
	require.Equal(t, int64(512*1024*1024), req.MemoryBytes)
	require.Equal(t, uint16(80), req.Cols)
	require.Equal(t, uint16(24), req.Rows)
	for _, invalid := range []TerminalRequest{
		{}, {Command: " \n"}, {Command: "x\x00y"}, {Command: strings.Repeat("x", 64*1024+1)},
		{Command: "x", Timeout: -1}, {Command: "x", Timeout: MaxTerminalTimeout + 1},
		{Command: "x", CPUSeconds: -1}, {Command: "x", MemoryBytes: -1},
	} {
		_, err := normalizeTerminalRequest(invalid)
		require.Error(t, err)
	}
	req, err = normalizeTerminalRequest(TerminalRequest{Command: "x", Timeout: MaxTerminalTimeout, CPUSeconds: 1, MemoryBytes: 64 * 1024 * 1024, Cols: 120, Rows: 40})
	require.NoError(t, err)
	require.Equal(t, MaxTerminalTimeout, req.Timeout)
	require.Equal(t, 1, req.CPUSeconds)
	require.Equal(t, uint16(120), req.Cols)
}

func TestTerminalTraceAndErrorsNeverFormatSecrets(t *testing.T) {
	secret := "INLINE_SECRET_DO_NOT_TRACE"
	command := "export API_TOKEN=" + secret + "; printf done"
	input := terminalSpanInput(TerminalRequest{Command: command})
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
		provider, ok := remoteTerminalFrom(wrapped)
		require.True(t, ok)
		require.Same(t, client, provider)
		mgr := &SessionBoundManager{client: wrapped, bindings: NewMemorySessionSandboxBindingStore()}
		_, ok = TerminalProviderFrom(mgr)
		require.True(t, ok)
		require.NoError(t, mgr.Cleanup(context.Background()))
		_, ok = TerminalProviderFrom(mgr)
		require.False(t, ok)
	}
	_, ok := TerminalProviderFrom(&DefaultManager{})
	require.False(t, ok)
	_, ok = TerminalProviderFrom(nil)
	require.False(t, ok)
	var mgr *SessionBoundManager
	_, ok = TerminalProviderFrom(mgr)
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
		{`{"event":{"start":{"pid":45}}}`, func(t *testing.T, event *terminalEnvdEvent) { require.Equal(t, uint32(45), event.Start.PID) }},
		{`{"event":{"data":{"pty":"AP/+DQ=="}}}`, func(t *testing.T, event *terminalEnvdEvent) {
			require.Equal(t, []byte{0, 255, 254, 13}, event.Data.PTY)
		}},
		{`{"event":{"end":{"exitCode":7,"exited":true,"error":"exit status 7"}}}`, func(t *testing.T, event *terminalEnvdEvent) { require.Equal(t, 7, event.End.ExitCode) }},
		{`{"event":{"keepalive":{}}}`, func(t *testing.T, event *terminalEnvdEvent) { require.Nil(t, event.End) }},
	} {
		event, err := readTerminalEnvdEvent(bytes.NewReader(terminalTestFrame(0, test.raw)))
		require.NoError(t, err)
		test.check(t, event)
	}
	_, err := readTerminalEnvdEvent(bytes.NewReader(terminalTestFrame(2, `{}`)))
	require.ErrorIs(t, err, io.EOF)
	_, err = readTerminalEnvdEvent(bytes.NewReader(terminalTestFrame(2, `{"error":{"code":"permission_denied","message":"SECRET"}}`)))
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
