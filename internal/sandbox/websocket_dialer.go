// Package sandbox provides WebSocket dialling for sandbox data-plane ports.
//
// This is the ws:// sibling of gateway_transport.go's RoundTripperFor, and it
// exists for the same reason: sandbox data-plane traffic is addressed as
// "{port}-{sandboxID}.{domain}" but must be dialled at the configured gateway,
// with the per-sandbox inbound token attached.
//
// It is a POOL METHOD, never a package-level function taking cfg. The process
// runs two SandboxGatewayTransportPools with two InboundTokenRegistries
// (tenant_resolver.go), selected by cfg.AllowPrivateEndpoints. A free function
// holding only cfg cannot know which registry to read; reading the wrong one
// returns an empty token and the gateway answers 401 — a symptom identical to
// the replica-affinity failure, and nearly impossible to tell apart in
// production.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// WebsocketDialer dials one data-plane port of one sandbox over
// WebSocket. Build it with SandboxGatewayTransportPool.WebsocketDialerFor.
type WebsocketDialer struct {
	// tokens is the registry of the pool that built this dialer.
	tokens *InboundTokenRegistry
	policy OutboundURLPolicy

	// target is the gateway "host:port" every dial actually connects to,
	// regardless of the authority in the URL. Empty means "no gateway
	// configured": dial the authority directly, matching what
	// RoundTripperFor does when parseProxyURL fails.
	target string

	// scheme is ws or wss, derived from the gateway's http/https.
	scheme string

	// sandboxDomain is the routing domain the gateway matches on Host.
	sandboxDomain string
}

// WebsocketDialerFor returns the dialer a client built from cfg must use to
// reach a sandbox data-plane port. It is the ws:// sibling of RoundTripperFor
// and MUST stay the only place that knows how: same gateway target, same
// SafeDialControlForPolicy guard, same inbound-token registry.
func (p *SandboxGatewayTransportPool) WebsocketDialerFor(cfg *Config) *WebsocketDialer {
	if p == nil {
		return nil
	}
	gatewayURL, _ := gatewayEndpointFor(cfg)
	d := &WebsocketDialer{
		tokens:        p.inboundTokens,
		policy:        p.policy,
		scheme:        "wss",
		sandboxDomain: sandboxDomainFor(cfg),
	}
	if host, port, scheme, ok := parseProxyURL(gatewayURL); ok {
		d.target = net.JoinHostPort(host, strconv.Itoa(port))
		if scheme == "http" {
			d.scheme = "ws"
		}
	}
	return d
}

// sandboxDomainFor reads the active provider's sandbox routing domain,
// mirroring gatewayEndpointFor's per-provider read: a stale sub-struct left
// behind by an earlier provider switch must not route today's traffic.
func sandboxDomainFor(cfg *Config) string {
	if cfg == nil {
		return ""
	}
	switch cfg.Type {
	case SandboxTypeE2B:
		if domain := strings.TrimSpace(cfg.E2BSandboxDomain); domain != "" {
			return domain
		}
		// Same fallback go-e2b applies when ClientConfig.SandboxDomain is
		// empty. Cube cannot do this: its domain is required and has no SDK
		// default.
		return DefaultE2BSandboxDomain
	default:
		return strings.TrimSpace(cfg.CubeSandboxDomain)
	}
}

// Dial opens a WebSocket to port/path of sandboxID.
//
// extraHeader carries credentials the caller owns — for the desktop that is
// Authorization: Basic for websockify, and the handle's inbound traffic token
// when the registry has not been written yet (Cube Create/Connect historically
// skipped that Put). The registry fills the inbound header only if missing.
//
// The negotiated subprotocol is pinned to "binary" and verified after the
// handshake. If an upstream ever selected base64, the bytes on the sandbox
// side would be base64 text and every byte offset the RFB parser computes
// would be wrong — silently, and only under load.
func (d *WebsocketDialer) Dial(
	ctx context.Context,
	sandboxID string,
	port int,
	path string,
	extraHeader http.Header,
) (*websocket.Conn, *http.Response, error) {
	if d == nil {
		return nil, nil, errors.New("sandbox: websocket dialer is not configured")
	}
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID == "" {
		return nil, nil, errors.New("sandbox: websocket dial requires a sandbox ID")
	}
	if d.sandboxDomain == "" {
		return nil, nil, errors.New(
			"sandbox: websocket dial requires a configured sandbox domain")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	authority := fmt.Sprintf("%d-%s.%s", port, sandboxID, d.sandboxDomain)
	target := &url.URL{Scheme: d.scheme, Host: authority, Path: path}

	header := http.Header{}
	for key, values := range extraHeader {
		for _, value := range values {
			header.Add(key, value)
		}
	}
	// Same rule as gatewaySplitTransport.withInboundToken: a caller-supplied
	// header (the handle's traffic token on DialDesktop) wins over a stale
	// registry copy. Fill only when missing so Cube/E2B HTTP and WS agree.
	if header.Get(InboundTokenHeader) == "" && d.tokens != nil {
		if token := d.tokens.Get(sandboxID); token != "" {
			header.Set(InboundTokenHeader, token)
		}
	}

	netDialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   SafeDialControlForPolicy(d.policy),
	}
	dialer := &websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
		// Only "binary" — see the doc comment.
		Subprotocols: []string{"binary"},
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// Ignore addr and dial the gateway, exactly as
			// newGatewayDataTransportWithPolicy does for HTTP.
			if d.target != "" {
				addr = d.target
			}
			return netDialer.DialContext(ctx, network, addr)
		},
	}
	dialer.NetDialTLSContext = nil // let the TLS handshake use NetDialContext

	conn, resp, err := dialer.DialContext(ctx, target.String(), header)
	if err != nil {
		return nil, resp, fmt.Errorf("sandbox: dial %s: %w", authority, err)
	}
	if sub := conn.Subprotocol(); sub != "binary" {
		_ = conn.Close()
		return nil, resp, fmt.Errorf(
			"sandbox: upstream selected subprotocol %q, want binary", sub)
	}
	return conn, resp, nil
}
