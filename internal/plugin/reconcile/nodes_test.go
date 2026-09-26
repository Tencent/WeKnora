package reconcile

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/Tencent/WeKnora/internal/plugin/driver"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/types"
)

// Two nodes share a database and Redis: installing on one reaches the other
// through the broadcast, and each sees the other's status.
func TestNodesConvergeThroughRedis(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	repo, store := plugintest.NewMemRepo(), &plugintest.MemStore{}
	newNode := func() (*Reconciler, *registry.Registry) {
		reg := registry.New()
		return New(Options{
			Repo: repo, Store: store, Registry: reg, CacheDir: t.TempDir(), Redis: rdb,
			Interval: time.Hour,
		}), reg
	}
	a, _ := newNode()
	b, regB := newNode()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.Start(ctx)
	b.Start(ctx)
	waitFor(t, func() bool { return mr.PubSubNumSub(channel())[channel()] == 2 })

	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.0.0"), types.PluginStateEnabled)
	if err := a.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	a.Notify(ctx)
	waitFor(t, func() bool { _, ok := regB.Plugin("acme.kit"); return ok })

	waitFor(t, func() bool {
		nodes, err := a.NodeStatuses(ctx, "acme.kit")
		return err == nil && len(nodes) == 2
	})
	instances, err := b.Driver(manifest.RuntimeDeclarative).Status(ctx, "acme.kit")
	if err != nil || len(instances) != 2 || instances[0].State != "ready" {
		t.Fatalf("instances = %+v, %v", instances, err)
	}
}

// egressActivator runs every plugin with one egress mode.
type egressActivator struct {
	recorder
	mode driver.EgressMode
}

func (a *egressActivator) Egress(string) driver.EgressMode { return a.mode }

// Each node reports how it controls the egress of the plugin code it runs,
// and the instances show every node's own report.
func TestNodesReportTheirOwnEgress(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	repo, store := plugintest.NewMemRepo(), &plugintest.MemStore{}
	newNode := func(role string, mode driver.EgressMode) *Reconciler {
		return New(Options{
			Repo: repo, Store: store, Registry: registry.New(), CacheDir: t.TempDir(), Redis: rdb,
			Interval: time.Hour, Role: role, Activators: []Activator{&egressActivator{mode: mode}},
		})
	}
	a := newNode("a", driver.EgressSandboxed)
	b := newNode("b", driver.EgressProxy)
	ctx := context.Background()
	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.0.0"), types.PluginStateEnabled)
	for _, r := range []*Reconciler{a, b} {
		if err := r.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if s, _ := a.Status("acme.kit"); s.Egress != driver.EgressSandboxed {
		t.Fatalf("status = %+v", s)
	}
	instances, err := a.Driver(manifest.RuntimeHost).Status(ctx, "acme.kit")
	if err != nil || len(instances) != 2 {
		t.Fatalf("instances = %+v, %v", instances, err)
	}
	got := map[string]driver.EgressMode{}
	for _, in := range instances {
		got[in.Node[:1]] = in.Egress
	}
	if got["a"] != driver.EgressSandboxed || got["b"] != driver.EgressProxy {
		t.Fatalf("egress by node = %v", got)
	}
}

// Nodes that run no plugin code report no egress: a declarative plugin has
// none, a remote one runs where WeKnora has no say.
func TestNodeDriverEgressWithoutReports(t *testing.T) {
	repo, store := plugintest.NewMemRepo(), &plugintest.MemStore{}
	r := New(Options{Repo: repo, Store: store, Registry: registry.New(), CacheDir: t.TempDir()})
	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.0.0"), types.PluginStateEnabled)
	ctx := context.Background()
	if err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	for rt, want := range map[manifest.RuntimeType]driver.EgressMode{
		manifest.RuntimeDeclarative: "",
		manifest.RuntimeHost:        "",
		manifest.RuntimeRemote:      driver.EgressUnmanaged,
	} {
		instances, err := r.Driver(rt).Status(ctx, "acme.kit")
		if err != nil || len(instances) != 1 || instances[0].Egress != want {
			t.Errorf("%s instances = %+v, %v; want egress %q", rt, instances, err, want)
		}
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
