package host

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/egress"
	"github.com/Tencent/WeKnora/internal/utils"
)

// egressProxy is the HTTP proxy a host plugin's traffic goes through
// (HTTP_PROXY / HTTPS_PROXY in its environment). It forwards only to hosts
// the plugin was granted and dials through the SSRF-safe dialer, so a
// granted name that resolves to a private address is still refused unless
// the operator whitelisted it. A plugin can ignore the proxy variables; on
// Linux a network namespace can make the proxy the only way out.
type egressProxy struct {
	pluginID string
	policy   egress.Policy
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
		pluginID: pluginID, policy: egress.NewPolicy(patterns), ln: ln,
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
	if p.direct[egress.DirectKey(host, port)] {
		return p.dialDirect, true
	}
	return p.dial, p.policy.Allows(host)
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
	http.Error(w, egress.Refusal(p.pluginID, host), http.StatusForbidden)
}

func (p *egressProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !p.authorized(w, r) {
		return
	}
	egress.Serve(w, r, p.allows, p.deny)
}
