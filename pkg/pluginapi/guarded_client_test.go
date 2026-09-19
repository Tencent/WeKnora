package pluginapi

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestNetworkGuardCheckNoneBlocks(t *testing.T) {
	guard := &networkGuard{policy: NetworkNone}
	if _, err := guard.check(context.Background(), "example.com:443"); err == nil {
		t.Fatal("expected network none to block")
	}
}

func TestNetworkGuardCheckEgressAllowsPublicDomain(t *testing.T) {
	guard := &networkGuard{policy: NetworkEgress, resolver: func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	}}
	if _, err := guard.check(context.Background(), "example.com:443"); err != nil {
		t.Fatalf("expected egress to allow public domain: %v", err)
	}
}

func TestNetworkGuardCheckEgressBlocksLoopback(t *testing.T) {
	guard := &networkGuard{policy: NetworkEgress}
	if _, err := guard.check(context.Background(), "127.0.0.1:8080"); err == nil {
		t.Fatal("expected egress to block loopback")
	}
}

func TestNetworkGuardCheckAllowlist(t *testing.T) {
	guard := &networkGuard{policy: NetworkAllowlist, allowlist: []string{"api.example.com"}, resolver: func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	}}
	if _, err := guard.check(context.Background(), "api.example.com:443"); err != nil {
		t.Fatalf("expected allowlist hit: %v", err)
	}
	if _, err := guard.check(context.Background(), "other.example.com:443"); err == nil {
		t.Fatal("expected allowlist miss to block")
	}
}

func TestNetworkGuardBlocksHostnameResolvingToPrivateIP(t *testing.T) {
	guard := &networkGuard{policy: NetworkEgress, resolver: func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("10.0.0.7")}, nil
	}}
	if _, err := guard.check(context.Background(), "public-looking.example:443"); err == nil {
		t.Fatal("expected hostname resolving to private IP to be blocked")
	}
}

func TestNetworkGuardBlocksMetadataHostEvenWhenAllowlisted(t *testing.T) {
	guard := &networkGuard{policy: NetworkAllowlist, allowlist: []string{"metadata.google.internal:80"}, resolver: func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("169.254.169.254")}, nil
	}}
	if _, err := guard.check(context.Background(), "metadata.google.internal:80"); err == nil {
		t.Fatal("expected metadata host to be blocked")
	}
}

func TestGuardedHTTPClientBlocksNone(t *testing.T) {
	client := NewGuardedHTTPClient(NetworkNone, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(req); err == nil {
		t.Fatal("expected network none to block the HTTP request")
	}
}

func TestPolicyFromEnvFailsClosed(t *testing.T) {
	t.Setenv(EnvNetworkPolicy, "")
	if got := PolicyFromEnv(); got != NetworkNone {
		t.Fatalf("empty policy = %q, want %q", got, NetworkNone)
	}
	t.Setenv(EnvNetworkPolicy, "invalid")
	if got := PolicyFromEnv(); got != NetworkNone {
		t.Fatalf("invalid policy = %q, want %q", got, NetworkNone)
	}
}
