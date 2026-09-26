package container

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/Tencent/WeKnora/internal/plugin/host"
	"github.com/Tencent/WeKnora/internal/plugin/hostpool"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
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
