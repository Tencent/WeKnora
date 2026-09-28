package host

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
)

// Leases elect the one node that runs each singleton plugin
// (runtime.singleton) across the cluster: a plugin holding a long-lived
// connection, such as an IM bot, must not run twice. Nodes that do not
// hold a plugin's lease leave its calls to the node that does.
type Leases interface {
	// Acquire takes the plugin's lease for this node, or renews it; false
	// when another node holds it.
	Acquire(ctx context.Context, pluginID string) (bool, error)
	// Release gives the lease up, if this node holds it.
	Release(ctx context.Context, pluginID string)
	// TTL is how long a lease lasts without being renewed.
	TTL() time.Duration
}

// alone is the lease of a node without a cluster: it holds every one.
type alone struct{}

func (alone) Acquire(context.Context, string) (bool, error) { return true, nil }
func (alone) Release(context.Context, string)               {}
func (alone) TTL() time.Duration                            { return time.Hour }

// leaseRenew is how often a singleton's lease is renewed, or tried for.
var leaseRenew = 10 * time.Second

// SetLeases makes this host take part in electing the node that runs each
// singleton plugin. Without it the host runs them, as the only node.
func (m *Manager) SetLeases(l Leases) {
	m.mu.Lock()
	m.leases = l
	m.mu.Unlock()
}

func (m *Manager) leasesOrAlone() Leases {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.leases == nil {
		return alone{}
	}
	return m.leases
}

// singleton is one singleton plugin version this node runs while it holds
// the plugin's lease, or stands by to run.
type singleton struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// runSingleton takes a singleton plugin version over from the previous one:
// the previous process stops before this one starts, so the plugin never
// runs twice, even here.
func (m *Manager) runSingleton(mf *manifest.Manifest, sp spec) {
	id := mf.ID
	ctx, cancel := context.WithCancel(context.Background())
	s := &singleton{cancel: cancel, done: make(chan struct{})}
	m.mu.Lock()
	prev := m.singletons[id]
	m.singletons[id] = s
	other := m.procs[id] // a version that was not a singleton
	m.mu.Unlock()
	go func() {
		defer func() {
			m.mu.Lock()
			if m.singletons[id] == s {
				delete(m.singletons, id)
			}
			m.mu.Unlock()
			close(s.done)
		}()
		if prev != nil {
			prev.cancel()
			<-prev.done
		}
		if other != nil {
			m.unserve(id, other)
			other.stop()
		}
		m.holdSingleton(ctx, mf, sp)
	}()
}

// stopSingleton stops a singleton plugin here and gives its lease up, in
// the background. Its entry stays until it stopped, so a version started
// meanwhile waits for it.
func (m *Manager) stopSingleton(pluginID string) {
	m.mu.Lock()
	s := m.singletons[pluginID]
	m.mu.Unlock()
	if s != nil {
		s.cancel()
	}
}

// holdSingleton runs the plugin while this node holds its lease, and stands
// by otherwise, until ctx ends. A node that cannot renew the lease (Redis
// unreachable) stops the plugin before the lease runs out, since another
// node may take it over then.
func (m *Manager) holdSingleton(ctx context.Context, mf *manifest.Manifest, sp spec) {
	id := mf.ID
	leases := m.leasesOrAlone()
	var p *process
	stop := func() {
		if p != nil {
			m.unserve(id, p)
			p.stop()
			p = nil
		}
	}
	defer func() {
		stop()
		rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		leases.Release(rctx, id)
		cancel()
	}()
	renewed := time.Now()
	t := time.NewTicker(leaseRenew)
	defer t.Stop()
	for {
		held, err := leases.Acquire(ctx, id)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			if p != nil && time.Since(renewed) > leases.TTL()*2/3 {
				logger.Warnf(ctx, "[plugin] %s: cannot renew the singleton lease (%v); stopping it here, "+
					"another node may take it over", id, err)
				stop()
			}
		case held:
			renewed = time.Now()
			if p == nil {
				started, err := startProcess(sp, func(pr *process, s State, err error) { m.report(id, pr, s, err) })
				if err != nil {
					logger.Warnf(ctx, "[plugin] %s: start the singleton: %v; trying again in %s", id, err, leaseRenew)
					break
				}
				p = started
				m.serve(ctx, mf, p)
				logger.Infof(ctx, "[plugin] %s runs on this node (singleton)", id)
			}
		case p != nil:
			logger.Warnf(ctx, "[plugin] %s: another node holds the singleton lease now; stopping it here", id)
			stop()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// unserve stops routing a plugin's calls to a process, if they go to it.
func (m *Manager) unserve(pluginID string, p *process) {
	m.mu.Lock()
	if m.procs[pluginID] == p {
		delete(m.procs, pluginID)
	}
	m.mu.Unlock()
	m.changed()
}
