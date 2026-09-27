// Package remote reaches remote-runtime plugins: HTTP services an
// administrator runs somewhere else and registers by URL. Every request is
// signed with the plugin's shared secret, so the service can tell WeKnora
// from anyone else who can reach it.
package remote

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/host"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

const (
	healthInterval = 15 * time.Second
	checkTimeout   = 10 * time.Second
)

// Manager keeps a client for every loaded remote plugin and health-checks
// it. It is a reconcile.Activator and, like the embedded host, must come
// before the activators that route calls to the plugins.
type Manager struct {
	newClient func(url string, secret []byte) *client.Client
	// newTrustedClient reaches services WeKnora deployed itself (the
	// kubernetes driver), which live on private cluster addresses.
	newTrustedClient func(url string, secret []byte) *client.Client
	interval         time.Duration

	mu        sync.Mutex
	reporter  host.StateReporter
	endpoints map[string]*endpoint
}

// endpoint is one registered remote plugin.
type endpoint struct {
	m *manifest.Manifest
	// deployed marks a service WeKnora deployed itself (ServeDeployed):
	// the kubernetes driver withdraws it, not a remote plugin's switch.
	deployed bool
	c        *client.Client
	cancel   context.CancelFunc
	done     chan struct{}

	mu sync.Mutex
	// err is why the service cannot be called right now: unreachable,
	// or serving something other than the installed package.
	err error
}

// NewManager creates a Manager whose clients refuse private addresses
// unless SSRF_WHITELIST allows them.
func NewManager() *Manager {
	cfg := utils.DefaultSSRFSafeHTTPClientConfig()
	cfg.Timeout = 0 // calls are bounded by their context; syncs stream
	cfg.SameOriginRedirectsOnly = true
	httpClient := utils.NewSSRFSafeHTTPClient(cfg)
	trusted := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	return &Manager{
		newClient: func(url string, secret []byte) *client.Client {
			return client.New(url, httpClient, client.Signed(secret))
		},
		newTrustedClient: func(url string, secret []byte) *client.Client {
			return client.New(url, trusted, client.Signed(secret))
		},
		interval:  healthInterval,
		endpoints: map[string]*endpoint{},
	}
}

// SetReporter wires health changes to the reconciler.
func (m *Manager) SetReporter(r host.StateReporter) {
	m.mu.Lock()
	m.reporter = r
	m.mu.Unlock()
}

// Name implements reconcile.Activator.
func (m *Manager) Name() string { return "remote" }

// Stage checks that the registered service is up and serves the installed
// package. From the commit on, calls go to it, a new URL or version taking
// over from the old endpoint without a gap, and it is health-checked. A
// version that no longer runs remotely withdraws the service once the new
// runtime took over.
func (m *Manager) Stage(ctx context.Context, _, l *reconcile.Loaded) (reconcile.Staged, error) {
	if l.Manifest.Runtime.Type != manifest.RuntimeRemote {
		id := l.Manifest.ID
		return reconcile.Swap{OnRetire: func() { m.withdraw(ctx, id, false) }}, nil
	}
	url := l.Installed.RemoteURL
	if url == "" {
		return nil, errors.New("no service URL is registered for this remote plugin")
	}
	if err := utils.ValidateURLForSSRF(url); err != nil {
		return nil, fmt.Errorf("service URL is not allowed: %w", err)
	}
	secret, err := utils.DecryptStoredSecret(l.Installed.RemoteSecret)
	if err != nil {
		return nil, fmt.Errorf("decrypt the plugin secret: %w", err)
	}
	if secret == "" {
		return nil, errors.New("the remote plugin has no signing secret")
	}
	c := m.newClient(url, []byte(secret))
	if err := check(ctx, c, l.Manifest); err != nil {
		return nil, err
	}
	return reconcile.Swap{
		OnCommit: func() { m.serve(ctx, l.Manifest, url, c, false) },
		OnAbort:  c.Close,
	}, nil
}

// Deployed is a plugin service WeKnora deployed itself, checked and ready
// to take the plugin's calls.
type Deployed struct {
	m   *Manager
	mf  *manifest.Manifest
	url string
	c   *client.Client
}

// PrepareDeployed checks a plugin service WeKnora deployed itself (the
// kubernetes driver) at url, reached on the cluster's private addresses.
// Serve registers it; Close drops it unregistered.
func (m *Manager) PrepareDeployed(ctx context.Context, mf *manifest.Manifest, url, secret string) (*Deployed, error) {
	c := m.newTrustedClient(url, []byte(secret))
	if err := check(ctx, c, mf); err != nil {
		return nil, err
	}
	return &Deployed{m: m, mf: mf, url: url, c: c}, nil
}

// Serve routes the plugin's calls to the service and health-watches it
// like a remote plugin's.
func (d *Deployed) Serve(ctx context.Context) { d.m.serve(ctx, d.mf, d.url, d.c, true) }

// Close drops a service that was never served.
func (d *Deployed) Close() { d.c.Close() }

