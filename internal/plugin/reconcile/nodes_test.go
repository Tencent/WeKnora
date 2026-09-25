package reconcile

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

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
	instances, err := b.Driver().Status(ctx, "acme.kit")
	if err != nil || len(instances) != 2 || instances[0].State != "ready" {
		t.Fatalf("instances = %+v, %v", instances, err)
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
