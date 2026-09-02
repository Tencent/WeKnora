// Package sandbox: connection pooling and data-plane routing for per-request
// remote clients.
//
// Named configs build a fresh client on every Resolve, so without an
// externally owned transport every request would open new TCP connections to
// both the control plane and the envd data plane.
//
// Remote backends speak two planes with different dialling rules:
//
//   - control plane (Create/Connect/List) talks to the API URL directly;
//   - data plane (exec, filesystem) addresses sandboxes as
//     "49983-{id}.{domain}", which E2B Cloud resolves through public DNS and
//     TLS. Self-hosted E2B-compatible control planes (CubeSandbox's CubeProxy,
//     Agent-Sandbox's gateway, e2b-dev/infra's client proxy) instead front
//     every sandbox with one gateway address and route on the Host header.
//
// Handing an SDK one http.Client for both planes drops that distinction, which
// only appears to work when DNS happens to resolve the sandbox domain to the
// gateway on the same port. This file keeps the two planes apart by routing per
// request: control traffic rides the process-wide transport shared by every
// backend, data traffic rides a transport cached per gateway endpoint so
// configs pointing at the same gateway share one pool.
//
// The gateway may also be plain HTTP. Both SDKs pin the data-plane scheme to
// https, so a http:// gateway URL additionally rewrites the request scheme
// rather than forcing operators to terminate TLS in front of a local cluster.
package sandbox

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SandboxGatewayTransportPool owns the transports handed to per-request remote
// clients. One instance lives for the process; clients built from it come and
// go.
type SandboxGatewayTransportPool struct {
	control       http.RoundTripper
	policy        OutboundURLPolicy
	inboundTokens *InboundTokenRegistry

	// data maps a gateway "host:port" to the transport that dials it.
	data sync.Map
}

// NewSandboxGatewayTransportPool returns a pool whose control plane rides
// control. A nil control transport installs a guarded one.
func NewSandboxGatewayTransportPool(control http.RoundTripper) *SandboxGatewayTransportPool {
	return NewSandboxGatewayTransportPoolWithPolicy(control, DefaultOutboundURLPolicy())
}

func NewSandboxGatewayTransportPoolWithPolicy(
	control http.RoundTripper,
	policy OutboundURLPolicy,
) *SandboxGatewayTransportPool {
	if control == nil {
		control = NewGuardedTransportWithPolicy(policy)
	}
	return &SandboxGatewayTransportPool{
		control:       control,
		policy:        policy,
		inboundTokens: NewInboundTokenRegistry(),
	}
}

// InboundTokens is the registry the data-plane transport consults to attach a
// sandbox's inbound credential. Adapters register on create / connect.
func (p *SandboxGatewayTransportPool) InboundTokens() *InboundTokenRegistry {
	return p.inboundTokens
}

// RoundTripperFor returns the transport a client built from cfg should use.
// Configs without a usable gateway URL keep every request on the control
// transport, matching the SDKs' behaviour when no gateway is configured.
func (p *SandboxGatewayTransportPool) RoundTripperFor(cfg *Config) http.RoundTripper {
	gatewayURL, controlURL := gatewayEndpointFor(cfg)
	split := &gatewaySplitTransport{
		control:      p.control,
		controlHost:  hostOfURL(controlURL),
		inboundToken: p.inboundTokens,
	}
	if host, port, scheme, ok := parseProxyURL(gatewayURL); ok {
		split.data = p.dataTransport(net.JoinHostPort(host, strconv.Itoa(port)))
		split.dataScheme = scheme
	}
	return split
}

// gatewayEndpointFor reads the active provider's gateway and control-plane
// endpoints. Reading them per provider (rather than merging both) keeps a stale
// sub-struct left behind by an earlier provider switch from routing today's
// traffic.
func gatewayEndpointFor(cfg *Config) (gatewayURL, controlURL string) {
	if cfg == nil {
		return "", ""
	}
	switch cfg.Type {
	case SandboxTypeE2B:
		return cfg.E2BProxyURL, cfg.E2BAPIURL
	default:
		return cfg.CubeProxyURL, cfg.CubeAPIURL
	}
}

// hostOfURL returns raw's lowercased hostname without its port, or "" when raw
// is empty or unparseable.
func hostOfURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

