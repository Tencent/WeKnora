package host

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
)

// memLeases is one cluster's leases, shared by the managers of a test.
type memLeases struct {
	mu     sync.Mutex
	holder map[string]string
}

type nodeLeases struct {
	c    *memLeases
	node string
}

func (l nodeLeases) Acquire(_ context.Context, id string) (bool, error) {
	l.c.mu.Lock()
	defer l.c.mu.Unlock()
	if h := l.c.holder[id]; h != "" && h != l.node {
		return false, nil
	}
	l.c.holder[id] = l.node
	return true, nil
}

func (l nodeLeases) Release(_ context.Context, id string) {
	l.c.mu.Lock()
	defer l.c.mu.Unlock()
	if l.c.holder[id] == l.node {
		delete(l.c.holder, id)
	}
}

func (nodeLeases) TTL() time.Duration { return time.Minute }

// A singleton plugin runs on the one node holding its lease; when that node
// stops it, another takes it over. An upgrade stops the previous process
// before starting the new one.
func TestSingletonRunsOnOneNode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process checks use signal 0")
	}
	fastTimings(t)
	old := leaseRenew
	leaseRenew = 50 * time.Millisecond
	t.Cleanup(func() { leaseRenew = old })
	ctx := context.Background()
	cluster := &memLeases{holder: map[string]string{}}
	a, b := NewManager(), NewManager()
	a.SetLeases(nodeLeases{cluster, "a"})
	b.SetLeases(nodeLeases{cluster, "b"})
	defer a.Close()
	defer b.Close()
	singleton := func(version string) *reconcile.Loaded {
		l := install(t, version, "")
		l.Manifest.Runtime.Singleton = true
		return l
	}
	if err := reconcile.Activate(ctx, a, singleton("1.0.0")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a to run it", func() bool { return a.Local("acme.echo") })
	if err := reconcile.Activate(ctx, b, singleton("1.0.0")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(4 * leaseRenew)
	if b.Local("acme.echo") {
		t.Fatal("both nodes run the singleton")
	}
	pid1, err := search(t, a, "pid")
	if err != nil {
		t.Fatal(err)
	}

	// An upgrade on a: the old process is gone before the new one runs.
	if err := reconcile.Activate(ctx, a, singleton("1.1.0")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the upgrade to run", func() bool {
		for _, r := range a.Running() {
			if r.Version == "1.1.0" && r.State == StateReady {
				return true
			}
		}
		return false
	})
	if !exited(pid1) {
		t.Fatal("the previous version still runs beside the upgrade")
	}

	// a lets go: b takes over.
	if err := a.Deactivate(ctx, "acme.echo"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "b to take over", func() bool { return b.Local("acme.echo") })
	if _, err := search(t, b, "hello"); err != nil {
		t.Fatal(err)
	}
}
