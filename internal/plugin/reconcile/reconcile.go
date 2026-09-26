// Package reconcile makes every node run the plugins the database says should
// run. Installing, upgrading, disabling or removing a plugin only writes rows;
// each node's Reconciler then loads the active version of every enabled plugin
// into the registry, hands its contributions to the domain activators, and
// unloads everything else. A Redis broadcast makes peers reconcile at once; a
// periodic pass catches anything a broadcast missed.
package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/pkg"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

// Loaded is a plugin version loaded on this node.
type Loaded struct {
	Manifest *manifest.Manifest
	Package  *pkg.Package
	// Dir is the package extracted on local disk, for consumers that read
	// files by path (skills).
	Dir string
}

// Activator wires one domain to plugin contributions: it registers what a
// loaded plugin contributes (skills, MCP servers, model vendors) and removes
// it again. Activate is called again for each new version, after Deactivate
// for the old one.
type Activator interface {
	Name() string
	Activate(ctx context.Context, l *Loaded) error
	Deactivate(ctx context.Context, pluginID string) error
}

// InPlaceActivator swaps an upgraded plugin itself inside Activate (the host
// starts the new process before stopping the old one), so it is not
// deactivated first.
type InPlaceActivator interface {
	Activator
	ActivatesInPlace()
}

// Status is how a plugin fares on this node.
type Status struct {
	Version   string    `json:"version"`
	State     string    `json:"state"`
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Node states reported in Status.
const (
	StateReady  = "ready"
	StateFailed = "failed"
	// StateDegraded: loaded, but its process crashed or fails health checks
	// and is being restarted.
	StateDegraded = "degraded"
)

// DefaultInterval is how often a node reconciles without being told to.
const DefaultInterval = 30 * time.Second

const channelBase = "weknora:plugins:changed"

func channel() string {
	if ns := strings.TrimSpace(os.Getenv("WEKNORA_REDIS_NAMESPACE")); ns != "" {
		return channelBase + ":" + ns
	}
	return channelBase
}

type changeMessage struct {
	OriginID string `json:"origin_id"`
}

// Reconciler converges this node to the installed-plugin rows.
type Reconciler struct {
	repo       interfaces.PluginRepository
	store      PackageStore
	registry   *registry.Registry
	cacheDir   string
	rdb        *redis.Client
	activators []Activator
	instanceID string
	interval   time.Duration

	mu       sync.Mutex // serializes passes
	loaded   map[string]*Loaded
	digests  map[string]string // plugin ID → loaded digest
	statusMu sync.RWMutex
	status   map[string]Status
	runOnce  sync.Once
}

// Options configures a Reconciler.
type Options struct {
	Repo       interfaces.PluginRepository
	Store      PackageStore
	Registry   *registry.Registry
	CacheDir   string
	Redis      *redis.Client // nil on single-node deployments
	Activators []Activator
	Interval   time.Duration
}

// New creates a Reconciler.
func New(o Options) *Reconciler {
	if o.Interval <= 0 {
		o.Interval = DefaultInterval
	}
	if o.CacheDir == "" {
		o.CacheDir = DefaultCacheDir()
	}
	return &Reconciler{
		repo: o.Repo, store: o.Store, registry: o.Registry, cacheDir: o.CacheDir, rdb: o.Redis,
		activators: o.Activators, instanceID: uuid.NewString(), interval: o.Interval,
		loaded: map[string]*Loaded{}, digests: map[string]string{}, status: map[string]Status{},
	}
}

// DefaultCacheDir is where packages are extracted unless
// WEKNORA_PLUGIN_CACHE_DIR says otherwise.
func DefaultCacheDir() string {
	if dir := strings.TrimSpace(os.Getenv("WEKNORA_PLUGIN_CACHE_DIR")); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), "weknora-plugins")
}

