// Package reconcile makes every node run the plugins the database says should
// run. Installing, upgrading, disabling or removing a plugin only writes rows;
// each node's Reconciler then loads the active version of every enabled plugin
// into the registry, hands its contributions to the domain activators, and
// unloads everything else. A Redis broadcast makes peers reconcile at once; a
// periodic pass catches anything a broadcast missed.
package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/Tencent/WeKnora/internal/plugin/driver"
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
	// Installed is the plugin's row: where a remote plugin runs and its
	// sealed secret.
	Installed types.InstalledPlugin
	// Report records how the plugin fares after Activate returned, for an
	// activator that finishes in the background (see Pending). It does
	// nothing once this version is no longer the loaded one.
	Report func(healthy bool, err error)
}

// Activator wires one domain to plugin contributions: it registers what a
// loaded plugin contributes (skills, MCP servers, model vendors) and removes
// it again.
//
// A version loads in two steps, so an upgrade that fails leaves the running
// version in place: every activator stages the new version beside the one
// it serves, and only when all of them staged does each commit.
type Activator interface {
	Name() string
	// Stage readies next without changing what the activator serves: it
	// checks what next contributes and starts what must run, but routes
	// nothing to it. prev is the version loaded now, nil on a first load.
	// A runtime that ran prev but does not run next (the plugin moved to
	// another runtime) stops it once the new runtime took over (Retiring).
	// On failure it leaves nothing behind and returns a nil Staged. A
	// PendingError comes with a Staged: the plugin commits and finishes
	// starting in the background.
	Stage(ctx context.Context, prev, next *Loaded) (Staged, error)
	// Deactivate removes everything the activator has for a plugin.
	Deactivate(ctx context.Context, pluginID string) error
}

// Staged is a version an activator staged.
type Staged interface {
	// Commit serves the staged version in place of the previous one. It
	// cannot fail: whatever could go wrong went wrong in Stage.
	Commit()
	// Abort drops the staged version; the previous one stays.
	Abort()
}

// Retiring is a Staged that also has something to stop once every
// activator committed, such as the runtime a plugin moved away from: the
// new runtime has taken the calls over by then.
type Retiring interface {
	Staged
	Retire()
}

// Swap is a Staged from functions; any may be nil.
type Swap struct{ OnCommit, OnAbort, OnRetire func() }

// Commit implements Staged.
func (s Swap) Commit() {
	if s.OnCommit != nil {
		s.OnCommit()
	}
}

// Abort implements Staged.
func (s Swap) Abort() {
	if s.OnAbort != nil {
		s.OnAbort()
	}
}

// Retire implements Retiring.
func (s Swap) Retire() {
	if s.OnRetire != nil {
		s.OnRetire()
	}
}

// Unchanged is the Staged of an activator with nothing to do for a version.
var Unchanged Staged = Swap{}

// Activate stages l on a and commits it at once, for callers outside a
// reconciler pass (tests, tools). A PendingError is returned after the
// commit.
func Activate(ctx context.Context, a Activator, l *Loaded) error {
	s, err := a.Stage(ctx, nil, l)
	if s != nil {
		s.Commit()
		if r, ok := s.(Retiring); ok {
			r.Retire()
		}
	}
	return err
}

// EgressReporter is an Activator that runs plugin code and knows how the
// code's outbound traffic is controlled, for the node's status report.
type EgressReporter interface {
	Activator
	// Egress is empty for a plugin the activator does not run.
	Egress(pluginID string) driver.EgressMode
}

// PendingError is returned by an activator that started the plugin but
// finishes in the background, such as a kubernetes rollout: the plugin is
// loaded and shows as degraded with the reason until the activator reports
// it healthy through Loaded.Report.
type PendingError struct{ Err error }

func (e *PendingError) Error() string { return e.Err.Error() }
func (e *PendingError) Unwrap() error { return e.Err }

// Pending wraps err as a PendingError.
func Pending(err error) error { return &PendingError{Err: err} }

