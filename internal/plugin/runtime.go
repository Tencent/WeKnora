package plugin

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/pkg/pluginapi"
	pluginproto "github.com/Tencent/WeKnora/pkg/pluginapi/proto"
	"google.golang.org/grpc"
)

// Runtime is the small common lifecycle surface shared by built-in adapters
// and external process/container runtimes. The manager owns ordering and state;
// the runtime owns the implementation-specific start/stop mechanics.
type Runtime interface {
	Start(context.Context) error
	Stop(context.Context) error
	Health(context.Context) HealthStatus
}

type CapabilityReporter interface {
	Capabilities() []string
}

// connProvider is implemented by runtimes that expose a raw gRPC connection.
// Protocol adapters construct their own type-specific clients from it, keeping
// the runtime free of extension-type knowledge.
type connProvider interface {
	Conn() *grpc.ClientConn
}

// validateHandshake validates a control-plane handshake response against the
// manifest without branching on the extension type. The shared PluginControl
// handshake reports plugin identity, protocol version, capabilities and
// extension type; the runtime only compares these against the manifest.
func validateHandshake(manifest Manifest, handshakeWire *pluginproto.HandshakeResponse) (pluginapi.HandshakeResponse, error) {
	var handshake pluginapi.HandshakeResponse
	if err := pluginapi.DecodeHandshake(handshakeWire, &handshake); err != nil {
		return handshake, err
	}
	if handshake.PluginID != "" && handshake.PluginID != manifest.ID {
		return handshake, fmt.Errorf("plugin handshake ID %q does not match manifest %q", handshake.PluginID, manifest.ID)
	}
	if handshake.ProtocolVersion != "" && handshake.ProtocolVersion != manifest.ProtocolVersion {
		return handshake, fmt.Errorf("plugin handshake protocol %q does not match manifest %q", handshake.ProtocolVersion, manifest.ProtocolVersion)
	}
	if handshake.ExtensionType != "" && handshake.ExtensionType != string(manifest.ExtensionType) {
		return handshake, fmt.Errorf("plugin handshake extension type %q does not match manifest %q", handshake.ExtensionType, manifest.ExtensionType)
	}
	if err := validateRuntimeCapabilities(manifest, handshake.Capabilities); err != nil {
		return handshake, fmt.Errorf("plugin capabilities: %w", err)
	}
	return handshake, nil
}

func validateRuntimeCapabilities(manifest Manifest, actual []string) error {
	declared := make(map[string]struct{}, len(manifest.Capabilities))
	for _, capability := range manifest.Capabilities {
		declared[capability] = struct{}{}
	}
	seen := make(map[string]struct{}, len(actual))
	for _, capability := range actual {
		if _, ok := declared[capability]; !ok {
			return fmt.Errorf("runtime capability %q is not declared", capability)
		}
		seen[capability] = struct{}{}
	}
	for _, capability := range manifest.Capabilities {
		if _, ok := seen[capability]; !ok {
			return fmt.Errorf("manifest capability %q was not confirmed by runtime", capability)
		}
	}
	return nil
}

type BuiltinRuntime struct {
	StartFunc        func(context.Context) error
	StopFunc         func(context.Context) error
	HealthFunc       func(context.Context) HealthStatus
	CapabilitiesList []string
}

func (r *BuiltinRuntime) Start(ctx context.Context) error {
	if r.StartFunc != nil {
		return r.StartFunc(ctx)
	}
	return nil
}
func (r *BuiltinRuntime) Stop(ctx context.Context) error {
	if r.StopFunc != nil {
		return r.StopFunc(ctx)
	}
	return nil
}
func (r *BuiltinRuntime) Health(ctx context.Context) HealthStatus {
	if r.HealthFunc != nil {
		return r.HealthFunc(ctx)
	}
	return HealthStatus{State: StateRunning, CheckedAt: time.Now().UTC()}
}
func (r *BuiltinRuntime) Capabilities() []string { return append([]string(nil), r.CapabilitiesList...) }

