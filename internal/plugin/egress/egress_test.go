package egress

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
)

func TestPolicy(t *testing.T) {
	p := NewPolicy([]string{"API.example.com", "*.wild.test", " "})
	for host, want := range map[string]bool{
		"api.example.com": true, "api.example.com.": true, "x.api.example.com": false,
		"a.wild.test": true, "a.b.wild.test": true, "wild.test": false, "evil.test": false,
	} {
		if got := p.Allows(host); got != want {
			t.Errorf("Allows(%q) = %v, want %v", host, got, want)
		}
	}
	if !NewPolicy([]string{"*"}).Allows("anything.test") || NewPolicy(nil).Allows("a.test") {
		t.Fatal("\"*\" grants every host, nothing grants none")
	}
}

func TestPasswordIsPerPluginAndKey(t *testing.T) {
	k := ProxyKey([]byte("cluster"))
	if Password(k, "acme.a") == Password(k, "acme.b") {
		t.Fatal("two plugins share a password")
	}
	if Password(k, "acme.a") == Password(ProxyKey([]byte("other")), "acme.a") {
		t.Fatal("another cluster key gives the same password")
	}
	if Password(k, "acme.a") != Password(ProxyKey([]byte("cluster")), "acme.a") {
		t.Fatal("the password is not stable across replicas")
	}
}

// clusterProxy serves the proxy over HTTP with every name dialled to
// upstream, so the policy is what differs between hosts.
func clusterProxy(t *testing.T, upstream string, direct ...string) (*ClusterProxy, string) {
	t.Helper()
	plugins := map[string]*manifest.Manifest{
		"acme.kube": {
			ID: "acme.kube", Runtime: manifest.Runtime{Type: manifest.RuntimeKubernetes},
			Permissions: manifest.Permissions{Egress: []string{"api.allowed.test", "*.wild.test"}},
		},
		"acme.host": {
			ID: "acme.host", Runtime: manifest.Runtime{Type: manifest.RuntimeHost},
			Permissions: manifest.Permissions{Egress: []string{"*"}},
		},
	}
	p := NewClusterProxy(ProxyKey([]byte("k")), func(id string) (*manifest.Manifest, bool) {
		m, ok := plugins[id]
		return m, ok
	}, direct)
	p.dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, upstream)
	}
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	return p, srv.Listener.Addr().String()
}

func proxyClient(addr string, user *url.Userinfo) *http.Client {
	u := &url.URL{Scheme: "http", Host: addr, User: user}
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(u)}}
}

func status(t *testing.T, c *http.Client, target string) int {
	t.Helper()
	resp, err := c.Get(target)
	if err != nil {
		t.Fatalf("%s: %v", target, err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestClusterProxyAuthenticatesThePlugin(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer upstream.Close()
	p, addr := clusterProxy(t, upstream.Listener.Addr().String())
	for name, tc := range map[string]struct {
		user *url.Userinfo
		want int
	}{
		"no credentials":      {nil, http.StatusProxyAuthRequired},
		"wrong password":      {url.UserPassword("acme.kube", "guess"), http.StatusProxyAuthRequired},
		"another's password":  {url.UserPassword("acme.kube", Password(p.key, "acme.other")), 407},
		"uninstalled plugin":  {url.UserPassword("acme.gone", Password(p.key, "acme.gone")), http.StatusForbidden},
		"not a kube plugin":   {url.UserPassword("acme.host", Password(p.key, "acme.host")), http.StatusForbidden},
		"the plugin":          {url.UserPassword("acme.kube", Password(p.key, "acme.kube")), http.StatusOK},
		"another cluster key": {url.UserPassword("acme.kube", Password(ProxyKey([]byte("x")), "acme.kube")), 407},
	} {
		if got := status(t, proxyClient(addr, tc.user), "http://api.allowed.test/"); got != tc.want {
			t.Errorf("%s: status %d, want %d", name, got, tc.want)
		}
	}
}

func TestClusterProxyEnforcesTheManifest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	defer upstream.Close()
	p, addr := clusterProxy(t, upstream.Listener.Addr().String())
	c := proxyClient(addr, url.UserPassword("acme.kube", Password(p.key, "acme.kube")))
	for host, want := range map[string]int{
		"api.allowed.test": 200, "x.wild.test": 200, "wild.test": 403, "evil.test": 403,
	} {
		if got := status(t, c, "http://"+host+"/"); got != want {
			t.Errorf("%s: status %d, want %d", host, got, want)
		}
	}
}

func TestClusterProxyTunnelsHTTPS(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("secure"))
	}))
	defer upstream.Close()
	p, addr := clusterProxy(t, upstream.Listener.Addr().String())
	tr := upstream.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyURL(&url.URL{
		Scheme: "http", Host: addr, User: url.UserPassword("acme.kube", Password(p.key, "acme.kube")),
	})
	tr.TLSClientConfig.InsecureSkipVerify = true
	c := &http.Client{Transport: tr}
	resp, err := c.Get("https://api.wild.test/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "secure" {
		t.Fatalf("tunnelled body = %q", body)
	}
	if _, err := c.Get("https://evil.test/"); err == nil || !strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("CONNECT to a host outside the policy must be refused, got %v", err)
	}
}

// The Host API address passes on its private address; other private
// addresses stay refused by the SSRF-safe dialer.
func TestClusterProxyPassesDirectHosts(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("host api"))
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	p := NewClusterProxy(ProxyKey([]byte("k")), func(id string) (*manifest.Manifest, bool) {
		return &manifest.Manifest{
			ID: id, Runtime: manifest.Runtime{Type: manifest.RuntimeKubernetes},
			Permissions: manifest.Permissions{Egress: []string{"*"}},
		}, true
	}, []string{u.Host})
	srv := httptest.NewServer(p)
	defer srv.Close()
	proxyURL := &url.URL{
		Scheme: "http", Host: srv.Listener.Addr().String(),
		User: url.UserPassword("acme.kube", Password(p.key, "acme.kube")),
	}
	// http.ProxyURL skips loopback targets; this proxies every request.
	c := &http.Client{Transport: &http.Transport{Proxy: func(*http.Request) (*url.URL, error) { return proxyURL, nil }}}
	if got := status(t, c, upstream.URL+"/"); got != 200 {
		t.Fatalf("direct host = %d", got)
	}
	if got := status(t, c, "http://localhost:"+u.Port()+"/"); got == 200 {
		t.Fatal("a private address that is not a direct host went through")
	}
}