// Reconcile runs one pass. It keeps going past a plugin that fails to load,
// records the failure in Status, and returns every failure joined.
func (r *Reconciler) Reconcile(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rows, err := r.repo.ListPlugins(ctx)
	if err != nil {
		return fmt.Errorf("list installed plugins: %w", err)
	}
	var errs []error
	desired := map[string]bool{}
	for _, row := range rows {
		if row.DesiredState != types.PluginStateEnabled {
			continue
		}
		desired[row.ID] = true
		if err := r.ensure(ctx, row); err != nil {
			errs = append(errs, fmt.Errorf("plugin %s: %w", row.ID, err))
			r.setStatus(row.ID, Status{Version: row.ActiveVersion, State: StateFailed, Error: err.Error()})
		}
	}
	for id := range r.digests {
		if !desired[id] {
			r.unload(ctx, id)
		}
	}
	// A plugin that never loaded has a status but no digest.
	r.statusMu.Lock()
	for id := range r.status {
		if !desired[id] {
			delete(r.status, id)
			r.forgetStatus(ctx, id)
		}
	}
	r.statusMu.Unlock()
	r.publishStatuses(ctx)
	return errors.Join(errs...)
}

// ensure loads the active version of one plugin unless it already is.
func (r *Reconciler) ensure(ctx context.Context, row types.InstalledPlugin) error {
	v, err := r.repo.GetVersion(ctx, row.ID, row.ActiveVersion)
	if err != nil {
		return err
	}
	if v == nil {
		return fmt.Errorf("version %s is not stored", row.ActiveVersion)
	}
	if r.digests[row.ID] == v.Digest {
		return nil
	}
	data, err := r.store.Get(ctx, v.PackageURI)
	if err != nil {
		return err
	}
	p, err := pkg.Open(data)
	if err != nil {
		return err
	}
	if p.Digest != v.Digest {
		return fmt.Errorf("stored package digest %s does not match %s", p.Digest, v.Digest)
	}
	if p.Manifest.ID != row.ID {
		return fmt.Errorf("package is plugin %s, not %s", p.Manifest.ID, row.ID)
	}
	dir, err := r.extract(p)
	if err != nil {
		return err
	}
	if err := r.registry.Replace(p.Manifest); err != nil {
		return err
	}
	l := &Loaded{Manifest: p.Manifest, Package: p, Dir: dir}
	var errs []error
	for _, a := range r.activators {
		_, inPlace := a.(InPlaceActivator)
		if _, had := r.loaded[row.ID]; had && !inPlace {
			if err := a.Deactivate(ctx, row.ID); err != nil {
				errs = append(errs, fmt.Errorf("%s: deactivate previous version: %w", a.Name(), err))
			}
		}
		if err := a.Activate(ctx, l); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", a.Name(), err))
		}
	}
	r.loaded[row.ID] = l
	r.digests[row.ID] = v.Digest
	if err := errors.Join(errs...); err != nil {
		return err
	}
	logger.Infof(ctx, "[plugin] loaded %s %s", row.ID, row.ActiveVersion)
	r.setStatus(row.ID, Status{Version: row.ActiveVersion, State: StateReady})
	return nil
}

func (r *Reconciler) unload(ctx context.Context, id string) {
	// Reverse order: routes go before the processes they route to.
	for i := len(r.activators) - 1; i >= 0; i-- {
		a := r.activators[i]
		if err := a.Deactivate(ctx, id); err != nil {
			logger.Warnf(ctx, "[plugin] %s: deactivate %s: %v", a.Name(), id, err)
		}
	}
	if err := r.registry.Unregister(id); err != nil {
		logger.Warnf(ctx, "[plugin] unregister %s: %v", id, err)
	}
	delete(r.loaded, id)
	delete(r.digests, id)
	r.statusMu.Lock()
	delete(r.status, id)
	r.statusMu.Unlock()
	r.forgetStatus(ctx, id)
	logger.Infof(ctx, "[plugin] unloaded %s", id)
}

