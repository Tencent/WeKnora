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
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

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
	inFlight atomic.Int64

	mu    sync.Mutex
	procs map[string]*process
	kinds map[string]bool
	// standalone hosts have no one to hand other kinds to.
	standalone bool
	// hostAPI is this node's Host API on loopback, for sandboxed plugins.
	hostAPI string
	// direct are hosts plugins reach without the egress proxy: the Host API
	// when it is not on this machine.
	direct []string
}

// NewManager creates an empty host that runs every kind this machine can.
func NewManager() *Manager {
	m := &Manager{procs: map[string]*process{}}
	m.SetKinds(AvailableKinds())
	return m
}

// NewStandaloneManager creates the host of a standalone plugin host
// (weknora plugin-host): it runs the given kinds and refuses the others.
func NewStandaloneManager(kinds []string) *Manager {
	m := &Manager{procs: map[string]*process{}, standalone: true}
	m.SetKinds(kinds)
	return m
}

// AvailableKinds are the kinds this machine can run: binaries, and python
// when an interpreter is installed.
func AvailableKinds() []string {
	kinds := []string{KindBinary}
	if _, err := exec.LookPath(PythonCommand()); err == nil {
		kinds = append(kinds, KindPython)
	}
	return kinds
}

// KindsFromEnv reads a comma-separated list of kinds from an environment
// variable: unset means every available kind, "none" means none.
func KindsFromEnv(name string) []string {
	raw, ok := os.LookupEnv(name)
	raw = strings.TrimSpace(raw)
	if !ok || raw == "" {
		return AvailableKinds()
	}
	if raw == "none" {
		return nil
	}
	var out []string
	for _, k := range strings.Split(raw, ",") {
		if k = strings.TrimSpace(k); Supported(k) {
			out = append(out, k)
		}
	}
	return out
}

// SetKinds limits the kinds this host runs; plugins of other kinds are left
// to plugin hosts elsewhere.
func (m *Manager) SetKinds(kinds []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.kinds = map[string]bool{}
	for _, k := range kinds {
		m.kinds[k] = true
	}
}

// Kinds lists the kinds this host runs.
func (m *Manager) Kinds() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.kinds))
	for k := range m.kinds {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Runs reports whether this host runs plugins of a kind.
func (m *Manager) Runs(kind string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.kinds[kind]
}

// SetDirectHosts names hosts plugins reach without the egress proxy, such
// as the Host API's when it is on another machine. It applies to processes
// started afterwards.
func (m *Manager) SetDirectHosts(hosts ...string) {
	m.mu.Lock()
	m.direct = append([]string(nil), hosts...)
	m.mu.Unlock()
}

// SetHostAPIAddr names this node's Host API on loopback ("127.0.0.1:8080"),
// which sandboxed plugins (WEKNORA_PLUGIN_NETNS) reach through a relay.
func (m *Manager) SetHostAPIAddr(addr string) {
	m.mu.Lock()
	m.hostAPI = addr
	m.mu.Unlock()
}

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
	if !Supported(l.Manifest.Runtime.Kind) {
		return fmt.Errorf("runtime.kind %q is not supported by this host; binary and python are",
			l.Manifest.Runtime.Kind)
	}
	if !m.Runs(l.Manifest.Runtime.Kind) {
		if m.standalone {
			return fmt.Errorf("this plugin host does not run %s plugins (it runs %s)",
				l.Manifest.Runtime.Kind, strings.Join(m.Kinds(), ", "))
		}
		return nil // a plugin host elsewhere runs it
	}
	id := l.Manifest.ID
	m.mu.Lock()
	direct, hostAPI := m.direct, m.hostAPI
	m.mu.Unlock()
	p, err := startProcess(spec{m: l.Manifest, dir: l.Dir, direct: direct, hostAPI: hostAPI}, func(s State, err error) {
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

// Local reports whether a plugin runs on this host.
func (m *Manager) Local(pluginID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.procs[pluginID]
	return ok
}

// Running is one plugin this host runs.
type Running struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Kind    string `json:"kind"`
	State   State  `json:"state"`
}

// Running lists the plugins this host runs and their state.
func (m *Manager) Running() []Running {
	m.mu.Lock()
	procs := make([]*process, 0, len(m.procs))
	for _, p := range m.procs {
		procs = append(procs, p)
	}
	m.mu.Unlock()
	out := make([]Running, 0, len(procs))
	for _, p := range procs {
		p.mu.RLock()
		st := p.state
		p.mu.RUnlock()
		out = append(out, Running{
			ID: p.spec.m.ID, Version: p.spec.m.Version, Kind: p.spec.m.Runtime.Kind, State: st,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// InFlight is how many gateway calls this host is answering.
func (m *Manager) InFlight() int64 { return m.inFlight.Load() }

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
