// Package host is the embedded plugin host: it runs host-runtime plugins as
// child processes of this WeKnora node and hands out clients to reach them.
// Each node runs its own copy of every enabled host plugin (the reconciler
// activates it everywhere), so a call never leaves the node.
//
// A plugin process gets a private socket, a random token, an environment
// built from scratch (no WeKnora secrets) and an egress proxy limited to the
// hosts its manifest was granted. The host checks that the process is the
// installed package, health-checks it and restarts it with backoff.
package host

import (
	"context"
	"fmt"
	"sync"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// StateReporter learns when a running plugin's health changes, so the
// cluster view shows a crashed plugin as degraded.
type StateReporter interface {
	ReportRuntime(pluginID string, healthy bool, err error)
}

// Manager runs host plugins. It is a reconcile.Activator and must come
// before the activators that route calls to the plugins.
type Manager struct {
	reporter StateReporter

	mu    sync.Mutex
	procs map[string]*process
}

// NewManager creates an empty host.
func NewManager() *Manager { return &Manager{procs: map[string]*process{}} }

// SetReporter wires health changes to the reconciler.
func (m *Manager) SetReporter(r StateReporter) {
	m.mu.Lock()
	m.reporter = r
	m.mu.Unlock()
}

// Name implements reconcile.Activator.
func (m *Manager) Name() string { return "host" }

// ActivatesInPlace implements reconcile.InPlaceActivator: an upgrade starts
// the new process before the old one stops.
func (m *Manager) ActivatesInPlace() {}

// Activate starts a host plugin and returns once it is ready. Other runtimes
// are ignored. A new version replaces the old process only after it started.
func (m *Manager) Activate(ctx context.Context, l *reconcile.Loaded) error {
	if l.Manifest.Runtime.Type != manifest.RuntimeHost {
		return nil
	}
	if l.Manifest.Runtime.Kind != "binary" {
		return fmt.Errorf("runtime.kind %q is not supported by this host yet; only binary", l.Manifest.Runtime.Kind)
	}
	id := l.Manifest.ID
	p, err := startProcess(spec{m: l.Manifest, dir: l.Dir}, func(s State, err error) {
		m.report(id, s, err)
	})
	if err != nil {
		return err
	}
	m.mu.Lock()
	old := m.procs[id]
	m.procs[id] = p
	m.mu.Unlock()
	if old != nil {
		old.stop()
	}
	logger.Infof(ctx, "[plugin] host started %s %s", id, l.Manifest.Version)
	return nil
}

// Deactivate implements reconcile.Activator: it stops the process.
func (m *Manager) Deactivate(ctx context.Context, pluginID string) error {
	m.mu.Lock()
	p := m.procs[pluginID]
	delete(m.procs, pluginID)
	m.mu.Unlock()
	if p != nil {
		p.stop()
		logger.Infof(ctx, "[plugin] host stopped %s", pluginID)
	}
	return nil
}

func (m *Manager) report(pluginID string, s State, err error) {
	m.mu.Lock()
	r := m.reporter
	m.mu.Unlock()
	if r == nil {
		return
	}
	switch s {
	case StateReady:
		r.ReportRuntime(pluginID, true, nil)
	case StateDegraded:
		r.ReportRuntime(pluginID, false, err)
	}
}

// Client returns a client for a running plugin. The error is a retryable
// pluginapi unavailable error while the plugin is starting or restarting.
func (m *Manager) Client(pluginID string) (*client.Client, error) {
	m.mu.Lock()
	p := m.procs[pluginID]
	m.mu.Unlock()
	if p == nil {
		return nil, &pluginapi.Error{
			Code: pluginapi.CodeUnavailable, Message: fmt.Sprintf("plugin %s is not running on this node", pluginID),
		}
	}
	return p.Client()
}

// Close stops every plugin, for shutdown.
func (m *Manager) Close() {
	m.mu.Lock()
	procs := m.procs
	m.procs = map[string]*process{}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, p := range procs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.stop()
		}()
	}
	wg.Wait()
}