// ServeDeployed checks and serves a deployed service at once.
func (m *Manager) ServeDeployed(ctx context.Context, mf *manifest.Manifest, url, secret string) error {
	d, err := m.PrepareDeployed(ctx, mf, url, secret)
	if err != nil {
		return err
	}
	d.Serve(ctx)
	return nil
}

// WithdrawDeployed unregisters a service ServeDeployed registered, but not
// a remote plugin's service registered since.
func (m *Manager) WithdrawDeployed(ctx context.Context, pluginID string) {
	m.withdraw(ctx, pluginID, true)
}

// check verifies a new client's service within the check timeout and
// closes the client if it fails.
func check(ctx context.Context, c *client.Client, mf *manifest.Manifest) error {
	cctx, cancel := context.WithTimeout(ctx, checkTimeout)
	err := verify(cctx, c, mf)
	cancel()
	if err != nil {
		c.Close()
	}
	return err
}

// serve routes a plugin's calls to a checked client and health-watches it.
func (m *Manager) serve(ctx context.Context, mf *manifest.Manifest, url string, c *client.Client, deployed bool) {
	id := mf.ID
	wctx, stop := context.WithCancel(context.Background())
	e := &endpoint{m: mf, deployed: deployed, c: c, cancel: stop, done: make(chan struct{})}
	m.mu.Lock()
	old := m.endpoints[id]
	m.endpoints[id] = e
	m.mu.Unlock()
	if old != nil {
		old.close()
	}
	go m.watch(wctx, id, e)
	logger.Infof(ctx, "[plugin] remote %s %s at %s", id, mf.Version, url)
}

// verify checks that the service is healthy and is the installed package.
func verify(ctx context.Context, c *client.Client, want *manifest.Manifest) error {
	if err := c.Health(ctx); err != nil {
		return fmt.Errorf("plugin service is not healthy: %w", err)
	}
	got, err := c.Manifest(ctx)
	if err != nil {
		return fmt.Errorf("read plugin manifest: %w", err)
	}
	return host.CheckServedManifest(want, got)
}

// watch health-checks the service until the endpoint is replaced or
// removed. After an outage it checks the manifest again: the service may
// have been redeployed with another version.
func (m *Manager) watch(ctx context.Context, id string, e *endpoint) {
	defer close(e.done)
	t := time.NewTicker(m.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cctx, cancel := context.WithTimeout(ctx, checkTimeout)
		var err error
		if e.failing() {
			err = verify(cctx, e.c, e.m)
		} else if err = e.c.Health(cctx); err != nil {
			err = fmt.Errorf("plugin service is not healthy: %w", err)
		}
		cancel()
		if ctx.Err() != nil {
			return
		}
		if e.setErr(err) {
			m.report(id, err)
		}
	}
}

func (m *Manager) report(id string, err error) {
	m.mu.Lock()
	r := m.reporter
	m.mu.Unlock()
	if r != nil {
		r.ReportRuntime(id, err == nil, err)
	}
}

func (e *endpoint) failing() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.err != nil
}

// setErr records the latest check and says whether health changed.
func (e *endpoint) setErr(err error) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	changed := (e.err == nil) != (err == nil)
	e.err = err
	return changed
}

func (e *endpoint) close() {
	e.cancel()
	<-e.done
	e.c.Close()
}

// Deactivate implements reconcile.Activator.
func (m *Manager) Deactivate(ctx context.Context, pluginID string) error {
	m.mu.Lock()
	e := m.endpoints[pluginID]
	delete(m.endpoints, pluginID)
	m.mu.Unlock()
	if e != nil {
		e.close()
		logger.Infof(ctx, "[plugin] remote %s unregistered", pluginID)
	}
	return nil
}

// withdraw unregisters a plugin's service if it is a deployed one or not,
// as asked.
func (m *Manager) withdraw(ctx context.Context, pluginID string, deployed bool) {
	m.mu.Lock()
	e := m.endpoints[pluginID]
	if e == nil || e.deployed != deployed {
		m.mu.Unlock()
		return
	}
	delete(m.endpoints, pluginID)
	m.mu.Unlock()
	e.close()
	logger.Infof(ctx, "[plugin] remote %s unregistered", pluginID)
}

// Client returns the client of a registered remote plugin. The error is a
// retryable pluginapi unavailable error while the service fails its checks.
func (m *Manager) Client(pluginID string) (*client.Client, error) {
	m.mu.Lock()
	e := m.endpoints[pluginID]
	m.mu.Unlock()
	if e == nil {
		return nil, pluginapi.Errorf(pluginapi.CodeUnavailable, "remote plugin %s is not registered", pluginID)
	}
	e.mu.Lock()
	err := e.err
	e.mu.Unlock()
	if err != nil {
		return nil, pluginapi.Errorf(pluginapi.CodeUnavailable, "%v", err)
	}
	return e.c, nil
}

// Owns says whether a plugin is registered here.
func (m *Manager) Owns(pluginID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.endpoints[pluginID]
	return ok
}

// Close unregisters every plugin, for shutdown.
func (m *Manager) Close() {
	m.mu.Lock()
	eps := m.endpoints
	m.endpoints = map[string]*endpoint{}
	m.mu.Unlock()
	for _, e := range eps {
		e.close()
	}
}