// dataTransport returns the transport dialling target, creating it once.
func (p *SandboxGatewayTransportPool) dataTransport(target string) http.RoundTripper {
	if existing, ok := p.data.Load(target); ok {
		return existing.(http.RoundTripper)
	}
	actual, _ := p.data.LoadOrStore(target, newGatewayDataTransportWithPolicy(target, p.policy))
	return actual.(http.RoundTripper)
}

// newGatewayDataTransport dials target regardless of the request's authority,
// mirroring the SDK's proxy rewrite while adding the outbound address guard
// the SDKs have no notion of.
func newGatewayDataTransport(target string) *http.Transport {
	return newGatewayDataTransportWithPolicy(target, DefaultOutboundURLPolicy())
}

func newGatewayDataTransportWithPolicy(target string, policy OutboundURLPolicy) *http.Transport {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   SafeDialControlForPolicy(policy),
	}
	return &http.Transport{
		// The gateway is addressed directly; an ambient HTTP proxy would
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

// gatewaySplitTransport routes a request to the control or the data transport
// by looking at the authority the SDK addressed.
type gatewaySplitTransport struct {
	control http.RoundTripper
	data    http.RoundTripper

	// controlHost is the API endpoint's hostname. It is an exclusion, not the
	// routing rule: the rule is the sandbox authority shape, and this only
	// covers an API host that happens to wear that shape.
	controlHost  string
	inboundToken *InboundTokenRegistry

	// dataScheme is the gateway's scheme. When it differs from the scheme the
	// SDK hardcoded, data-plane requests are rewritten before dialling.
	dataScheme string
}

func (t *gatewaySplitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = t.withInboundToken(req)
	if t.data == nil || !t.isDataPlane(req.URL.Hostname()) {
		return t.control.RoundTrip(req)
	}
	return t.data.RoundTrip(t.applyGatewayScheme(req))
}

// withInboundToken attaches the sandbox's inbound credential when one is
// registered. It runs before the data/control split because a deployment
// without a gateway URL keeps sandbox traffic on the control transport, and
// those requests need the header just as much.
//
// An existing header wins: Cube's SDK sets it from the sandbox object, and
// overwriting that with our copy would turn a fresh token into a stale one.
func (t *gatewaySplitTransport) withInboundToken(req *http.Request) *http.Request {
	if t.inboundToken == nil || req.Header.Get(InboundTokenHeader) != "" {
		return req
	}
	sandboxID := SandboxIDFromDataPlaneHost(req.URL.Host)
	if sandboxID == "" {
		return req
	}
	token := t.inboundToken.Get(sandboxID)
	if token == "" {
		return req
	}
	// Clone rather than mutate: the caller owns req, and the SDK may retry it.
	cloned := req.Clone(req.Context())
	cloned.Header.Set(InboundTokenHeader, token)
	return cloned
}

// applyGatewayScheme returns req addressed with the gateway's scheme. The
// sandbox authority is preserved so the gateway can still route on Host.
func (t *gatewaySplitTransport) applyGatewayScheme(req *http.Request) *http.Request {
	if t.dataScheme == "" || t.dataScheme == req.URL.Scheme {
		return req
	}
	rewritten := req.Clone(req.Context())
	url := *req.URL
	url.Scheme = t.dataScheme
	rewritten.URL = &url
	return rewritten
}

// isDataPlane reports whether host addresses a sandbox rather than the control
// plane, by the "<port>-<sandboxID>.<domain>" authority shape both SDKs
// generate — the same test withInboundToken already applies to the same
// request.
//
// It deliberately does NOT compare against the configured sandbox domain.
// That field is optional for E2B, and go-e2b's envdBaseURL prefers whatever
// domain the control plane reported over the client-wide one, so the authority
// actually dialled need not appear anywhere in the config. Matching on it
// meant a configured gateway was silently never used, and — because the check
// was a suffix match — that E2B's own defaults (api.e2b.app under sandbox
// domain e2b.app) sent every control-plane call to the gateway instead.
//
// The control host is excluded explicitly because the shape test alone would
// accept an API host like "8080-api.example.com".
func (t *gatewaySplitTransport) isDataPlane(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || (t.controlHost != "" && host == t.controlHost) {
		return false
	}
	return SandboxIDFromDataPlaneHost(host) != ""
}

// CloseIdleConnections keeps the SDK's post-rollback reset meaningful. Only
// the data pool is dropped: the control transport is shared with every other
// tenant and every other backend, and one sandbox's restart is no reason to
// close it.
func (t *gatewaySplitTransport) CloseIdleConnections() {
	if closer, ok := t.data.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