type pluginEntry struct {
	manifest       Manifest
	runtime        Runtime
	state          HealthStatus
	generation     uint64
	started        bool
	healthFailures int
	restartCount   int
	admission      *admissionController
	lifecycle      sync.Mutex
	loadedTrust    PluginTrustLevel
}

type Manager struct {
	mu               sync.RWMutex
	hostVersion      string
	entries          map[string]*pluginEntry
	supervisorMu     sync.Mutex
	supervisorCancel context.CancelFunc
	supervisorWG     sync.WaitGroup
	admissionConfig  AdmissionConfig
	trustConfig      PluginTrustConfig
	supervisionAudit AuditSink
	// modelConfigRepusher re-delivers saved model configurations to a model
	// plugin after it (re)starts. See ModelConfigRepusher.
	modelConfigRepusher ModelConfigRepusher
}

// ModelConfigRepusher re-delivers persisted per-model configuration (api_key,
// base_url, extra_config) to a model plugin after its runtime (re)starts.
//
// Model plugins cache configuration in-process after the one-shot
// ValidateConfig delivered at model-save time; a restart empties that cache.
// The host owns the authoritative copy in its DB, so a repusher implementation
// re-pushes it on every (re)start path. It is registered at wiring time and
// only consulted for ExtensionModel plugins.
type ModelConfigRepusher interface {
	RepushModelConfigs(ctx context.Context, providerName string) error
}

// HealthSupervisorConfig controls active health monitoring. Monitoring is
// opt-in so existing embedders retain the previous on-demand behavior.
type HealthSupervisorConfig struct {
	Interval         time.Duration
	FailureThreshold int
	RestartEnabled   bool
	MaxRestartCount  int
}

const (
	defaultHealthInterval    = 30 * time.Second
	defaultFailureThreshold  = 3
	defaultCancellationGrace = 5 * time.Second
	defaultStopGrace         = 15 * time.Second
	defaultStartGrace        = 30 * time.Second
)

func NewManager(hostVersion string) *Manager {
	return &Manager{hostVersion: hostVersion, entries: make(map[string]*pluginEntry), admissionConfig: defaultAdmissionConfig(), trustConfig: LoadPluginTrustConfig(), supervisionAudit: LoggerAuditSink{}}
}

func NewManagerWithAdmission(hostVersion string, config AdmissionConfig) *Manager {
	return &Manager{hostVersion: hostVersion, entries: make(map[string]*pluginEntry), admissionConfig: config, trustConfig: LoadPluginTrustConfig(), supervisionAudit: LoggerAuditSink{}}
}

// SetSupervisionAuditSink injects the sink that receives supervision events
// (health degradation, supervisor-triggered restarts and their outcomes). The
// default writes to the host log stream; deployments that persist audit rows
// should call this once at startup before starting the health supervisor.
func (m *Manager) SetSupervisionAuditSink(sink AuditSink) {
	if sink == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.supervisionAudit = sink
}

// recordSupervision emits one supervision audit event. Failures to record are
// deliberately swallowed: supervision must never panic or block the lifecycle
// path because auditing is unavailable.
func (m *Manager) recordSupervision(pluginID, action string, allowed bool, reason, policy string) {
	m.mu.RLock()
	sink := m.supervisionAudit
	m.mu.RUnlock()
	if sink == nil {
		return
	}
	defer func() { _ = recover() }()
	sink.Record(AuditEvent{
		PluginID: pluginID,
		Action:   action,
		Allowed:  allowed,
		Reason:   reason,
		Policy:   policy,
		At:       time.Now().UTC(),
	})
}

// SetPluginTrustConfig replaces the deployment-scoped trust map used by future loads and rescans.
// A nil or missing plugin entry is intentionally treated as offline.
func (m *Manager) SetPluginTrustConfig(config PluginTrustConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trustConfig = config.Clone()
}

// SetModelConfigRepusher installs the single re-push sink for model plugin
// configuration. It must be called before any model plugin starts so boot-time
// loads are covered; later restarts also consult it.
func (m *Manager) SetModelConfigRepusher(repusher ModelConfigRepusher) {
	if repusher == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.modelConfigRepusher = repusher
}

