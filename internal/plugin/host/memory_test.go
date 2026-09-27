package host

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
)

// One sample serves every plugin until it is memorySampleAge old.
func TestMemorySampleIsShared(t *testing.T) {
	now := time.Unix(1000, 0)
	calls := 0
	s := &memorySampler{
		now: func() time.Time { return now },
		measure: func(map[int]bool) (map[int]int64, error) {
			calls++
			return map[int]int64{1: 10 << 20, 2: 20 << 20}, nil
		},
	}
	all := func() map[int]bool { return map[int]bool{1: true, 2: true} }
	if s.group(1, all) != 10<<20 || s.group(2, all) != 20<<20 || calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
	now = now.Add(memorySampleAge)
	s.group(1, all)
	if calls != 2 {
		t.Fatalf("an old sample was reused: calls = %d", calls)
	}
	// A group the sample lacks (a plugin that just started) is measured
	// again, but not more than once a memoryMissAge.
	s.group(3, all)
	if calls != 2 {
		t.Fatalf("a fresh sample was measured again for a new group: calls = %d", calls)
	}
	now = now.Add(memoryMissAge)
	s.group(3, all)
	if calls != 3 {
		t.Fatalf("a missing group was not measured again: calls = %d", calls)
	}
	s.measure = func(map[int]bool) (map[int]int64, error) { return nil, errors.New("no ps") }
	now = now.Add(memorySampleAge)
	if got := s.group(1, all); got != 0 {
		t.Fatalf("unmeasured memory = %d", got)
	}
}

// A running plugin reports the memory of its process; an idle one says so.
func TestUsageReportsMemoryAndIdle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("plugin memory is not measured on Windows")
	}
	fastTimings(t)
	ctx := context.Background()
	m := NewManager()
	m.SetIdleTimeout(300 * time.Millisecond)
	defer m.Close()
	if _, ok := m.Usage("acme.echo"); ok {
		t.Fatal("usage of a plugin this host does not run")
	}
	if err := reconcile.Activate(ctx, m, install(t, "1.0.0", "")); err != nil {
		t.Fatal(err)
	}
	u, ok := m.Usage("acme.echo")
	// The echo plugin is a Go binary: a few MB at least.
	if !ok || u.Idle || u.MemoryBytes < 1<<20 {
		t.Fatalf("usage = %+v, %v", u, ok)
	}
	var _ reconcile.UsageReporter = m
	waitFor(t, "the idle plugin to stop", func() bool { return state(m, "acme.echo") == StateIdle })
	if u, _ := m.Usage("acme.echo"); !u.Idle || u.MemoryBytes != 0 {
		t.Fatalf("idle usage = %+v", u)
	}
}
