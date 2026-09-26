package container

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/Tencent/WeKnora/internal/plugin/egress"
	"github.com/Tencent/WeKnora/internal/plugin/host"
	"github.com/Tencent/WeKnora/internal/plugin/hostpool"
	"github.com/Tencent/WeKnora/internal/plugin/kube"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	pluginregistry "github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/plugin/remote"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

func pythonPlugin() *reconcile.Loaded {
	return &reconcile.Loaded{Manifest: &manifest.Manifest{
		ID: "acme.py", Version: "1.0.0",
		Runtime: manifest.Runtime{Type: manifest.RuntimeHost, Kind: host.KindPython, Entry: "main.py"},
	}}
}

// A node that does not run a kind hands it to plugin hosts; without any it
// says so instead of pretending the plugin loaded.
func TestDelegationNeedsPluginHosts(t *testing.T) {
	ctx := context.Background()
	h := host.NewManager()
	h.SetKinds([]string{host.KindBinary})

	err := newPluginDelegation(h, nil).Activate(ctx, pythonPlugin())
	if err == nil || !strings.Contains(err.Error(), "no plugin host is configured") {
		t.Fatalf("without plugin hosts = %v", err)
	}

	mr := miniredis.RunT(t)
	pool := hostpool.NewPool(redis.NewClient(&redis.Options{Addr: mr.Addr()}), []byte("k"))
	if err := newPluginDelegation(h, pool).Activate(ctx, pythonPlugin()); err != nil {
		t.Fatalf("with plugin hosts configured, a missing one is not a load failure: %v", err)
	}

	h.SetKinds([]string{host.KindPython})
	if err := newPluginDelegation(h, nil).Activate(ctx, pythonPlugin()); err != nil {
		t.Fatalf("a kind the node runs needs no plugin host: %v", err)
	}
}

// Calls go to the pool only for host plugins this node does not run.
func TestPluginClientsRouteToThePool(t *testing.T) {
	ctx := context.Background()
	h := host.NewManager()
	h.SetKinds([]string{host.KindBinary})
	mr := miniredis.RunT(t)
	pool := hostpool.NewPool(redis.NewClient(&redis.Options{Addr: mr.Addr()}), []byte("k"))
	clients := pluginClients{host: h, remote: remote.NewManager(), pool: pool}

	_, err := clients.Client(ctx, pythonPlugin().Manifest)
	if pe, ok := pluginapi.AsError(err); !ok || !strings.Contains(pe.Message, "no plugin host runs acme.py@1.0.0") {
		t.Fatalf("a delegated plugin is looked up in the pool, got %v", err)
	}
	if clients.OnThisNode("acme.py") {
		t.Fatal("a delegated plugin is not on this node")
	}

	clients.pool = nil
	_, err = clients.Client(ctx, pythonPlugin().Manifest)
	if pe, ok := pluginapi.AsError(err); !ok || !strings.Contains(pe.Message, "not running on this node") {
		t.Fatalf("without a pool the node's own host answers, got %v", err)
	}
}

// Events held since startup are flushed only once the task handlers exist:
// Lite runs tasks in process and drops a task with no handler.
func TestDeferredPluginEventsFlushAfterTaskHandlers(t *testing.T) {
	src, err := os.ReadFile("container.go")
	if err != nil {
		t.Fatal(err)
	}
	flush := strings.Index(string(src), "container.Invoke(flushDeferredPluginEvents)")
	for _, handlers := range []string{
		"container.Invoke(startPluginReconciler)",
		"container.Invoke(router.RunAsynqServer)",
		"container.Invoke(router.RegisterSyncHandlers)",
	} {
		if at := strings.Index(string(src), handlers); at < 0 || flush < at {
			t.Fatalf("deferred plugin events must be flushed after %s", handlers)
		}
	}
}

// Plugins' pipeline hooks work before the plugins registered after them:
// they must follow query understanding and top-k filtering and come
// before entity extraction.
func TestPipelineHooksFollowTheirBuiltinStages(t *testing.T) {
	src, err := os.ReadFile("container.go")
	if err != nil {
		t.Fatal(err)
	}
	at := func(s string) int {
		i := strings.Index(string(src), "container.Invoke(chatpipeline."+s+")")
		if i < 0 {
			t.Fatalf("%s is not registered", s)
		}
		return i
	}
	hooks := at("NewPluginExternalHooks")
	if hooks < at("NewPluginQueryUnderstand") || hooks < at("NewPluginFilterTopK") ||
		hooks > at("NewPluginExtractEntity") {
		t.Fatal("pipeline hooks must be registered after query understanding and before entity extraction")
	}
}

// With the kubernetes driver and an egress port, every app node serves the
// egress proxy; it asks for credentials and applies the plugin's grant.
func TestPluginEgressProxyServes(t *testing.T) {
	t.Setenv("JWT_SECRET", "cluster")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	t.Setenv("WEKNORA_PLUGIN_K8S_EGRESS_PORT", strconv.Itoa(port))

	reg := pluginregistry.New()
	if err := reg.Register(&manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion, ID: "acme.kube", Version: "1.0.0", APIVersion: pluginapi.APIVersion,
		Name: manifest.Text("Kube", nil), Publisher: manifest.Publisher{ID: "acme"},
		Runtime:     manifest.Runtime{Type: manifest.RuntimeKubernetes, Image: "x"},
		Contributes: manifest.Contributions{manifest.PointWebSearch: {{ID: "x", Name: manifest.Text("X", nil)}}},
		Permissions: manifest.Permissions{Egress: []string{"api.allowed.test"}},
	}); err != nil {
		t.Fatal(err)
	}
	cleaner := NewResourceCleaner()
	if err := startPluginEgressProxy(nil, reg, cleaner); err != nil {
		t.Fatalf("without the kubernetes driver: %v", err)
	}
	d, err := kube.New(&kube.Config{Namespace: "p", ServiceType: "ClusterIP"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := startPluginEgressProxy(d, reg, cleaner); err != nil {
		t.Fatal(err)
	}
	defer cleaner.Cleanup(context.Background())

	key, err := pluginEgressKey()
	if err != nil {
		t.Fatal(err)
	}
	get := func(user *url.Userinfo) int {
		proxy := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port), User: user}
		c := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}
		resp, err := c.Get("http://evil.test/")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if got := get(nil); got != http.StatusProxyAuthRequired {
		t.Fatalf("without credentials = %d", got)
	}
	if got := get(url.UserPassword("acme.kube", egress.Password(key, "acme.kube"))); got != http.StatusForbidden {
		t.Fatalf("a host outside the grant = %d", got)
	}
}