// fireModelConfigRepush re-delivers saved model configs after a model plugin
// reaches Running. It is invoked from every (re)start path. The call runs
// asynchronously and outside all manager locks: a repusher does DB queries plus
// a gRPC ValidateConfig round-trip and must never back-pressure the lifecycle.
func (m *Manager) fireModelConfigRepush(manifest Manifest) {
	if manifest.ExtensionType != ExtensionModel {
		return
	}
	m.mu.RLock()
	repusher := m.modelConfigRepusher
	m.mu.RUnlock()
	if repusher == nil {
		return
	}
	providerName := ModelProviderName(manifest)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := repusher.RepushModelConfigs(ctx, providerName); err != nil {
			logger.Warnf(ctx, "[Plugin] repush model configs for provider %q failed: %v", providerName, err)
		}
	}()
}

func (m *Manager) PluginTrustLevel(id string) PluginTrustLevel {
	m.mu.RLock()
	config := m.trustConfig
	m.mu.RUnlock()
	return config.Level(id)
}

// GetPluginTrustConfig returns a copy of the deployment-scoped trust map.
func (m *Manager) GetPluginTrustConfig() PluginTrustConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.trustConfig.Clone()
}

// LoadedTrustLevel returns the trust level the plugin was loaded with. Rescan
// uses it to detect a trust change that requires a reload (a trust change does
// not alter the manifest, so manifest-equality alone would skip it).
func (m *Manager) LoadedTrustLevel(id string) PluginTrustLevel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.entries[id]
	if !ok {
		return TrustOffline
	}
	return entry.loadedTrust
}

// SetPluginTrustLevel updates a single plugin's trust level in place. It is used
// by the plugin management API so an admin can change one plugin without a full
// config swap.
func (m *Manager) SetPluginTrustLevel(id string, level PluginTrustLevel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg := m.trustConfig.Clone()
	cfg[id] = level
	m.trustConfig = cfg
}

type InvocationLease struct {
	Context    context.Context
	Generation uint64
	release    func()
	once       sync.Once
}

func (l *InvocationLease) Close() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		if l.release != nil {
			l.release()
		}
	})
}

// AcquireInvocation admits one business call and pins the current runtime
// generation until release. Restart/stop enters draining and waits for these
// leases before terminating the shared process.
func (m *Manager) AcquireInvocation(ctx context.Context, id string) (*InvocationLease, error) {
	m.mu.RLock()
	entry, ok := m.entries[id]
	if !ok {
		m.mu.RUnlock()
		return nil, fmt.Errorf("plugin %q not registered", id)
	}
	if !entry.started {
		m.mu.RUnlock()
		return nil, fmt.Errorf("plugin %q is not running", id)
	}
	generation, admission := entry.generation, entry.admission
	m.mu.RUnlock()
	callCtx, release, err := admission.acquire(ctx)
	if err != nil {
		return nil, err
	}
	return &InvocationLease{Context: callCtx, Generation: generation, release: release}, nil
}

func (m *Manager) GenerationValid(id string, generation uint64) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.entries[id]
	return ok && entry.started && entry.generation == generation
}

func (m *Manager) Admission(id string) (AdmissionStatus, bool) {
	m.mu.RLock()
	entry, ok := m.entries[id]
	if !ok {
		m.mu.RUnlock()
		return AdmissionStatus{}, false
	}
	admission := entry.admission
	m.mu.RUnlock()
	return admission.status(), true
}

// StartHealthSupervisor periodically probes every started plugin. It updates
// the same state returned by Health(), marks repeatedly failing plugins
// unhealthy, and optionally performs bounded restarts with a fresh lifecycle
// generation. Calling it twice is idempotent.
func (m *Manager) StartHealthSupervisor(parent context.Context, cfg HealthSupervisorConfig) error {
	if parent == nil {
		return fmt.Errorf("health supervisor context is nil")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultHealthInterval
	}
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = defaultFailureThreshold
	}
	if cfg.RestartEnabled && cfg.MaxRestartCount <= 0 {
		return fmt.Errorf("max restart count must be positive when restart is enabled")
	}
	m.supervisorMu.Lock()
	defer m.supervisorMu.Unlock()
	if m.supervisorCancel != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	m.supervisorCancel = cancel
	m.supervisorWG.Add(1)
	go m.healthSupervisorLoop(ctx, cfg)
	return nil
}

