package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

// EgressProxy is a per-plugin HTTP CONNECT proxy. It listens on a Unix socket
// mounted into an OCI plugin that otherwise runs with --network none. The
// plugin therefore has no direct network device; every HTTP(S) request must
// cross this policy and audit boundary.
type EgressProxy struct {
	path   string
	server *http.Server
	ln     net.Listener
	guard  NetworkGuard
	mu     sync.Mutex
	lifeMu sync.Mutex
}

func NewEgressProxy(path, pluginID string, policy NetworkPolicy, allowlist []string, sink AuditSink) *EgressProxy {
	return &EgressProxy{
		path: path,
		guard: NetworkGuard{
			PluginID:  pluginID,
			Policy:    policy,
			Allowlist: append([]string(nil), allowlist...),
			Audit:     sink,
		},
	}
}

func (p *EgressProxy) Start() error {
	if p == nil {
		return fmt.Errorf("egress proxy is nil")
	}
	p.lifeMu.Lock()
	defer p.lifeMu.Unlock()
	if p.path == "" {
		return fmt.Errorf("egress proxy socket path is empty")
	}
	p.mu.Lock()
	if p.server != nil {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()
	if err := os.RemoveAll(p.path); err != nil {
		return fmt.Errorf("remove stale egress socket: %w", err)
	}
	ln, err := net.Listen("unix", p.path)
	if err != nil {
		return fmt.Errorf("listen egress socket: %w", err)
	}
	if err := os.Chmod(p.path, 0o600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("chmod egress socket: %w", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(p.handle), ReadHeaderTimeout: 10 * time.Second}
	p.mu.Lock()
	if p.server != nil {
		p.mu.Unlock()
		_ = ln.Close()
		_ = os.Remove(p.path)
		return nil
	}
	p.ln, p.server = ln, server
	p.mu.Unlock()
	go func() {
		if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
			p.guard.record(p.path, false, "egress proxy stopped: "+err.Error())
		}
	}()
	return nil
}

func (p *EgressProxy) Stop(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.lifeMu.Lock()
	defer p.lifeMu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.Lock()
	server, ln := p.server, p.ln
	p.server, p.ln = nil, nil
	p.mu.Unlock()
	if server == nil && ln == nil {
		return nil
	}
	var stopErr error
	if server != nil {
		stopErr = server.Shutdown(ctx)
	}
	if ln != nil {
		// server.Shutdown already closes the listener, so a second Close on the
		// normal path returns net.ErrClosed; only surface real close errors.
		if err := ln.Close(); err != nil && !errors.Is(err, net.ErrClosed) && stopErr == nil {
			stopErr = err
		}
	}
	if err := os.Remove(p.path); err != nil && !os.IsNotExist(err) && stopErr == nil {
		stopErr = err
	}
	return stopErr
}

func (p *EgressProxy) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	if r.URL == nil || r.URL.Hostname() == "" {
		http.Error(w, "proxy requires an absolute URL", http.StatusBadRequest)
		return
	}
	port := r.URL.Port()
	if port == "" {
		if r.URL.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	ips, err := p.authorize(r.Context(), r.URL.Hostname(), port)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           p.authorizedDialer(r.Context(), r.URL.Hostname(), port, ips),
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       30 * time.Second,
	}
	defer transport.CloseIdleConnections()
	out := r.Clone(r.Context())
	out.RequestURI = ""
	out.Header.Del("Proxy-Connection")
	resp, err := transport.RoundTrip(out)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (p *EgressProxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
		port = "443"
	}
	ips, err := p.authorize(r.Context(), host, port)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	upstream, err := p.authorizedDialer(r.Context(), host, port, ips)(r.Context(), "tcp", r.Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer upstream.Close()
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "CONNECT is not supported", http.StatusInternalServerError)
		return
	}
	client, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	copyDone := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, client); _ = upstream.Close(); copyDone <- struct{}{} }()
	go func() { _, _ = io.Copy(client, upstream); _ = client.Close(); copyDone <- struct{}{} }()
	<-copyDone
}

func (p *EgressProxy) authorize(ctx context.Context, host, port string) ([]net.IP, error) {
	if host == "" || port == "" {
		return nil, fmt.Errorf("invalid proxy destination")
	}
	ips, err := p.guard.authorize(ctx, host, port)
	if err != nil {
		return nil, err
	}
	p.guard.record(net.JoinHostPort(host, port), true, "allowed by managed egress proxy")
	return ips, nil
}

func (p *EgressProxy) authorizedDialer(ctx context.Context, host, port string, ips []net.IP) func(context.Context, string, string) (net.Conn, error) {
	return func(dialCtx context.Context, network, _ string) (net.Conn, error) {
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		var lastErr error
		for _, ip := range ips {
			conn, err := dialer.DialContext(dialCtx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("no authorized addresses for %s", host)
	}
}