// Status is how a plugin fares on this node.
type Status struct {
	Version   string    `json:"version"`
	State     string    `json:"state"`
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
	// Egress is how the plugin's outbound traffic is controlled where this
	// node runs its code; empty when the node runs none of it.
	Egress driver.EgressMode `json:"egress,omitempty"`
	// UpgradeVersion is a newer active version the node failed to load;
	// it keeps serving Version meanwhile and says why in UpgradeError.
	UpgradeVersion string `json:"upgradeVersion,omitempty"`
	UpgradeError   string `json:"upgradeError,omitempty"`
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
	runtimes   map[string]bool
	accept     func(*manifest.Manifest) bool
	admit      func(*pkg.Package) error
	role       string

	mu      sync.Mutex // serializes passes
	loaded  map[string]*Loaded
	digests map[string]string // plugin ID → loaded digest and runtime target
	// retries holds plugins whose activation failed: a process that would
	// not start, a remote service that was down. They are tried again with
	// backoff until they load or change.
	retries  map[string]retry
	now      func() time.Time
	statusMu sync.RWMutex
	status   map[string]Status
	runOnce  sync.Once
}

// retry is when a failed activation is tried again.
type retry struct {
	key      string // the load key that failed
	attempts int
	next     time.Time
}

// Backoff between attempts to activate a plugin that failed.
const (
	retryFloor   = 30 * time.Second
	retryCeiling = 10 * time.Minute
)

func retryDelay(attempts int) time.Duration {
	d := retryFloor
	for i := 1; i < attempts && d < retryCeiling; i++ {
		d *= 2
	}
	return min(d, retryCeiling)
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
	// Runtimes limits the node to plugins of these runtimes (a standalone
	// plugin host loads host plugins only); empty means all.
	Runtimes []manifest.RuntimeType
	// Accept further limits the node by the active version's manifest (a
	// plugin host that runs python plugins only); nil accepts all.
	Accept func(*manifest.Manifest) bool
	// Admit is the last word on a package before it loads, such as the
	// platform's minimum trust level; nil admits all. A refused plugin
	// fails with the reason in its status.
	Admit func(*pkg.Package) error
	// Role names what the node is in status reports, e.g. "plugin-host".
	Role string
}

// New creates a Reconciler.
func New(o Options) *Reconciler {
	if o.Interval <= 0 {
		o.Interval = DefaultInterval
	}
	if o.CacheDir == "" {
		o.CacheDir = DefaultCacheDir()
	}
	var runtimes map[string]bool
	if len(o.Runtimes) > 0 {
		runtimes = map[string]bool{}
		for _, rt := range o.Runtimes {
			runtimes[string(rt)] = true
		}
	}
	return &Reconciler{
		repo: o.Repo, store: o.Store, registry: o.Registry, cacheDir: o.CacheDir, rdb: o.Redis,
		activators: o.Activators, instanceID: uuid.NewString(), interval: o.Interval,
		runtimes: runtimes, accept: o.Accept, admit: o.Admit, role: o.Role,
		loaded: map[string]*Loaded{}, digests: map[string]string{}, retries: map[string]retry{},
		status: map[string]Status{}, now: time.Now,
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
		if row.DesiredState != types.PluginStateEnabled || (r.runtimes != nil && !r.runtimes[row.Runtime]) ||
			!r.accepts(ctx, row) {
			continue
		}
		desired[row.ID] = true
		// Before loading, so a plugin limited to some tenants is never
		// everyone's, not even for a moment.
		r.registry.SetAudience(row.ID, row.AudienceTenants())
		if err := r.ensure(ctx, row); err != nil {
			errs = append(errs, fmt.Errorf("plugin %s: %w", row.ID, err))
			var kept *upgradeError
			if !errors.As(err, &kept) {
				r.setStatus(row.ID, Status{Version: row.ActiveVersion, State: StateFailed, Error: err.Error()})
			}
		}
	}
	for id := range r.loaded {
		if !desired[id] {
			r.unload(ctx, id)
		}
	}
	// A plugin that never loaded has a status but nothing loaded.
	r.statusMu.Lock()
	for id := range r.status {
		if !desired[id] {
			delete(r.status, id)
			delete(r.retries, id)
			r.forgetStatus(ctx, id)
		}
	}
	r.statusMu.Unlock()
	r.publishStatuses(ctx)
	return errors.Join(errs...)
}

// accepts applies Options.Accept to a plugin's active version. A version
// that cannot be read is accepted, so ensure reports why.
func (r *Reconciler) accepts(ctx context.Context, row types.InstalledPlugin) bool {
	if r.accept == nil {
		return true
	}
	v, err := r.repo.GetVersion(ctx, row.ID, row.ActiveVersion)
	if err != nil || v == nil {
		return true
	}
	var m manifest.Manifest
	if json.Unmarshal(v.Manifest, &m) != nil {
		return true
	}
	return r.accept(&m)
}