func (m *Manager) StopHealthSupervisor() {
	m.supervisorMu.Lock()
	cancel := m.supervisorCancel
	m.supervisorCancel = nil
	m.supervisorMu.Unlock()
	if cancel != nil {
		cancel()
		m.supervisorWG.Wait()
	}
}

func (m *Manager) healthSupervisorLoop(ctx context.Context, cfg HealthSupervisorConfig) {
	defer m.supervisorWG.Done()
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, info := range m.List() {
				m.superviseOne(ctx, info.Manifest.ID, cfg)
			}
		}
	}
}

func (m *Manager) superviseOne(ctx context.Context, id string, cfg HealthSupervisorConfig) {
	m.mu.RLock()
	entry, ok := m.entries[id]
	if !ok || !entry.started {
		m.mu.RUnlock()
		return
	}
	runtime := entry.runtime
	generation := entry.generation
	m.mu.RUnlock()

	health := runtime.Health(ctx)
	if health.State == "" {
		health.State = StateRunning
	}
	health.Generation = generation
	health.CheckedAt = time.Now().UTC()

	m.mu.Lock()
	entry, ok = m.entries[id]
	if !ok {
		m.mu.Unlock()
		return
	}
	if !entry.started || entry.generation != generation {
		m.mu.Unlock()
		return
	}
	if entry.admission.status().Draining {
		m.mu.Unlock()
		return
	}
	if health.State == StateRunning {
		entry.healthFailures = 0
		entry.restartCount = 0
	} else {
		entry.healthFailures++
		if entry.healthFailures >= cfg.FailureThreshold && health.State != StateFailed {
			health.State = StateUnhealthy
		}
	}
	entry.state = health
	shouldRestart := cfg.RestartEnabled && entry.healthFailures >= cfg.FailureThreshold && entry.restartCount < cfg.MaxRestartCount
	if shouldRestart {
		entry.restartCount++
	}
	failures, attempt, trust := entry.healthFailures, entry.restartCount, string(entry.loadedTrust)
	m.mu.Unlock()

	// Supervision audit: every degradation decision and restart trigger is
	// recorded so the supervision trail is retrievable from the audit stream.
	if health.State == StateUnhealthy && !shouldRestart {
		m.recordSupervision(id, "supervision.unhealthy", false,
			fmt.Sprintf("health %q persisted for %d checks (threshold %d); no restart scheduled",
				health.State, failures, cfg.FailureThreshold), trust)
	}
	if shouldRestart {
		m.recordSupervision(id, "supervision.restart.triggered", true,
			fmt.Sprintf("health %q persisted for %d checks (threshold %d); restart attempt %d of %d",
				health.State, failures, cfg.FailureThreshold, attempt, cfg.MaxRestartCount), trust)
	}

	if shouldRestart {
		m.restartOne(ctx, id, runtime, generation)
	}
}

