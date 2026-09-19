package plugin

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"testing"
	"time"
)

func TestEgressProxyStartStopIsIdempotentAndProtectsSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-domain socket proxy test requires a Unix runtime")
	}
	path := t.TempDir() + string(os.PathSeparator) + "egress.sock"
	sink := &MemoryAuditSink{}
	proxy := NewEgressProxy(path, "example.third-party", NetworkNone, nil, sink)
	if err := proxy.Start(); err != nil {
		t.Fatal(err)
	}
	if err := proxy.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("egress socket was not created: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket permissions = %o, want 600", got)
	}

	transport := &http.Transport{
		Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: "weknora-egress"}),
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		},
	}
	client := &http.Client{Transport: transport}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
	if events := sink.Snapshot(); len(events) == 0 || events[0].Allowed {
		t.Fatalf("expected a denied audit event, got %#v", events)
	}

	if err := proxy.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := proxy.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("egress socket still exists or stat failed: %v", err)
	}
}

func TestEgressProxyAuthorizeUsesManifestPolicyAndResolver(t *testing.T) {
	proxy := NewEgressProxy("unused.sock", "example.third-party", NetworkAllowlist, []string{"api.example.com:443"}, nil)
	proxy.guard.Resolver = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	}
	if _, err := proxy.authorize(context.Background(), "api.example.com", "443"); err != nil {
		t.Fatalf("allowlisted destination was rejected: %v", err)
	}
	if _, err := proxy.authorize(context.Background(), "other.example.com", "443"); err == nil {
		t.Fatal("non-allowlisted destination was accepted")
	}
}