// runtimeTarget identifies where a plugin runs beyond its package: a new
// remote URL or secret reloads the plugin like a new version would.
func runtimeTarget(row types.InstalledPlugin) string {
	if row.RemoteURL == "" && row.RemoteSecret == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(row.RemoteURL + "\x00" + row.RemoteSecret))
	return hex.EncodeToString(sum[:8])
}

// upgradeError is a new version that failed to load while the previous
// one keeps running.
type upgradeError struct {
	version string
	err     error
}

func (e *upgradeError) Error() string {
	return fmt.Sprintf("version %s failed to load; the previous version keeps running: %v", e.version, e.err)
}

func (e *upgradeError) Unwrap() error { return e.err }

// ensure loads the active version of one plugin unless it already is.
func (r *Reconciler) ensure(ctx context.Context, row types.InstalledPlugin) error {
	v, err := r.repo.GetVersion(ctx, row.ID, row.ActiveVersion)
	if err != nil {
		return err
	}
	if v == nil {
		return fmt.Errorf("version %s is not stored", row.ActiveVersion)
	}
	loadKey := v.Digest + "|" + runtimeTarget(row)
	prev := r.loaded[row.ID]
	if r.digests[row.ID] == loadKey {
		if prev == nil || dirExists(prev.Dir) {
			// Back on the loaded version (an upgrade rolled back): the
			// upgrade that failed is no longer wanted.
			delete(r.retries, row.ID)
			r.clearUpgrade(row.ID)
			return nil
		}
		// The extracted files are gone (the OS purged a temp directory):
		// extract and load again.
		logger.Warnf(ctx, "[plugin] %s: extracted files at %s are gone; reloading", row.ID, prev.Dir)
	}
	if rt, ok := r.retries[row.ID]; ok && rt.key == loadKey && r.now().Before(rt.next) {
		return nil // still failed; its status says why
	}
	data, err := r.store.Get(ctx, v.PackageURI)
	if err != nil {
		return r.failed(prev, row, loadKey, err)
	}
	p, err := pkg.Open(data)
	if err != nil {
		return r.failed(prev, row, loadKey, err)
	}
	if p.Digest != v.Digest {
		return r.failed(prev, row, loadKey,
			fmt.Errorf("stored package digest %s does not match %s", p.Digest, v.Digest))
	}
	if p.Manifest.ID != row.ID {
		return r.failed(prev, row, loadKey, fmt.Errorf("package is plugin %s, not %s", p.Manifest.ID, row.ID))
	}
	if r.admit != nil {
		if err := r.admit(p); err != nil {
			// The platform's policy refuses the plugin, not only this
			// version: the previous one stops too.
			if prev != nil {
				r.unload(ctx, row.ID)
			}
			// The verdict holds until the version or the platform's
			// settings change; do not fetch the package every pass.
			r.retries[row.ID] = retry{key: loadKey, attempts: 1, next: r.now().Add(retryCeiling)}
			return err
		}
	}
	dir, err := r.extract(p)
	if err != nil {
		return r.failed(prev, row, loadKey, err)
	}
	l := &Loaded{Manifest: p.Manifest, Package: p, Dir: dir, Installed: row}
	l.Report = func(healthy bool, err error) { r.reportLoaded(l, healthy, err) }
	pending, err := r.activate(ctx, prev, l)
	if err != nil {
		return r.failed(prev, row, loadKey, err)
	}
	r.loaded[row.ID] = l
	r.digests[row.ID] = loadKey
	delete(r.retries, row.ID)
	if err := errors.Join(pending...); err != nil {
		logger.Infof(ctx, "[plugin] loaded %s %s; pending: %v", row.ID, row.ActiveVersion, err)
		r.setStatus(row.ID, Status{
			Version: row.ActiveVersion, State: StateDegraded, Error: err.Error(), Egress: r.egress(row.ID),
		})
		return nil
	}
	logger.Infof(ctx, "[plugin] loaded %s %s", row.ID, row.ActiveVersion)
	r.setStatus(row.ID, Status{Version: row.ActiveVersion, State: StateReady, Egress: r.egress(row.ID)})
	return nil
}