func (m *Manager) restartOne(ctx context.Context, id string, runtime Runtime, expectedGeneration uint64) {
	m.mu.RLock()
	entry, ok := m.entries[id]
	if !ok {
		m.mu.RUnlock()
		return
	}
	admission := entry.admission
	drainTimeout := admission.config.DrainTimeout
	trust := string(entry.loadedTrust)
	m.mu.RUnlock()
	entry.lifecycle.Lock()
	defer entry.lifecycle.Unlock()
	m.mu.RLock()
	current, ok := m.entries[id]
	if !ok || current != entry || !entry.started || entry.generation != expectedGeneration {
		m.mu.RUnlock()
		return
	}
	m.mu.RUnlock()
	m.mu.Lock()
	entry.state = HealthStatus{State: StateDraining, CheckedAt: time.Now().UTC(), Generation: entry.generation}
	m.mu.Unlock()
	drainAdmission(ctx, admission, drainTimeout)
	stopCtx, cancelStop := context.WithTimeout(ctx, defaultStopGrace)
	stopErr := runtime.Stop(stopCtx)
	cancelStop()
	if stopErr != nil {
		m.mu.Lock()
		if entry, ok := m.entries[id]; ok {
			// runtime.Stop already tore the gRPC connection down even when it
			// reports an error; leaving started=true would let AcquireInvocation
			// admit calls against a nil conn (nil-pointer panic downstream).
			entry.started = false
			entry.state = HealthStatus{State: StateFailed, Message: "restart stop failed: " + stopErr.Error(), CheckedAt: time.Now().UTC(), Generation: entry.generation}
		}
		m.mu.Unlock()
		m.recordSupervision(id, "supervision.restart.failed", false,
			"restart aborted: stop failed: "+stopErr.Error(), trust)
		return
	}
	postStopCtx, cancelPostStop := context.WithTimeout(ctx, defaultCancellationGrace)
	postStopErr := admission.waitDrained(postStopCtx)
	cancelPostStop()
	if postStopErr != nil {
		m.mu.Lock()
		entry.started = false
		entry.state = HealthStatus{State: StateFailed, Message: "old plugin calls did not terminate after stop: " + postStopErr.Error(), CheckedAt: time.Now().UTC(), Generation: entry.generation}
		m.mu.Unlock()
		m.recordSupervision(id, "supervision.restart.failed", false,
			"restart aborted: old plugin calls did not terminate after stop: "+postStopErr.Error(), trust)
		return
	}
	startCtx, cancelStart := context.WithTimeout(ctx, defaultStartGrace)
	startErr := runtime.Start(startCtx)
	cancelStart()
	if startErr != nil {
		m.mu.Lock()
		if entry, ok := m.entries[id]; ok {
			entry.started = false
			entry.state = HealthStatus{State: StateFailed, Message: "restart start failed: " + startErr.Error(), CheckedAt: time.Now().UTC(), Generation: entry.generation}
		}
		m.mu.Unlock()
		m.recordSupervision(id, "supervision.restart.failed", false,
			"restart aborted: start failed: "+startErr.Error(), trust)
		return
	}
	health := runtime.Health(ctx)
	if health.State == "" {
		health.State = StateRunning
	}
	newGeneration := uint64(0)
	var manifest Manifest
	m.mu.Lock()
	if entry, ok := m.entries[id]; ok {
		entry.started = true
		entry.generation++
		entry.healthFailures = 0
		health.Generation = entry.generation
		health.CheckedAt = time.Now().UTC()
		entry.state = health
		newGeneration = entry.generation
		trust = string(entry.loadedTrust)
		manifest = entry.manifest
	}
	m.mu.Unlock()
	admission.resume()
	m.recordSupervision(id, "supervision.restart.completed", true,
		fmt.Sprintf("plugin restarted by the health supervisor; generation bumped to %d", newGeneration), trust)
	m.fireModelConfigRepush(manifest)
}

