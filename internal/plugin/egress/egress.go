// Package egress holds a plugin's outbound traffic to the hosts its manifest
// was granted (permissions.egress). The same policy and forwarding back the
// per-process proxy of host plugins and the cluster proxy kubernetes plugins
// go through.
package egress

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Policy decides which hosts a plugin may reach: the manifest's
// permissions.egress patterns ("api.example.com", "*.atlassian.net"), which
// the system administrator accepted at install time.
type Policy struct {
	any      bool // "*": every public host
	exact    map[string]bool
	suffixes []string // ".atlassian.net"
}

// NewPolicy reads permissions.egress patterns.
func NewPolicy(patterns []string) Policy {
	p := Policy{exact: map[string]bool{}}
	for _, raw := range patterns {
		pat := strings.ToLower(strings.TrimSpace(raw))
		if pat == "" {
			continue
		}
		if pat == "*" {
			p.any = true
			continue
		}
		if rest, ok := strings.CutPrefix(pat, "*."); ok {
			p.suffixes = append(p.suffixes, "."+rest)
			continue
		}
		p.exact[pat] = true
	}
	return p
}

// Allows reports whether the policy grants host.
func (p Policy) Allows(host string) bool {
	if p.any {
		// The SSRF-safe dialer still refuses private addresses.
		return true
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if p.exact[host] {
		return true
	}
	for _, s := range p.suffixes {
		if strings.HasSuffix(host, s) {
			return true
		}
	}
	return false
}

// Dialer opens a connection for the proxy.
type Dialer = func(ctx context.Context, network, addr string) (net.Conn, error)

// Route picks how to dial host:port for a plugin, or refuses it.
type Route func(host, port string) (Dialer, bool)

// DirectKey is how a "direct" address (one WeKnora names itself, such as the
// Host API) is matched against a request's host and port.
func DirectKey(host, port string) string {
	return net.JoinHostPort(strings.ToLower(strings.TrimSuffix(host, ".")), port)
}

// Serve forwards one proxy request the caller has authenticated: plain HTTP
// is relayed, CONNECT (which HTTPS uses) is tunnelled. deny answers a target
// route refuses.
func Serve(w http.ResponseWriter, r *http.Request, route Route, deny func(w http.ResponseWriter, host string)) {
	if r.Method == http.MethodConnect {
		tunnel(w, r, route, deny)
		return
	}
	if r.URL.Host == "" {
		http.Error(w, "not a proxy request", http.StatusBadRequest)
		return
	}
	host, port := r.URL.Hostname(), r.URL.Port()
	if port == "" {
		port = "80"
		if r.URL.Scheme == "https" {
			port = "443"
		}
	}
	dial, ok := route(host, port)
	if !ok {
		deny(w, host)
		return
	}
	out := r.Clone(r.Context())
	out.RequestURI = ""
	out.Header.Del("Proxy-Connection")
	out.Header.Del("Proxy-Authorization")
	tr := &http.Transport{DialContext: dial, ResponseHeaderTimeout: 2 * time.Minute}
	defer tr.CloseIdleConnections()
	resp, err := tr.RoundTrip(out)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// tunnel handles CONNECT: the proxy sees only the host and port, which is
// what the policy needs.
func tunnel(w http.ResponseWriter, r *http.Request, route Route, deny func(w http.ResponseWriter, host string)) {
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		http.Error(w, "bad CONNECT target", http.StatusBadRequest)
		return
	}
	dial, ok := route(host, port)
	if !ok {
		deny(w, host)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	upstream, err := dial(ctx, "tcp", r.Host)
	cancel()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(w, "hijacking unsupported", http.StatusInternalServerError)
		return
	}
	client, buf, err := hj.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		_ = upstream.Close()
		_ = client.Close()
		return
	}
	if buf.Reader.Buffered() > 0 {
		pending := make([]byte, buf.Reader.Buffered())
		_, _ = buf.Read(pending)
		_, _ = upstream.Write(pending)
	}
	go func() {
		_, _ = io.Copy(upstream, client)
		_ = upstream.Close()
	}()
	_, _ = io.Copy(client, upstream)
	_ = client.Close()
}

// Refusal is the body of a denied request.
func Refusal(pluginID, host string) string {
	return fmt.Sprintf("egress to %s is not permitted for plugin %s", host, pluginID)
}