// activate stages next on every activator and commits them all, or aborts
// them all and leaves prev as it was. Activators stage in reverse order:
// the domains only check what a package contributes, so a bad package
// fails before a runtime starts anything. They commit in order, runtimes
// first, so a code plugin is reachable before anything routes calls to it,
// and retire what they no longer run only after every commit, so a plugin
// that moved runtimes is served by the new one before the old one stops.
func (r *Reconciler) activate(ctx context.Context, prev, next *Loaded) (pending []error, err error) {
	staged := make([]Staged, 0, len(r.activators))
	abort := func() {
		for i := len(staged) - 1; i >= 0; i-- {
			staged[i].Abort()
		}
	}
	for i := len(r.activators) - 1; i >= 0; i-- {
		a := r.activators[i]
		s, err := a.Stage(ctx, prev, next)
		var pe *PendingError
		switch {
		case err == nil:
		case errors.As(err, &pe) && s != nil:
			pending = append(pending, fmt.Errorf("%s: %w", a.Name(), err))
		default:
			abort()
			return nil, fmt.Errorf("%s: %w", a.Name(), err)
		}
		if s == nil {
			s = Unchanged
		}
		staged = append(staged, s)
	}
	// The registry swaps in one step, so it goes first: if it refuses the
	// manifest, nothing has changed yet.
	if err := r.registry.Replace(next.Manifest); err != nil {
		abort()
		return nil, err
	}
	for i := len(staged) - 1; i >= 0; i-- {
		staged[i].Commit()
	}
	for i := len(staged) - 1; i >= 0; i-- {
		if s, ok := staged[i].(Retiring); ok {
			s.Retire()
		}
	}
	return pending, nil
}

// failed records a version that did not load and when to try it again. A
// plugin with a previous version loaded keeps it and reports the upgrade
// as failed; the error says so.
func (r *Reconciler) failed(prev *Loaded, row types.InstalledPlugin, loadKey string, err error) error {
	rt := r.retries[row.ID]
	if rt.key != loadKey {
		rt = retry{key: loadKey}
	}
	rt.attempts++
	rt.next = r.now().Add(retryDelay(rt.attempts))
	r.retries[row.ID] = rt
	if prev == nil {
		return err
	}
	r.statusMu.Lock()
	s := r.status[row.ID]
	s.UpgradeVersion, s.UpgradeError = row.ActiveVersion, err.Error()
	s.UpdatedAt = time.Now()
	r.status[row.ID] = s
	r.statusMu.Unlock()
	return &upgradeError{version: row.ActiveVersion, err: err}
}

// clearUpgrade forgets a failed upgrade of a plugin.
func (r *Reconciler) clearUpgrade(pluginID string) {
	r.statusMu.Lock()
	defer r.statusMu.Unlock()
	s, ok := r.status[pluginID]
	if !ok || s.UpgradeVersion == "" {
		return
	}
	s.UpgradeVersion, s.UpgradeError = "", ""
	s.UpdatedAt = time.Now()
	r.status[pluginID] = s
}

// egress asks the activators how this node controls a plugin's outbound
// traffic.
func (r *Reconciler) egress(pluginID string) driver.EgressMode {
	for _, a := range r.activators {
		if e, ok := a.(EgressReporter); ok {
			if mode := e.Egress(pluginID); mode != "" {
				return mode
			}
		}
	}
	return ""
}

// reportLoaded is Loaded.Report: runtime health for the version still
// loaded. It waits for a pass in progress, so the version it reports on has
// its status by then.
func (r *Reconciler) reportLoaded(l *Loaded, healthy bool, err error) {
	r.mu.Lock()
	current := r.loaded[l.Manifest.ID] == l && r.digests[l.Manifest.ID] != ""
	r.mu.Unlock()
	if current {
		r.ReportRuntime(l.Manifest.ID, healthy, err)
	}
}

func dirExists(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
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
	delete(r.retries, id)
	r.statusMu.Lock()
	delete(r.status, id)
	r.statusMu.Unlock()
	r.forgetStatus(ctx, id)
	logger.Infof(ctx, "[plugin] unloaded %s", id)
}

// extract writes the package under cacheDir/<digest>, once per digest. An
// existing extraction is reused, so cacheDir must be private: in a shared
// temp directory another user could plant a digest's files first.
func (r *Reconciler) extract(p *pkg.Package) (string, error) {
	if err := os.MkdirAll(r.cacheDir, 0o700); err != nil {
		return "", err
	}
	if err := ensurePrivateDir(r.cacheDir); err != nil {
		return "", fmt.Errorf("plugin cache %s: %w (set WEKNORA_PLUGIN_CACHE_DIR)", r.cacheDir, err)
	}
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
