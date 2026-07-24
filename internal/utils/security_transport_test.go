package utils

import (
	"net/http"
	"testing"
	"time"
)

// TestNewSSRFSafeTransport_SharedAcrossClients verifies that a single transport
// can back multiple clients (global connection pooling) while each client keeps
// its own timeout and a redirect policy.
func TestNewSSRFSafeTransport_SharedAcrossClients(t *testing.T) {
	shared := NewSSRFSafeTransport(DefaultSSRFSafeHTTPClientConfig())

	cfg := DefaultSSRFSafeHTTPClientConfig()
	cfg.Timeout = 15 * time.Second
	first := NewSSRFSafeHTTPClientWithTransport(cfg, shared)

	cfg.Timeout = 45 * time.Second
	second := NewSSRFSafeHTTPClientWithTransport(cfg, shared)

	if first == second {
		t.Fatal("expected distinct HTTP clients")
	}
	if first.Transport != second.Transport {
		t.Fatal("expected clients to share the same transport")
	}
	if first.Transport != http.RoundTripper(shared) {
		t.Fatal("expected client to use the supplied shared transport")
	}
	if first.Timeout != 15*time.Second {
		t.Fatalf("unexpected first timeout: got %v, want %v", first.Timeout, 15*time.Second)
	}
	if second.Timeout != 45*time.Second {
		t.Fatalf("unexpected second timeout: got %v, want %v", second.Timeout, 45*time.Second)
	}
	if first.CheckRedirect == nil || second.CheckRedirect == nil {
		t.Fatal("expected SSRF redirect policy to be set on both clients")
	}
}

// TestNewSSRFSafeHTTPClient_HasDedicatedTransport verifies the convenience
// constructor still builds a working transport + redirect policy.
func TestNewSSRFSafeHTTPClient_HasDedicatedTransport(t *testing.T) {
	client := NewSSRFSafeHTTPClient(DefaultSSRFSafeHTTPClientConfig())
	if client.Transport == nil {
		t.Fatal("expected a transport to be set")
	}
	if _, ok := client.Transport.(*http.Transport); !ok {
		t.Fatalf("expected *http.Transport, got %T", client.Transport)
	}
	if client.CheckRedirect == nil {
		t.Fatal("expected SSRF redirect policy to be set")
	}
}
