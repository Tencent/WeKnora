package egress

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/utils"
)

// ProxyKey derives the key plugin proxy passwords come from out of the key
// every app node shares (hostpool.ClusterKey), so any replica can check a
// plugin's credentials without state.
func ProxyKey(clusterKey []byte) []byte {
	mac := hmac.New(sha256.New, clusterKey)
	_, _ = mac.Write([]byte("weknora plugin egress proxy"))
	return mac.Sum(nil)
}

// Password is a plugin's password on the cluster egress proxy; its user
// name is the plugin ID.
func Password(key []byte, pluginID string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(pluginID))
	return hex.EncodeToString(mac.Sum(nil))
}

// ClusterProxy is the egress proxy kubernetes plugins go through: an app
// node serves it on its own port, and a plugin's pod is handed it with the
// plugin's credentials. It applies the policy of the plugin's installed
// manifest, the same as a host plugin's proxy does.
type ClusterProxy struct {
	key    []byte
	lookup func(pluginID string) (*manifest.Manifest, bool)
	// direct are host:port addresses WeKnora names itself (the Host API
	// through the app Service): allowed without the public-address check.
	direct     map[string]bool
	dial       Dialer
	dialDirect Dialer
}

// NewClusterProxy serves the plugins lookup finds, with the credentials
// key derives.
func NewClusterProxy(
	key []byte, lookup func(pluginID string) (*manifest.Manifest, bool), direct []string,
) *ClusterProxy {
	p := &ClusterProxy{
		key: key, lookup: lookup, direct: map[string]bool{},
		dial:       utils.SSRFSafeDialContext,
		dialDirect: (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
	}
	for _, h := range direct {
		p.direct[strings.ToLower(h)] = true
	}
	return p
}

// caller is the plugin a request's credentials name, or "" when they are
// missing or wrong.
func (p *ClusterProxy) caller(r *http.Request) string {
	auth, ok := strings.CutPrefix(r.Header.Get("Proxy-Authorization"), "Basic ")
	if !ok {
		return ""
	}
	req := &http.Request{Header: http.Header{"Authorization": {"Basic " + auth}}}
	user, pass, ok := req.BasicAuth()
	if !ok || user == "" {
		return ""
	}
	if subtle.ConstantTimeCompare([]byte(pass), []byte(Password(p.key, user))) != 1 {
		return ""
	}
	return user
}

func (p *ClusterProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	pluginID := p.caller(r)
	if pluginID == "" {
		w.Header().Set("Proxy-Authenticate", `Basic realm="weknora-plugin"`)
		http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	m, ok := p.lookup(pluginID)
	if !ok || m.Runtime.Type != manifest.RuntimeKubernetes {
		logger.Warnf(r.Context(), "[plugin] egress proxy: %s is not an installed kubernetes plugin", pluginID)
		http.Error(w, "plugin "+pluginID+" is not installed", http.StatusForbidden)
		return
	}
	policy := NewPolicy(m.Permissions.Egress)
	route := func(host, port string) (Dialer, bool) {
		if p.direct[DirectKey(host, port)] {
			return p.dialDirect, true
		}
		return p.dial, policy.Allows(host)
	}
	Serve(w, r, route, func(w http.ResponseWriter, host string) {
		logger.Warnf(r.Context(), "[plugin] %s: egress to %s refused (not in permissions.egress)", pluginID, host)
		http.Error(w, Refusal(pluginID, host), http.StatusForbidden)
	})
}
