//go:build linux

package host

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/plugin/sandbox"
)

// TestMain lets the test binary be the sandbox helper, as WeKnora's binary
// is in production.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == sandbox.Subcommand {
		os.Exit(sandbox.Main(os.Args[2:]))
	}
	os.Exit(m.Run())
}

func TestSandboxedPluginOnlyReachesTheProxyAndHostAPI(t *testing.T) {
	fastTimings(t)
	t.Setenv(envNetns, "1")
	hostAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "host api")
	}))
	defer hostAPI.Close()
	other, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	m := NewManager()
	m.SetHostAPIAddr(strings.TrimPrefix(hostAPI.URL, "http://"))
	defer m.Close()
	if err := m.Activate(context.Background(), install(t, "1.0.0", "")); err != nil {
		if strings.Contains(err.Error(), "user namespaces") {
			// GitHub's Ubuntu 24.04 runners restrict them.
			t.Skipf("unprivileged user namespaces are not available here: %v", err)
		}
		t.Fatalf("Activate: %v", err)
	}
	// Direct connections to the host's loopback do not exist in there.
	if got, err := search(t, m, "dial:"+other.Addr().String()); err != nil || got != "dial failed" {
		t.Fatalf("direct dial = %q, %v", got, err)
	}
	// The Host API is relayed, on the address the plugin is told.
	if got, err := search(t, m, "fetch:"+hostAPI.URL+"/"); err != nil || got != "200 host api" {
		t.Fatalf("Host API = %q, %v", got, err)
	}
	// The egress proxy answers, and applies the plugin's policy.
	if got, err := search(t, m, "fetch:http://not-granted.example/"); err != nil || !strings.HasPrefix(got, "403") {
		t.Fatalf("proxy = %q, %v", got, err)
	}
}
