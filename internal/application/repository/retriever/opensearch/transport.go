package opensearch

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/opensearch-project/opensearch-go/v4"
	osapi "github.com/opensearch-project/opensearch-go/v4/opensearchapi"

	"github.com/Tencent/WeKnora/internal/types"
)

// newOpenSearchClient builds a TLS-hardened, pool-tuned *osapi.Client for
// the OpenSearch driver. The caller wires it into the registry from the
// env path (container) and the DB-store path (engine factory); the
// Repository constructor itself receives the pre-built client. While the
// driver is still gated dead code, no code path here is reachable from
// production — the activation switch lands in a later change.
//
// TLS posture:
//   - MinVersion: TLS 1.2 (TLS 1.3 negotiated when both ends support).
//   - InsecureSkipVerify: opt-in via ConnectionConfig.InsecureSkipVerify.
//     Default false. Operators set true ONLY for self-signed dev clusters.
//   - CipherSuites: explicit forward-secrecy-only list. TLS 1.2's default
//     cipher list includes TLS_RSA_* (no forward secrecy); we drop them.
//     TLS 1.3 cipher selection is automatic and safe.
//
// Transport tuning:
//   - MaxIdleConnsPerHost: 32   (Go default 2 is too low when callers
//     dispatch concurrent queries across multiple knowledge bases)
//   - IdleConnTimeout:     90s  (typical LB keep-alive)
//   - ResponseHeaderTimeout: 30s (per-request safety net)
//   - ExpectContinueTimeout: 1s
func newOpenSearchClient(cfg *types.ConnectionConfig) (*osapi.Client, error) {
	if cfg == nil || cfg.Addr == "" {
		return nil, fmt.Errorf("opensearch: ConnectionConfig.Addr required: %w", ErrConfigInvalid)
	}
	transport := buildHTTPTransport(cfg.InsecureSkipVerify)
	return osapi.NewClient(osapi.Config{
		Client: opensearch.Config{
			Addresses: []string{cfg.Addr},
			Username:  cfg.Username,
			Password:  cfg.Password,
			Transport: transport,
		},
	})
}

// buildHTTPTransport constructs the TLS-hardened, pool-tuned
// *http.Transport used by every OpenSearch client this driver creates.
// Extracted from newOpenSearchClient so unit tests can pin the TLS
// posture directly (the opensearch-go SDK wraps the transport in an
// internal interface, making reflection-based introspection of the
// final client fragile across SDK versions).
func buildHTTPTransport(insecureSkipVerify bool) *http.Transport {
	return &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: insecureSkipVerify, //nolint:gosec — operator opt-in flag
			CipherSuites: []uint16{
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
				tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			},
		},
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}
