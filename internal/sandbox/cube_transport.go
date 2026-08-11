// Package sandbox: connection pooling for per-request Cube clients.
//
// Named Cube configs build a fresh client on every Resolve, so without an
// externally owned transport every request would open new TCP connections to
// both the CubeAPI control plane and the envd proxy.
//
// Cube cannot reuse the single shared transport E2B uses, because the SDK
// speaks two planes with different dialling rules:
//
//   - control plane (Create/Connect/List) talks to CubeAPIURL directly;
//   - data plane (exec, filesystem) addresses sandboxes as
//     "49983-{id}.{domain}" but must dial CubeProxyURL, keeping the sandbox
//     authority in the Host header so the proxy can route it.
//
// Handing the SDK one http.Client for both planes (WithHTTPClient overwrites
// controlHTTP and dataHTTP alike) drops that dial rewrite, which only appears
// to work when DNS happens to resolve the sandbox domain to the proxy node on
// the same port. This file keeps the rewrite by routing per request: control
// traffic rides the process-wide transport shared with E2B, data traffic rides
// a transport cached per proxy endpoint so configs pointing at the same proxy
// share one pool.
package sandbox

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CubeTransportPool owns the transports handed to per-request Cube clients.
// One instance lives for the process; clients built from it come and go.
type CubeTransportPool struct {
	control http.RoundTripper
	policy  OutboundURLPolicy

	// data maps a proxy "host:port" to the transport that dials it.
	data sync.Map
}

// NewCubeTransportPool returns a pool whose control plane rides control.
// A nil control transport installs a guarded one.
func NewCubeTransportPool(control http.RoundTripper) *CubeTransportPool {
	return NewCubeTransportPoolWithPolicy(control, DefaultOutboundURLPolicy())
}

func NewCubeTransportPoolWithPolicy(control http.RoundTripper, policy OutboundURLPolicy) *CubeTransportPool {
	if control == nil {
		control = NewGuardedTransportWithPolicy(policy)
	}
	return &CubeTransportPool{control: control, policy: policy}
}

// RoundTripperFor returns the transport a client built from cfg should use.
// Configs without a usable proxy URL keep every request on the control
// transport, matching the SDK's behaviour when no proxy node is configured.
func (p *CubeTransportPool) RoundTripperFor(cfg *Config) http.RoundTripper {
	split := &cubeSplitTransport{
		control:       p.control,
		sandboxDomain: strings.ToLower(strings.TrimSpace(cfg.CubeSandboxDomain)),
	}
	if host, port, _, ok := parseProxyURL(cfg.CubeProxyURL); ok {
		split.data = p.dataTransport(net.JoinHostPort(host, strconv.Itoa(port)))
	}
	return split
}

// dataTransport returns the transport dialling target, creating it once.
func (p *CubeTransportPool) dataTransport(target string) http.RoundTripper {
	if existing, ok := p.data.Load(target); ok {
		return existing.(http.RoundTripper)
	}
	actual, _ := p.data.LoadOrStore(target, newCubeDataTransportWithPolicy(target, p.policy))
	return actual.(http.RoundTripper)
}

// newCubeDataTransport dials target regardless of the request's authority,
// mirroring the SDK's proxy rewrite while adding the outbound address guard
// the SDK has no notion of.
func newCubeDataTransport(target string) *http.Transport {
	return newCubeDataTransportWithPolicy(target, DefaultOutboundURLPolicy())
}

func newCubeDataTransportWithPolicy(target string, policy OutboundURLPolicy) *http.Transport {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   SafeDialControlForPolicy(policy),
	}
	return &http.Transport{
		// The proxy is addressed directly; an ambient HTTP proxy would
		// defeat the rewrite.
		Proxy: nil,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, target)
		},
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
	}
}

// cubeSplitTransport routes a request to the control or the data transport by
// looking at the authority the SDK addressed.
type cubeSplitTransport struct {
	control       http.RoundTripper
	data          http.RoundTripper
	sandboxDomain string
}

func (t *cubeSplitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.data != nil && t.isDataPlane(req.URL.Hostname()) {
		return t.data.RoundTrip(req)
	}
	return t.control.RoundTrip(req)
}

// isDataPlane reports whether host addresses a sandbox rather than CubeAPI.
// Anything else - including an unset sandbox domain - stays on the control
// transport, so a misconfiguration cannot silently redirect API calls at the
// proxy.
func (t *cubeSplitTransport) isDataPlane(host string) bool {
	if t.sandboxDomain == "" {
		return false
	}
	host = strings.ToLower(host)
	return host == t.sandboxDomain || strings.HasSuffix(host, "."+t.sandboxDomain)
}

// CloseIdleConnections keeps the SDK's post-rollback reset meaningful. Only
// the data pool is dropped: the control transport is shared with every other
// tenant and with E2B, and one sandbox's restart is no reason to close it.
func (t *cubeSplitTransport) CloseIdleConnections() {
	if closer, ok := t.data.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
