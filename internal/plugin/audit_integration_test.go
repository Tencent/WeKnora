package plugin

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/pkg/pluginapi"
	"github.com/stretchr/testify/require"
)

// TestHelperProcess is a helper subprocess: it attempts a real HTTP request
// through the SDK Guarded Client, then writes one ordinary log line to stderr.
// When run by go test normally (no env marker) it does nothing.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_PLUGIN_AUDIT_HELPER") != "1" {
		return
	}
	client := pluginapi.NewGuardedHTTPClient(pluginapi.NetworkNone, nil)
	request, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/api", nil)
	_, _ = client.Do(request)
	fmt.Fprintln(os.Stderr, "ordinary helper log")
	os.Exit(0)
}

// TestConsumeStderrFromRealProcess proves the end-to-end stderr capture path
// against a real OS process: a subprocess writes plugin_audit to its stderr,
// the host captures it via a pipe, parses it, and delivers it to the sink.
func TestConsumeStderrFromRealProcess(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")
	cmd.Env = append(os.Environ(), "GO_WANT_PLUGIN_AUDIT_HELPER=1")
	stderr, err := cmd.StderrPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())

	sink := &MemoryAuditSink{}
	done := make(chan struct{})
	go func() {
		ConsumePluginStderr(stderr, "test-plugin", sink)
		close(done)
	}()

	require.NoError(t, cmd.Wait())
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stderr consumer did not finish")
	}

	events := sink.Snapshot()
	require.Len(t, events, 1)
	require.Equal(t, "test-plugin", events[0].PluginID)
	require.Equal(t, "example.com:443", events[0].Destination)
	require.Equal(t, "none", events[0].Policy)
}