func (m *Manager) Register(manifest Manifest, runtime Runtime) error {
	if runtime == nil {
		return fmt.Errorf("%w: runtime is nil", ErrManifestInvalid)
	}
	if err := manifest.Validate(m.hostVersion); err != nil {
		return err
	}
	if reporter, ok := runtime.(CapabilityReporter); ok {
		declared := make(map[string]struct{}, len(manifest.Capabilities))
		for _, capability := range manifest.Capabilities {
			declared[capability] = struct{}{}
		}
		for _, capability := range reporter.Capabilities() {
			if _, ok := declared[capability]; !ok {
				return fmt.Errorf("%w: runtime capability %q is not declared", ErrManifestInvalid, capability)
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.entries[manifest.ID]; exists {
		return fmt.Errorf("plugin %q already registered", manifest.ID)
	}
	m.entries[manifest.ID] = &pluginEntry{
		manifest:    manifest,
		runtime:     runtime,
		state:       HealthStatus{State: StateDiscovered},
		admission:   newAdmissionController(m.admissionConfig),
		loadedTrust: m.trustConfig.Level(manifest.ID),
	}
	return nil
}

func (m *Manager) Start(ctx context.Context, id string) error {
	m.mu.RLock()
	entry, ok := m.entries[id]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("plugin %q not registered", id)
	}
	m.mu.RUnlock()
	entry.lifecycle.Lock()
	defer entry.lifecycle.Unlock()
	m.mu.Lock()
	if entry.started {
		status := entry.admission.status()
		if status.Draining {
			m.mu.Unlock()
			return fmt.Errorf("plugin %q cannot start while previous runtime is draining", id)
		}
		m.mu.Unlock()
		return nil
	}
	if status := entry.admission.status(); status.Active > 0 {
		m.mu.Unlock()
		return fmt.Errorf("plugin %q cannot start while %d old invocation(s) remain", id, status.Active)
	}
	entry.state.State = StateStarting
	entry.state.CheckedAt = time.Now().UTC()
	runtime := entry.runtime
	m.mu.Unlock()
	if err := runtime.Start(ctx); err != nil {
		m.mu.Lock()
		entry.state = HealthStatus{State: StateFailed, Message: err.Error(), CheckedAt: time.Now().UTC(), Generation: entry.generation}
		m.mu.Unlock()
		return fmt.Errorf("start plugin %q: %w", id, err)
	}
	health := runtime.Health(ctx)
	if health.State == "" {
		health.State = StateRunning
	}
	m.mu.Lock()
	entry.started = true
	entry.generation++
	health.Generation = entry.generation
	health.CheckedAt = time.Now().UTC()
	entry.state = health
	manifest := entry.manifest
	m.mu.Unlock()
	entry.admission.resume()
	m.fireModelConfigRepush(manifest)
	return nil
}

func (m *Manager) Stop(ctx context.Context, id string) error {
	m.mu.RLock()
	entry, ok := m.entries[id]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("plugin %q not registered", id)
	}
	m.mu.RUnlock()
	entry.lifecycle.Lock()
	defer entry.lifecycle.Unlock()
	m.mu.Lock()
	if !entry.started {
		entry.state.State = StateStopped
		m.mu.Unlock()
		return nil
	}
	runtime := entry.runtime
	admission := entry.admission
	entry.state = HealthStatus{State: StateDraining, CheckedAt: time.Now().UTC(), Generation: entry.generation}
	m.mu.Unlock()
	drainAdmission(ctx, admission, admission.config.DrainTimeout)
	stopCtx, cancelStop := context.WithTimeout(ctx, defaultStopGrace)
	stopErr := runtime.Stop(stopCtx)
	cancelStop()
	if stopErr != nil {
		m.mu.Lock()
		entry.state = HealthStatus{State: StateFailed, Message: stopErr.Error(), CheckedAt: time.Now().UTC(), Generation: entry.generation}
		m.mu.Unlock()
		return fmt.Errorf("stop plugin %q: %w", id, stopErr)
	}
	postStopCtx, cancelPostStop := context.WithTimeout(context.WithoutCancel(ctx), defaultCancellationGrace)
	postStopErr := admission.waitDrained(postStopCtx)
	cancelPostStop()
	if postStopErr != nil {
		m.mu.Lock()
		entry.started = false
		entry.state = HealthStatus{State: StateFailed, Message: "old plugin calls did not terminate after stop: " + postStopErr.Error(), CheckedAt: time.Now().UTC(), Generation: entry.generation}
		m.mu.Unlock()
		return fmt.Errorf("stop plugin %q: %w", id, postStopErr)
	}
	m.mu.Lock()
	entry.started = false
	entry.state = HealthStatus{State: StateStopped, CheckedAt: time.Now().UTC(), Generation: entry.generation}
	m.mu.Unlock()
	return nil
}

func drainAdmission(ctx context.Context, admission *admissionController, timeout time.Duration) {
	admission.beginDrain()
	drainCtx, cancelDrain := context.WithTimeout(ctx, timeout)
	err := admission.waitDrained(drainCtx)
	cancelDrain()
	if err != nil {
		admission.cancelActive(ErrPluginDrainTimeout)
	}
}

// HealthSnapshot returns the last recorded health state without actively
// probing the plugin. The snapshot is written by Start (post-handshake) and
// refreshed by the health supervisor; availability checks that must not pay a
// probe round-trip (e.g. engine lists) should read this instead of Health.
func (m *Manager) HealthSnapshot(id string) (HealthStatus, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.entries[id]
	if !ok {
		return HealthStatus{}, false
	}
	return entry.state, true
}

func (m *Manager) Health(ctx context.Context, id string) (HealthStatus, error) {
	m.mu.RLock()
	entry, ok := m.entries[id]
	if !ok {
		m.mu.RUnlock()
		return HealthStatus{}, fmt.Errorf("plugin %q not registered", id)
	}
	runtime := entry.runtime
	started := entry.started
	generation := entry.generation
	current := entry.state
	m.mu.RUnlock()
	if !started {
		return current, nil
	}
	health := runtime.Health(ctx)
	if health.State == "" {
		health.State = StateRunning
	}
	health.Generation = generation
	health.CheckedAt = time.Now().UTC()
	m.mu.Lock()
	entry.state = health
	m.mu.Unlock()
	return health, nil
}

func (m *Manager) Get(id string) (PluginInfo, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.entries[id]
	if !ok {
		return PluginInfo{}, false
	}
	return PluginInfo{Manifest: entry.manifest, State: entry.state}, true
}

// Unregister removes a stopped or failed plugin from the control plane. It is
// used when a multi-plugin discovery pass fails part way through, so a later
// retry does not inherit stale registrations.
func (m *Manager) Unregister(ctx context.Context, id string) error {
	if err := m.Stop(ctx, id); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.entries, id)
	m.mu.Unlock()
	return nil
}