// extract writes the package under cacheDir/<digest>, once per digest.
func (r *Reconciler) extract(p *pkg.Package) (string, error) {
	dir := filepath.Join(r.cacheDir, strings.TrimPrefix(p.Digest, "sha256:"))
	if _, err := os.Stat(filepath.Join(dir, pkg.ManifestFile)); err == nil {
		return dir, nil
	}
	tmp := dir + ".tmp-" + uuid.NewString()[:8]
	for _, name := range p.Files("") {
		data, _ := p.ReadFile(name)
		target, err := utils.SafeJoinUnderBase(tmp, name)
		if err != nil {
			_ = os.RemoveAll(tmp)
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			_ = os.RemoveAll(tmp)
			return "", err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			_ = os.RemoveAll(tmp)
			return "", err
		}
	}
	if err := os.Rename(tmp, dir); err != nil {
		_ = os.RemoveAll(tmp)
		// Another pass extracted the same digest first.
		if _, statErr := os.Stat(filepath.Join(dir, pkg.ManifestFile)); statErr == nil {
			return dir, nil
		}
		return "", err
	}
	return dir, nil
}

func (r *Reconciler) setStatus(id string, s Status) {
	s.UpdatedAt = time.Now()
	r.statusMu.Lock()
	r.status[id] = s
	r.statusMu.Unlock()
}

// ReportRuntime records a loaded plugin's runtime health (a host process that
// crashed and is restarting, or recovered) and publishes it at once.
func (r *Reconciler) ReportRuntime(pluginID string, healthy bool, err error) {
	r.statusMu.Lock()
	s, ok := r.status[pluginID]
	if !ok || s.State == StateFailed {
		r.statusMu.Unlock()
		return
	}
	s.State, s.Error = StateReady, ""
	if !healthy {
		s.State = StateDegraded
		if err != nil {
			s.Error = err.Error()
		}
	}
	s.UpdatedAt = time.Now()
	r.status[pluginID] = s
	r.statusMu.Unlock()
	r.publishStatuses(context.Background())
}

// Status reports how one plugin fares on this node.
func (r *Reconciler) Status(pluginID string) (Status, bool) {
	r.statusMu.RLock()
	defer r.statusMu.RUnlock()
	s, ok := r.status[pluginID]
	return s, ok
}

// Loaded returns the plugins loaded on this node, sorted by ID.
func (r *Reconciler) Loaded() []*Loaded {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Loaded, 0, len(r.loaded))
	for _, l := range r.loaded {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Manifest.ID < out[j].Manifest.ID })
	return out
}

// Notify tells peer nodes to reconcile. Best effort: the periodic pass
// catches up when Redis is down or absent.
func (r *Reconciler) Notify(ctx context.Context) {
	if r.rdb == nil {
		return
	}
	payload, _ := json.Marshal(changeMessage{OriginID: r.instanceID})
	if err := r.rdb.Publish(ctx, channel(), payload).Err(); err != nil {
		logger.Warnf(ctx, "[plugin] publish change: %v", err)
	}
}

// Start reconciles once, then keeps reconciling on broadcasts and on a timer
// until ctx ends. Calling it twice has no effect.
func (r *Reconciler) Start(ctx context.Context) {
	r.runOnce.Do(func() {
		if err := r.Reconcile(ctx); err != nil {
			logger.Warnf(ctx, "[plugin] initial reconcile: %v", err)
		}
		wake := make(chan struct{}, 1)
		if r.rdb != nil {
			go r.subscribe(ctx, wake)
		}
		go r.loop(ctx, wake)
	})
}

func (r *Reconciler) loop(ctx context.Context, wake <-chan struct{}) {
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-wake:
		}
		if err := r.Reconcile(ctx); err != nil {
			logger.Warnf(ctx, "[plugin] reconcile: %v", err)
		}
	}
}

// subscribe forwards peer broadcasts to wake, reconnecting with backoff.
func (r *Reconciler) subscribe(ctx context.Context, wake chan<- struct{}) {
	const maxBackoff = 30 * time.Second
	backoff := time.Second
	for ctx.Err() == nil {
		sub := r.rdb.Subscribe(ctx, channel())
		if _, err := sub.Receive(ctx); err != nil {
			_ = sub.Close()
			logger.Warnf(ctx, "[plugin] subscribe: %v (retry in %s)", err, backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		backoff = time.Second
		for msg := range sub.Channel() {
			var m changeMessage
			if json.Unmarshal([]byte(msg.Payload), &m) == nil && m.OriginID == r.instanceID {
				continue
			}
			select {
			case wake <- struct{}{}:
			default: // a pass is already pending
			}
		}
		_ = sub.Close()
	}
}
