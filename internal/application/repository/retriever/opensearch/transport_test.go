package opensearch

import (
	"crypto/tls"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// TestNewOpenSearchClient_RejectsEmptyAddr verifies the constructor
// refuses an empty Addr — operators who forget to set OPENSEARCH_ADDR
// get a clear ErrConfigInvalid at registration instead of a cryptic
// transport error later.
func TestNewOpenSearchClient_RejectsEmptyAddr(t *testing.T) {
	t.Parallel()
	_, err := newOpenSearchClient(&types.ConnectionConfig{Addr: ""})
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("empty addr: want ErrConfigInvalid, got %v", err)
	}
	_, err = newOpenSearchClient(nil)
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("nil cfg: want ErrConfigInvalid, got %v", err)
	}
}

// TestNewOpenSearchClient_Succeeds_OnValidAddr is a smoke test for the
// happy path — a non-empty Addr must produce a non-nil *osapi.Client.
// We don't probe the cluster (no network), just verify the constructor
// returns successfully.
func TestNewOpenSearchClient_Succeeds_OnValidAddr(t *testing.T) {
	t.Parallel()
	client, err := newOpenSearchClient(&types.ConnectionConfig{
		Addr:     "https://opensearch.example.com:9200",
		Username: "admin",
		Password: "secret", // not a real password — wire-format only
	})
	if err != nil {
		t.Fatalf("newOpenSearchClient: %v", err)
	}
	if client == nil {
		t.Fatal("client must be non-nil on success")
	}
}

// TestBuildHTTPTransport_DefaultsToSecureTLS pins the default
// InsecureSkipVerify=false. Operators who don't opt in MUST get a
// strict-verify HTTPS client.
func TestBuildHTTPTransport_DefaultsToSecureTLS(t *testing.T) {
	t.Parallel()
	tr := buildHTTPTransport(false)
	if tr.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig must be set")
	}
	if tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("default InsecureSkipVerify must be false (secure)")
	}
}

// TestBuildHTTPTransport_InsecureSkipVerifyOptIn confirms the flag
// flows through to tls.Config. Self-signed dev clusters can opt in;
// production deployments MUST leave it false.
func TestBuildHTTPTransport_InsecureSkipVerifyOptIn(t *testing.T) {
	t.Parallel()
	tr := buildHTTPTransport(true)
	if tr.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig must be set")
	}
	if !tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify=true must propagate to tls.Config")
	}
}

// TestBuildHTTPTransport_TLS12MinVersion pins the TLS 1.2 hard floor.
// TLS 1.3 negotiates automatically when both ends support it; 1.0 / 1.1
// must be rejected at handshake time.
func TestBuildHTTPTransport_TLS12MinVersion(t *testing.T) {
	t.Parallel()
	tr := buildHTTPTransport(false)
	if tr.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion: want tls.VersionTLS12 (0x%x), got 0x%x",
			tls.VersionTLS12, tr.TLSClientConfig.MinVersion)
	}
}

// TestBuildHTTPTransport_CipherSuitesExcludeNonECDHE pins the explicit
// cipher list to forward-secrecy-only ECDHE suites. TLS 1.2's default
// list includes TLS_RSA_* (no forward secrecy); we drop them. TLS 1.3
// cipher selection is automatic so this only matters for the 1.2 path.
func TestBuildHTTPTransport_CipherSuitesExcludeNonECDHE(t *testing.T) {
	t.Parallel()
	tr := buildHTTPTransport(false)
	cs := tr.TLSClientConfig.CipherSuites
	if len(cs) == 0 {
		t.Fatal("CipherSuites must be explicitly pinned (got empty)")
	}
	allowed := map[uint16]bool{
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256: true,
		tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256:   true,
		tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384: true,
		tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384:   true,
		tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305:  true,
		tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305:    true,
	}
	for _, suite := range cs {
		if !allowed[suite] {
			t.Fatalf("cipher suite 0x%x is not in the forward-secrecy whitelist", suite)
		}
	}
	// Spot-check that a known-weak non-ECDHE cipher is NOT in the list.
	for _, suite := range cs {
		if suite == tls.TLS_RSA_WITH_AES_128_GCM_SHA256 {
			t.Fatal("TLS_RSA_WITH_AES_128_GCM_SHA256 must NOT be present (no forward secrecy)")
		}
	}
}

// TestBuildHTTPTransport_PoolTuning pins the connection-pool settings.
// The Go default MaxIdleConnsPerHost is 2, which is too low for concurrent
// queries across multiple knowledge bases.
func TestBuildHTTPTransport_PoolTuning(t *testing.T) {
	t.Parallel()
	tr := buildHTTPTransport(false)
	if tr.MaxIdleConnsPerHost < 32 {
		t.Errorf("MaxIdleConnsPerHost: want >=32 for concurrent KB queries, got %d",
			tr.MaxIdleConnsPerHost)
	}
	if tr.IdleConnTimeout == 0 {
		t.Error("IdleConnTimeout must be set (Go default of 0 leaks idle conns)")
	}
	if tr.ResponseHeaderTimeout == 0 {
		t.Error("ResponseHeaderTimeout must be set (Go default of 0 means unbounded)")
	}
	if tr.ExpectContinueTimeout == 0 {
		t.Error("ExpectContinueTimeout must be set (avoids 100-continue stalls)")
	}
}
