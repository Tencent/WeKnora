package host

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
)

func state(m *Manager, id string) State {
	for _, r := range m.Running() {
		if r.ID == id {
			return r.State
		}
	}
	return ""
}

func exited(pid string) bool {
	n, _ := strconv.Atoi(pid)
	proc, err := os.FindProcess(n)
	return err != nil || proc.Signal(syscall.Signal(0)) != nil
}

// A plugin without calls is stopped after the idle timeout and started
// again by the next call, which waits for it; the host still runs it as
// far as routing and plugin hosts are concerned.
func TestIdlePluginStopsAndStartsOnTheNextCall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process checks use signal 0")
	}
	fastTimings(t)
	ctx := context.Background()
	m := NewManager()
	m.SetIdleTimeout(300 * time.Millisecond)
	rep := &reports{}
	m.SetReporter(rep)
	defer m.Close()
	if err := reconcile.Activate(ctx, m, install(t, "1.0.0", "")); err != nil {
		t.Fatal(err)
	}
	pid1, err := search(t, m, "pid")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the idle plugin to stop", func() bool { return state(m, "acme.echo") == StateIdle })
	waitFor(t, "the idle process to exit", func() bool { return exited(pid1) })
	if !m.Local("acme.echo") {
		t.Fatal("an idle plugin must still route here")
	}

	pid2, err := search(t, m, "pid")
	if err != nil || pid2 == pid1 {
		t.Fatalf("after waking pid = %s (was %s), %v", pid2, pid1, err)
	}
	if state(m, "acme.echo") != StateReady {
		t.Fatalf("state after waking = %s", state(m, "acme.echo"))
	}
	if got := rep.String(); got != "acme.echo:true" {
		t.Fatalf("stopping and starting an idle plugin is not a health change: %s", got)
	}
}

// A call in flight, a stream included, keeps the plugin running past the
// timeout; health checks do not.
func TestCallsInFlightKeepAPluginRunning(t *testing.T) {
	fastTimings(t)
	ctx := context.Background()
	m := NewManager()
	m.SetIdleTimeout(200 * time.Millisecond)
	defer m.Close()
	if err := reconcile.Activate(ctx, m, install(t, "1.0.0", "")); err != nil {
		t.Fatal(err)
	}
	if got, err := search(t, m, "sleep:900ms"); err != nil || got != "slept" {
		t.Fatalf("slow call = %q, %v", got, err)
	}
	if st := state(m, "acme.echo"); st != StateReady {
		t.Fatalf("a plugin was stopped with a call in flight: %s", st)
	}
	// healthInterval is 100ms here: checks run, yet it goes idle.
	waitFor(t, "the idle plugin to stop", func() bool { return state(m, "acme.echo") == StateIdle })
}

// A plugin that asks to keep running is never stopped for being idle.
func TestKeepAlivePluginsStayRunning(t *testing.T) {
	fastTimings(t)
	ctx := context.Background()
	m := NewManager()
	m.SetIdleTimeout(100 * time.Millisecond)
	defer m.Close()
	l := install(t, "1.0.0", "")
	l.Manifest.Runtime.KeepAlive = true
	if err := reconcile.Activate(ctx, m, l); err != nil {
		t.Fatal(err)
	}
	time.Sleep(600 * time.Millisecond)
	if st := state(m, "acme.echo"); st != StateReady {
		t.Fatalf("a keepAlive plugin went %s", st)
	}
}

// Callers that find a plugin idle at once share one start.
func TestConcurrentCallsWakeAPluginOnce(t *testing.T) {
	fastTimings(t)
	ctx := context.Background()
	m := NewManager()
	m.SetIdleTimeout(200 * time.Millisecond)
	defer m.Close()
	if err := reconcile.Activate(ctx, m, install(t, "1.0.0", "")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the idle plugin to stop", func() bool { return state(m, "acme.echo") == StateIdle })
	var wg sync.WaitGroup
	pids := make([]string, 8)
	for i := range pids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pids[i], _ = search(t, m, "pid")
		}()
	}
	wg.Wait()
	for _, pid := range pids {
		if pid == "" || pid != pids[0] {
			t.Fatalf("pids = %v, want one process", pids)
		}
	}
}

// A caller gives up on a plugin that takes too long to start, with an
// error to retry.
func TestWakingIsBoundedByTheCaller(t *testing.T) {
	fastTimings(t)
	ctx := context.Background()
	m := NewManager()
	m.SetIdleTimeout(200 * time.Millisecond)
	defer m.Close()
	if err := reconcile.Activate(ctx, m, install(t, "1.0.0", "")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the idle plugin to stop", func() bool { return state(m, "acme.echo") == StateIdle })
	cctx, cancel := context.WithTimeout(ctx, time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	if _, err := m.Client(cctx, "acme.echo"); err == nil {
		t.Fatal("want an error once the caller's deadline passed")
	}
	// The start it asked for goes on; the next caller gets the plugin.
	if _, err := search(t, m, "hello"); err != nil {
		t.Fatal(err)
	}
}
