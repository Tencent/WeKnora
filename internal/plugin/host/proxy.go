package host

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/utils"
)

// egressPolicy decides which hosts a plugin may reach: the manifest's
// permissions.egress patterns ("api.example.com", "*.atlassian.net"), which
// the system administrator accepted at install time.
type egressPolicy struct {
	any      bool // "*": every public host
	exact    map[string]bool
	suffixes []string // ".atlassian.net"
}

func newEgressPolicy(patterns []string) egressPolicy {
	p := egressPolicy{exact: map[string]bool{}}
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

func (p egressPolicy) allows(host string) bool {
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

// egressProxy is the HTTP proxy a host plugin's traffic goes through
// (HTTP_PROXY / HTTPS_PROXY in its environment). It forwards only to hosts
// the plugin was granted and dials through the SSRF-safe dialer, so a
// granted name that resolves to a private address is still refused unless
// the operator whitelisted it. A plugin can ignore the proxy variables; on
// Linux a network namespace can make the proxy the only way out.
type egressProxy struct {
	pluginID string
	policy   egressPolicy
	ln       net.Listener
	srv      *http.Server
	dial     func(ctx context.Context, network, addr string) (net.Conn, error)
	// direct are host:port addresses WeKnora itself names (the Host API on
	// another node): always allowed, and dialed without the public-address
	// check, since they are usually on the internal network. Only that port:
	// other services on the same machine stay out of reach. A sandboxed
	// plugin reaches them only through the proxy.
	direct     map[string]bool
	dialDirect func(ctx context.Context, network, addr string) (net.Conn, error)
	logf       func(format string, args ...any)
	// auth is the Proxy-Authorization this plugin's process sends; other
	// local processes (other plugins) cannot use its egress grants.
	auth, secret string
	wg           sync.WaitGroup
}

func startEgressProxy(
	pluginID string, patterns, direct []string, logf func(string, ...any),
) (*egressProxy, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start egress proxy: %w", err)
	}
	secret := randomToken()
	p := &egressProxy{
		pluginID: pluginID, policy: newEgressPolicy(patterns), ln: ln,
		dial: utils.SSRFSafeDialContext, logf: logf,
		direct:     map[string]bool{},
		dialDirect: (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		auth:       "Basic " + base64.StdEncoding.EncodeToString([]byte(proxyUser+":"+secret)),
		secret:     secret,
	}
	for _, h := range direct {
		p.direct[strings.ToLower(h)] = true
	}
	p.srv = &http.Server{Handler: p, ReadHeaderTimeout: 30 * time.Second}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		_ = p.srv.Serve(ln)
	}()
	return p, nil
}

// proxyUser is the user name in the proxy URL; the password is per process.
const proxyUser = "plugin"

// URL is the proxy address for the plugin's environment, with the
// credentials the proxy requires.
func (p *egressProxy) URL() string {
	return "http://" + proxyUser + ":" + p.secret + "@" + p.ln.Addr().String()
}

func (p *egressProxy) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = p.srv.Shutdown(ctx)
	p.wg.Wait()
}

// allows reports whether the plugin may reach host on port, and how to
// dial it.
func (p *egressProxy) allows(
	host, port string,
) (func(ctx context.Context, network, addr string) (net.Conn, error), bool) {
	if p.direct[net.JoinHostPort(strings.ToLower(strings.TrimSuffix(host, ".")), port)] {
		return p.dialDirect, true
	}
	return p.dial, p.policy.allows(host)
}

// authorized checks the request's proxy credentials.
func (p *egressProxy) authorized(w http.ResponseWriter, r *http.Request) bool {
	got := r.Header.Get("Proxy-Authorization")
	if subtle.ConstantTimeCompare([]byte(got), []byte(p.auth)) == 1 {
		return true
	}
	w.Header().Set("Proxy-Authenticate", `Basic realm="weknora-plugin"`)
	http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
	return false
}

func (p *egressProxy) deny(w http.ResponseWriter, host string) {
	p.logf("[plugin] %s: egress to %s refused (not in permissions.egress)", p.pluginID, host)
	http.Error(w, fmt.Sprintf("egress to %s is not permitted for plugin %s", host, p.pluginID), http.StatusForbidden)
}

func (p *egressProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !p.authorized(w, r) {
		return
	}
	if r.Method == http.MethodConnect {
		p.tunnel(w, r)
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
	dial, ok := p.allows(host, port)
	if !ok {
		p.deny(w, host)
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

// tunnel handles CONNECT, which HTTPS traffic uses: the proxy sees only the
// host and port, which is what the policy needs.
func (p *egressProxy) tunnel(w http.ResponseWriter, r *http.Request) {
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		http.Error(w, "bad CONNECT target", http.StatusBadRequest)
		return
	}
	dial, ok := p.allows(host, port)
	if !ok {
		p.deny(w, host)
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
