package utils

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

type recordingRoundTripper struct {
	called bool
}

func (r *recordingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	r.called = true
	return nil, fmt.Errorf("unexpected network call")
}

func TestSSRFSafeClientValidatesInitialRequestAtFinalSink(t *testing.T) {
	base := &recordingRoundTripper{}
	client := NewSSRFSafeHTTPClientWithTransport(DefaultSSRFSafeHTTPClientConfig(), base)
	req, err := http.NewRequest(http.MethodGet, "http://169.254.169.254/latest/meta-data", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(req)
	if err == nil || !strings.Contains(err.Error(), "SSRF") {
		t.Fatalf("expected final-sink SSRF rejection, got %v", err)
	}
	if base.called {
		t.Fatal("unsafe request reached the base transport")
	}
}

func TestSSRFSafeDialContextRejectsRestrictedPortAtFinalSink(t *testing.T) {
	_, err := SSRFSafeDialContext(context.Background(), "tcp", "example.com:6379")
	if err == nil || !strings.Contains(err.Error(), "port 6379") {
		t.Fatalf("expected restricted-port error, got %v", err)
	}
}

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
	firstGuard, ok := first.Transport.(*SSRFValidatingRoundTripper)
	if !ok {
		t.Fatalf("expected SSRF-validating wrapper, got %T", first.Transport)
	}
	secondGuard, ok := second.Transport.(*SSRFValidatingRoundTripper)
	if !ok {
		t.Fatalf("expected SSRF-validating wrapper, got %T", second.Transport)
	}
	if firstGuard.Base != secondGuard.Base || firstGuard.Base != http.RoundTripper(shared) {
		t.Fatal("expected clients to share the supplied base transport")
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
	guard, ok := client.Transport.(*SSRFValidatingRoundTripper)
	if !ok {
		t.Fatalf("expected SSRF-validating wrapper, got %T", client.Transport)
	}
	if _, ok := guard.Base.(*http.Transport); !ok {
		t.Fatalf("expected *http.Transport base, got %T", guard.Base)
	}
	if client.CheckRedirect == nil {
		t.Fatal("expected SSRF redirect policy to be set")
	}
}
