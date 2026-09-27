package host

import (
	"context"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
)

// memorySampleAge is how long one memory sample of all plugins is reused:
// status reports come in bursts, and ps is a process on some systems. A
// group missing from a sample (a plugin that just started) is measured
// again once the sample is memoryMissAge old.
const (
	memorySampleAge = 10 * time.Second
	memoryMissAge   = time.Second
)

// memorySampler measures the plugins' process groups together and keeps
// the result for memorySampleAge.
type memorySampler struct {
	mu      sync.Mutex
	at      time.Time
	byGroup map[int]int64
	warned  bool
	measure func(map[int]bool) (map[int]int64, error)
	now     func() time.Time
}

func newMemorySampler() *memorySampler {
	return &memorySampler{measure: groupMemory, now: time.Now}
}

// group returns a process group's resident memory, measuring the groups of
// every running plugin when the last sample is too old.
func (s *memorySampler) group(pgid int, all func() map[int]bool) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	age := s.now().Sub(s.at)
	_, seen := s.byGroup[pgid]
	if s.byGroup == nil || age >= memorySampleAge || (!seen && age >= memoryMissAge) {
		sums, err := s.measure(all())
		if err != nil && !s.warned {
			s.warned = true
			logger.Warnf(context.Background(), "[plugin] cannot measure plugin memory: %v", err)
		}
		s.byGroup, s.at = sums, s.now()
	}
	return s.byGroup[pgid]
}

// Usage implements reconcile.UsageReporter: the resident memory of a
// plugin's processes on this host, or that it is stopped while idle.
func (m *Manager) Usage(pluginID string) (reconcile.Usage, bool) {
	m.mu.Lock()
	p := m.procs[pluginID]
	m.mu.Unlock()
	if p == nil {
		return reconcile.Usage{}, false
	}
	p.mu.RLock()
	idle := p.state == StateIdle
	p.mu.RUnlock()
	if idle {
		return reconcile.Usage{Idle: true}, true
	}
	pgid := int(p.group.Load())
	if pgid == 0 {
		return reconcile.Usage{}, true
	}
	return reconcile.Usage{MemoryBytes: m.memory.group(pgid, m.groups)}, true
}

// groups are the process groups of the plugins running on this host.
func (m *Manager) groups() map[int]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[int]bool, len(m.procs))
	for _, p := range m.procs {
		if g := p.group.Load(); g != 0 {
			out[int(g)] = true
		}
	}
	return out
}