// RegisterFailed records a discovered plugin whose runtime plan could not be
// resolved (e.g. an OCI manifest set to "trusted", or a process plugin set to
// "isolated"). Without this placeholder the plugin would have no card in the
// management UI and no way back: the trust level can only be changed from the
// card. Rescan retries failed placeholders once the configuration is fixed.
func (m *Manager) RegisterFailed(manifest Manifest, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[manifest.ID] = &pluginEntry{
		manifest: manifest,
		state:    HealthStatus{State: StateFailed, Message: reason, CheckedAt: time.Now().UTC()},
	}
}

// Restart stops and starts one plugin through the same bounded lifecycle the
// health supervisor uses. It is the manual recovery entry point for an
// unhealthy plugin (e.g. its container was removed externally) and also picks
// up a freshly pulled OCI image without touching the trust configuration.
func (m *Manager) Restart(ctx context.Context, id string) error {
	m.mu.RLock()
	entry, ok := m.entries[id]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("plugin %q is not loaded", id)
	}
	if !entry.started {
		m.mu.RUnlock()
		return fmt.Errorf("plugin %q is not running; use rescan to retry a failed load", id)
	}
	runtime, generation := entry.runtime, entry.generation
	m.mu.RUnlock()

	m.restartOne(ctx, id, runtime, generation)

	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok = m.entries[id]
	if !ok {
		return fmt.Errorf("plugin %q disappeared during restart", id)
	}
	if entry.state.State != StateRunning {
		return fmt.Errorf("restart plugin %q: %s", id, entry.state.Message)
	}
	return nil
}

func (m *Manager) List() []PluginInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]PluginInfo, 0, len(m.entries))
	for _, entry := range m.entries {
		result = append(result, PluginInfo{Manifest: entry.manifest, State: entry.state})
	}
	// Map iteration order is randomized in Go; sort so the plugin management
	// UI and any other consumer get a stable, deterministic listing.
	sort.Slice(result, func(i, j int) bool {
		ni, nj := result[i].Manifest.Name, result[j].Manifest.Name
		if ni != nj {
			return ni < nj
		}
		return result[i].Manifest.ID < result[j].Manifest.ID
	})
	return result
}
