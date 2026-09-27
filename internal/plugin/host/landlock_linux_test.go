//go:build linux

package host

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/plugin/driver"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/plugin/sandbox"
)

// Landlock keeps a plugin to the system's files, its package (read only) and
// its own directory, although it runs as WeKnora's user; outside a network
// namespace it also keeps its TCP connections to the proxy and the Host
// API.
func TestLandlockKeepsAPluginToItsFiles(t *testing.T) {
	abi := sandbox.LandlockABI()
	if abi < 1 {
		t.Skip("the kernel offers no Landlock")
	}
	fastTimings(t)
	t.Setenv(envNetns, "0")
	t.Setenv(envLandlock, "1")
	secret := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(secret, []byte("db password"), 0o600); err != nil {
		t.Fatal(err)
	}
	hostAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "host api")
	}))
	defer hostAPI.Close()
	other, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()

	m := NewManager()
	m.SetHostAPIAddr(strings.TrimPrefix(hostAPI.URL, "http://"))
	defer m.Close()
	l := install(t, "1.0.0", "")
	if err := reconcile.Activate(context.Background(), m, l); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if got := m.Files("acme.echo"); got != driver.FilesConfined {
		t.Fatalf("files = %q", got)
	}
	for query, want := range map[string]string{
		"read:" + secret:     "read failed",
		"read:/etc/hostname": "read ok",
		"read:" + filepath.Join(l.Dir, "bin", "linux-"+runtime.GOARCH, "echo"): "read ok",
		"write:" + filepath.Join(l.Dir, "x"):                                   "write failed",
		"write:" + filepath.Dir(secret) + "/x":                                 "write failed",
		"write:$HOME/state":                                                    "write ok",
		"read:/proc/cpuinfo":                                                   "read ok",
		"read:/proc/1/cmdline":                                                 "read failed",
	} {
		if got, err := search(t, m, query); err != nil || got != want {
			t.Errorf("%s = %q, %v; want %q", query, got, err, want)
		}
	}

	if abi < landlockTCPABI {
		if got := m.Egress("acme.echo"); got != driver.EgressProxy {
			t.Fatalf("egress without Landlock TCP rules = %q", got)
		}
		return
	}
	if got := m.Egress("acme.echo"); got != driver.EgressTCPLimited {
		t.Fatalf("egress = %q", got)
	}
	if got, err := search(t, m, "dial:"+other.Addr().String()); err != nil || got != "dial failed" {
		t.Fatalf("direct dial = %q, %v", got, err)
	}
	if got, err := search(t, m, "fetch:"+hostAPI.URL+"/"); err != nil || got != "200 host api" {
		t.Fatalf("Host API = %q, %v", got, err)
	}
	if got, err := search(t, m, "fetch:http://not-granted.example/"); err != nil || !strings.HasPrefix(got, "403") {
		t.Fatalf("proxy = %q, %v", got, err)
	}
}

// Without Landlock a plugin reaches WeKnora's user's files, and says so.
func TestNoLandlockReportsSharedFiles(t *testing.T) {
	fastTimings(t)
	t.Setenv(envNetns, "0")
	t.Setenv(envLandlock, "0")
	m := NewManager()
	defer m.Close()
	if err := reconcile.Activate(context.Background(), m, install(t, "1.0.0", "")); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if got := m.Files("acme.echo"); got != driver.FilesShared {
		t.Fatalf("files = %q", got)
	}
	if got := m.Egress("acme.echo"); got != driver.EgressProxy {
		t.Fatalf("egress = %q", got)
	}
}
