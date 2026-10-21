// Package sandbox: session-bound Manager.
//
// SessionBoundManager keeps one persistent remote sandbox per tenant session.
// It delegates all provider-specific work to a RemoteSandboxClient adapter and
// treats the authoritative session→sandbox binding as external state that
// lives in SessionSandboxBindingStore (Redis in production, memory in tests
// and single-process deployments). This makes the manager provider-neutral
// (Cube , E2B) and multi-instance safe: two WeKnora processes
// concurrently servicing the same session never allocate duplicate sandboxes,
// and a restart never loses the session's remote resource.
//
// Semantics:
//   - An Execute call with a non-empty ExecuteConfig.SessionID resolves the
//     session's remote sandbox (creating it lazily) and runs the script on
//     the resolved handle. All resolution goes through the lifecycle
//     coordinator so create/recover/replace/delete are serialised by the
//     distributed lifecycle lock.
//   - An Execute call with an empty SessionID falls through to a stateless
//     RemoteSandbox, which allocates a fresh sandbox, runs the script, and
//     tears the sandbox down after Execute returns.
//   - Cube and E2B reap idle sandboxes themselves. Docker has no provider TTL,
//     so that backend runs its own idle sweep against activity-marker mtimes.
package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/common/redislock"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox/admission"
	"github.com/Tencent/WeKnora/internal/sandbox/allocation"
	"github.com/Tencent/WeKnora/internal/sandbox/workspace"
	"github.com/Tencent/WeKnora/internal/types"
)

// SessionInputRoot is reserved for durable user attachments restored from
// file storage. Generated artifacts must remain under SessionOutputRoot.
const SessionInputRoot = workspace.InputDir

// SessionOutputRoot is where skill scripts write artifacts for collection.
// The skills manager injects this path via skillOutputEnvVar; Execute
// materialises the directory via envd before the script runs so scripts
// do not depend on the template user being able to mkdir under /workspace.
const SessionOutputRoot = workspace.OutputDir

// skillOutputEnvVar matches the skills manager's WEKNORA_SKILL_OUTPUT_DIR.
const skillOutputEnvVar = "WEKNORA_SKILL_OUTPUT_DIR"

// sessionInputEnvVar matches the skills manager's WEKNORA_SESSION_INPUT_DIR.
// Both names are injected into the sandbox environment itself, not only into
// the skill-script Execute call, so an agent exploring with shell_exec reads
// the same paths the skills framework uses.
const sessionInputEnvVar = "WEKNORA_SESSION_INPUT_DIR"

// SessionWorkspaceRoot is the writable workspace root inside remote sandboxes.
// shell_exec work_dir must stay underneath this path.
const SessionWorkspaceRoot = workspace.Root

// SessionGitDir is the git metadata directory for per-turn workspace
// checkpoints. It lives on the sandbox root filesystem so a fork snapshot
// still copies the object store, but outside SessionWorkspaceRoot so
// `rm -rf /workspace` (or an agent cleaning the work tree) cannot drop
// checkpoint history that rewind and fork later reset to.
//
// Checkpoints used to live in SessionWorkspaceRoot/.git, and sandboxes
// provisioned before this constant existed still hold theirs there. The
// shared git preamble adopts that repository on first use so SHAs recorded
// before the move keep resolving; see gitWorkspaceAdoptLegacyRepo.
const SessionGitDir = workspace.GitDir

// sessionArtifactDirBootstrapTimeout bounds directory creation and access
// checks, performed with the execution identity.
const sessionArtifactDirBootstrapTimeout = 15 * time.Second

// sessionLifecycleCleanupTimeout bounds the lifecycle coordinator's own
// bookkeeping deletions (loser cleanup, orphan cleanup after session
// disappearance).
const sessionLifecycleCleanupTimeout = 30 * time.Second

// SessionBoundManager is a sandbox.Manager that binds one remote sandbox per
// tenant session. Concrete provider work is delegated to RemoteSandboxClient;
// this type owns validation and the mapping between application concepts
// (ExecuteConfig, session-scoped shell/file APIs) and the provider-neutral
// RemoteSandboxClient contract.
type SessionBoundManager struct {
	config    *Config
	validator *ScriptValidator

	client    RemoteSandboxClient
	bindings  SessionSandboxBindingStore
	checker   SessionExistenceChecker
	lifecycle *remoteSessionLifecycle
	ephemeral *RemoteSandbox

	// activeType is the effective sandbox type callers observe.
	activeType SandboxType

	configID    string
	allocations AllocationDirectory
	admission   admission.Registry
	namespace   string

	// addresses caches user-mode assignments for this manager's lifetime
	// (one request). Only the address is trusted from it; validity is read
	// fresh by admitUser.
	addresses sync.Map

	// admissionWait bounds waiting for a closed scope (rebuild, exclusive).
	admissionWait time.Duration

	// rewindWait bounds the exclusive window rewind takes on a shared machine.
	// It is shorter than admissionWait: an open terminal should fail rewind
	// quickly instead of holding the HTTP request for a rebuild-length wait.
	rewindWait time.Duration

	// mu guards Cleanup's idempotency flag.
	mu     sync.RWMutex
	closed bool
}

// AllocationDirectory resolves a session's recorded placement. Implemented by
// internal/application/service/sandboxalloc; nil keeps every session on the
// historical per-session path.
type AllocationDirectory interface {
	// SessionAllocation is the session's address. The manager caches it for
	// one request; it is never trusted for validity.
	SessionAllocation(ctx context.Context, sessionID string) (allocation.Assignment, bool, error)
	// VerifySessionAllocation reads the database on every call and returns
	// nil only if expected is still the session's live assignment and the
	// session exists, is not retired, and its current owner is expected's
	// owner and still eligible to share. It returns ErrSandboxSessionDeleted
	// or ErrUserAllocationRejected for those cases, other errors for
	// failures to verify.
	VerifySessionAllocation(
		ctx context.Context, sessionTenantID uint64, sessionID string, expected allocation.Assignment,
	) error
	UserMachinesByConfig(ctx context.Context, configTenantID uint64, configID string) ([]allocation.UserMachine, error)
}

// SessionBoundManagerConfig bundles the wired dependencies. Test helpers and
// the production container use it so callers only have to name the moving
// parts they actually override.
type SessionBoundManagerConfig struct {
	Config  *Config
	Client  RemoteSandboxClient
	Store   SessionSandboxBindingStore
	Checker SessionExistenceChecker

	// ConfigID identifies the tenant sandbox config this manager serves. It is
	// stamped onto sandbox metadata so cleanup can target one config without
	// touching another that shares the same provider account.
	ConfigID string

	// ConfigTenantID is the workspace that owns ConfigID. The resolver fills
	// it so provider creates register on that config's admission scope.
	ConfigTenantID uint64

	// SkipHealthProbe skips the construction-time Health() round-trip.
	// Set by the per-tenant resolver, which builds a manager per request.
	// See NewSessionBoundManager.
	SkipHealthProbe bool

	// Bootstrapper customises the first sandbox create of individual sessions
	// (session fork). Optional: nil is the ordinary path.
	Bootstrapper SessionBootstrapper

	// Allocations, Admission and Namespace enable shared user machines. All
	// three are optional; a user-mode assignment is refused without them.
	Allocations AllocationDirectory
	Admission   admission.Registry
	Namespace   string
}

// NewSessionBoundManager wires the manager with an explicit RemoteSandboxClient
// backend, binding store, and session existence checker. Every persistent
// operation flows through these three dependencies; the manager never keeps
// authoritative session→sandbox state locally.
//
// Provider identity comes from deps.Client.Provider() — not Config.Type —
// so test harnesses and custom wiring that inject a different client backend
// always project the correct template, TTL, and health timeout.
func NewSessionBoundManager(deps SessionBoundManagerConfig) (*SessionBoundManager, error) {
	cfg := deps.Config
	if cfg == nil {
		cfg = DefaultConfig()
	}
	if err := ValidateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid sandbox config: %w", err)
	}
	if deps.Client == nil {
		return nil, errors.New("session bound manager requires a RemoteSandboxClient")
	}
	if deps.Store == nil {
		return nil, errors.New("session bound manager requires a SessionSandboxBindingStore")
	}
	if deps.Checker == nil {
		return nil, errors.New("session bound manager requires a SessionExistenceChecker")
	}

	provider := deps.Client.Provider()
	if !isRemoteProvider(provider) {
		return nil, fmt.Errorf("sandbox: unsupported remote provider %q", provider)
	}

	// Apply the provider's tuning defaults so downstream code reads only
	// non-zero TTL / timeout fields. Endpoint defaults are deliberately not
	// applied here: this constructor also serves named configs, which must be
	// told what they are missing rather than handed a built-in localhost value.
	switch provider {
	case SandboxTypeCube:
		applyCubeRuntimeDefaults(cfg)
	case SandboxTypeE2B:
		applyE2BRuntimeDefaults(cfg)
	case SandboxTypeDocker:
		applyDockerRuntimeDefaults(cfg)
	}

	// Build the provider-specific neutral create request using the
	// provider's own template and TTL fields.
	createRequest, err := buildSessionCreateRequest(provider, cfg)
	if err != nil {
		return nil, fmt.Errorf("session bound manager: %w", err)
	}
	// An empty template for the selected provider means the deployment is
	// misconfigured. Fail early so operators get a clear message instead of
	// a remote API error at the first sandbox allocation.
	if strings.TrimSpace(createRequest.TemplateID) == "" {
		return nil, fmt.Errorf(
			"sandbox: %s template ID is required but not configured",
			provider,
		)
	}

	client := wrapLangfuseRemoteClient(deps.Client)

	bootstrapper := deps.Bootstrapper
	if withClient, ok := bootstrapper.(SessionBootstrapperWithClient); ok {
		bootstrapper = withClient.WithClient(client)
	}

	var lifecycleOpts []lifecycleOption
	if deps.Admission != nil {
		lifecycleOpts = append(lifecycleOpts, withUserMachines(deps.Namespace, deps.Admission))
		lifecycleOpts = append(lifecycleOpts, withConfigCreateGate(deps.Admission, deps.ConfigTenantID))
	}
	lifecycle, err := newRemoteSessionLifecycle(
		client,
		deps.Store,
		deps.Checker,
		createRequest,
		sessionLifecycleCleanupTimeout,
		deps.ConfigID,
		bootstrapper,
		lifecycleOpts...,
	)
	if err != nil {
		return nil, fmt.Errorf("session bound manager: %w", err)
	}

	m := &SessionBoundManager{
		config:        cfg,
		validator:     NewScriptValidator(),
		client:        client,
		bindings:      deps.Store,
		checker:       deps.Checker,
		lifecycle:     lifecycle,
		ephemeral:     NewRemoteSandbox(client, createRequest),
		activeType:    provider,
		configID:      deps.ConfigID,
		allocations:   deps.Allocations,
		admission:     deps.Admission,
		namespace:     deps.Namespace,
		admissionWait: 30 * time.Second,
		rewindWait:    rewindExclusiveWait,
	}

	// Per-tenant managers are rebuilt on every request, so probing here would
	// add a remote round-trip to each one. When a tenant explicitly configures
	// a backend, an unreachable provider must fail at first use rather than
	// substituting a different execution environment.
	if deps.SkipHealthProbe {
		return m, nil
	}

	// Health probe uses the provider's own HTTP timeout.
	probeCtx, cancel := context.WithTimeout(
		context.Background(),
		effectiveHTTPTimeout(provider, cfg),
	)
	defer cancel()
	if err := deps.Client.Health(probeCtx); err != nil {
		return nil, fmt.Errorf("remote sandbox provider unavailable: %w", err)
	}
	return m, nil
}

// GetType reports the current effective sandbox type.
func (m *SessionBoundManager) GetType() SandboxType {
	if m == nil {
		return SandboxTypeDisabled
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeType
}

// TerminalIdleDisconnect is how long an open PTY or desktop relay may sit
// idle before the WebSocket is closed. Missing or out-of-range workspace
// values are clamped onto the built-in default so a stored 0 still disconnects.
func (m *SessionBoundManager) TerminalIdleDisconnect() time.Duration {
	if m == nil || m.config == nil {
		return DefaultTerminalIdleDisconnect
	}
	return EffectiveTerminalIdleDisconnect(m.config.TerminalIdleDisconnect)
}

// GetSandbox exposes a diagnostic Sandbox for callers that need to inspect
// availability. Returns a stateless RemoteSandbox surface for the current
// provider.
func (m *SessionBoundManager) GetSandbox() Sandbox {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ephemeral
}

// Execute is the shared entry point used by the DefaultManager compatibility
// layer, the skills manager, and the ephemeral tool path. It applies script
// security validation, then dispatches to the session-bound path (non-empty
// SessionID) or the ephemeral path.
func (m *SessionBoundManager) Execute(ctx context.Context, cfg *ExecuteConfig) (*ExecuteResult, error) {
	if m == nil {
		return nil, ErrSandboxDisabled
	}
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return nil, ErrSandboxDisabled
	}
	m.mu.RUnlock()

	if cfg == nil {
		return nil, ErrInvalidScript
	}
	if !cfg.SkipValidation {
		if err := runScriptValidation(m.validator, cfg); err != nil {
			log.Printf("[sandbox] security validation failed: %v", err)
			return &ExecuteResult{
				ExitCode: -1,
				Error:    err.Error(),
				Stderr:   fmt.Sprintf("Security validation failed: %v", err),
			}, ErrSecurityViolation
		}
	}

	if strings.TrimSpace(cfg.SessionID) == "" {
		return m.ephemeral.Execute(ctx, cfg)
	}
	sc, err := m.scope(ctx, cfg.SessionID)
	if err != nil {
		return nil, err
	}
	ctx, release, err := m.admitUser(ctx, sc, "exec")
	if err != nil {
		return nil, err
	}
	defer release()
	handle, err := m.resolveScope(ctx, sc)
	if err != nil {
		return nil, err
	}
	outputDir := executionOutputDir(cfg)
	if sc.user {
		outputDir = sc.layout.OutputDir
	}
	if err := m.prepareSessionDirs(ctx, handle, DefaultSandboxExecUser, sc.layout.InputDir, outputDir); err != nil {
		return nil, err
	}
	if !sc.user {
		return m.ephemeral.ExecuteOnHandle(ctx, handle, cfg)
	}
	userCfg := *cfg
	userCfg.Env = withSessionEnv(cfg.Env, sc.layout)
	return m.ephemeral.ExecuteOnHandleIn(ctx, handle, &userCfg, sc.layout.Root, userScriptDir(sc.layout))
}

// ensureSessionWorkspaceDirs prepares the shared execution layout as the same
// account that runs commands. Preparation failures abort the operation; hiding
// them sends the agent into repeated writes through different tools.
func (m *SessionBoundManager) ensureSessionWorkspaceDirs(
	ctx context.Context, handle RemoteSandboxHandle, outputDir string,
) error {
	return m.prepareSessionDirs(ctx, handle, DefaultSandboxExecUser, SessionInputRoot, outputDir)
}

// prepareSessionDirs never renames or deletes existing data to "repair" access.
// A bad image or an inaccessible directory needs an explicit diagnosis, not an
// apparently empty replacement directory and missing attachments/artifacts.
func (m *SessionBoundManager) prepareSessionDirs(
	ctx context.Context, handle RemoteSandboxHandle, user string, dirs ...string,
) (prepErr error) {
	ctx, span := startSandboxSpan(ctx, "sandbox.ensure_workspace",
		map[string]interface{}{"directories": dirs, "user": user}, sandboxHandleMeta(handle))
	defer func() { span.Finish(nil, nil, prepErr) }()
	result, err := m.client.Exec(ctx, handle, RemoteExecRequest{
		Shell:   true,
		Command: workspaceBootstrapCommand(dirs...),
		User:    user,
		Timeout: sessionArtifactDirBootstrapTimeout,
	})
	if err != nil {
		return fmt.Errorf("sandbox: workspace preparation failed for user %s: %w; command was not started", user, err)
	}
	if result == nil || result.ExitCode != 0 || result.Killed {
		detail := "provider returned no result"
		if result != nil {
			detail = fmt.Sprintf("exit=%d killed=%t stderr=%s", result.ExitCode, result.Killed, strings.TrimSpace(result.Stderr))
		}
		return fmt.Errorf("sandbox: workspace preparation failed for user %s at %s: %s. "+
			"Command was not started; existing files were preserved. "+
			"Use an accessible directory under /workspace. If /workspace itself is inaccessible, "+
			"the sandbox image/template must provide /workspace owned by %s. "+
			"Switching tools or retrying the same operation will not change filesystem permissions",
			user, strings.Join(dirs, ", "), detail, DefaultSandboxExecUser)
	}
	return nil
}

func workspaceBootstrapCommand(dirs ...string) string {
	quoted := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		quoted = append(quoted, ShellQuote(dir))
	}
	return fmt.Sprintf(
		`set -e; for d in %s; do `+
			`if [ -L "$d" ]; then echo "workspace directory is a symlink: $d" >&2; exit 1; fi; `+
			`mkdir -p -- "$d"; `+
			`if [ ! -d "$d" ] || [ ! -w "$d" ] || [ ! -x "$d" ]; then `+
			`echo "workspace directory is not writable/searchable: $d" >&2; exit 1; fi; done`,
		strings.Join(quoted, " "),
	)
}

// withWorkspaceEnvDefaults stamps the workspace paths onto the sandbox's own
// environment. A tenant-configured value wins: an operator who points the
// artifact directory somewhere else must not have it overwritten here.
func withWorkspaceEnvDefaults(env map[string]string) map[string]string {
	if env == nil {
		env = make(map[string]string, 2)
	}
	if strings.TrimSpace(env[skillOutputEnvVar]) == "" {
		env[skillOutputEnvVar] = SessionOutputRoot
	}
	if strings.TrimSpace(env[sessionInputEnvVar]) == "" {
		env[sessionInputEnvVar] = SessionInputRoot
	}
	return env
}

// executionOutputDir resolves the artifact directory for this Execute call.
// It prefers WEKNORA_SKILL_OUTPUT_DIR from cfg.Env when the path stays under
// SessionWorkspaceRoot; otherwise it falls back to SessionOutputRoot.
func executionOutputDir(cfg *ExecuteConfig) string {
	if cfg != nil && cfg.Env != nil {
		if dir := strings.TrimSpace(cfg.Env[skillOutputEnvVar]); dir != "" {
			if clean, ok := ValidatedSessionOutputDir(dir); ok {
				return clean
			}
		}
	}
	return SessionOutputRoot
}

// DestroySession removes the remote sandbox bound to sessionID (if any) and
// the authoritative binding. Idempotent: succeeds on absent sessions.
func (m *SessionBoundManager) DestroySession(ctx context.Context, sessionID string) error {
	if m == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	if m.remoteDisabled() {
		return nil
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return err
	}
	if sc.user {
		return ErrSharedSandbox
	}
	return m.lifecycle.Destroy(ctx, sc.key)
}

// InvalidateConfigSandboxes marks every session sandbox this config owns stale,
// so each session rebuilds its sandbox from the config's current image on its
// next use, and reports how many bindings were marked.
//
// It is the image-maintenance counterpart to DestroySession: nothing is torn
// down here, so marking cannot delete a sandbox that is executing right now.
// The replacement happens at the session's next resolve, which may be the next
// operation of a turn already in flight; see resolveLocked for that limitation.
func (m *SessionBoundManager) InvalidateConfigSandboxes(
	ctx context.Context, tenantID uint64, configID string,
) (int, error) {
	if err := m.requireRemoteBackend(); err != nil {
		return 0, err
	}
	marked, err := m.bindings.InvalidateByConfig(ctx, tenantID, configID)
	if m.allocations == nil {
		return marked, err
	}
	// tenantID is the config's owner, so this covers every borrower's machine.
	machines, listErr := m.allocations.UserMachinesByConfig(ctx, tenantID, NormalizeConfigID(configID))
	if listErr != nil {
		return marked, errors.Join(err, fmt.Errorf("list user machines: %w", listErr))
	}
	n, markErr := m.InvalidateConfigSandboxesForMachines(ctx, configID, machines)
	return marked + n, errors.Join(err, markErr)
}

// InvalidateConfigSandboxesForMachines marks the listed user machines stale.
func (m *SessionBoundManager) InvalidateConfigSandboxesForMachines(
	ctx context.Context, configID string, machines []allocation.UserMachine,
) (int, error) {
	scanner, ok := m.bindings.(tenantBindingScanner)
	if !ok {
		return 0, nil
	}
	keys := make([]SessionSandboxKey, 0, len(machines))
	for _, machine := range machines {
		keys = append(keys, m.UserMachineKey(machine))
	}
	return markBindingKeysStale(ctx, scanner, keys, configID)
}

// CreateSnapshot forwards provider snapshot creation for the live sandbox bound
// to sessionID. Session execution never uses this optional capability; it is
// reserved for skill image maintenance.
func (m *SessionBoundManager) CreateSnapshot(
	ctx context.Context, sessionID string, name string,
) (RemoteSnapshotRef, error) {
	if err := m.requireRemoteBackend(); err != nil {
		return RemoteSnapshotRef{}, err
	}
	snapshots, ok := SnapshotManagerFrom(m.client)
	if !ok || !m.client.Capabilities().SupportsSnapshots {
		return RemoteSnapshotRef{}, errors.New("sandbox: remote provider does not support snapshots")
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return RemoteSnapshotRef{}, err
	}
	ctx, release, err := m.admitUser(ctx, sc, "exec")
	if err != nil {
		return RemoteSnapshotRef{}, err
	}
	defer release()
	handle, err := m.resolveScope(ctx, sc)
	if err != nil {
		return RemoteSnapshotRef{}, err
	}
	return snapshots.CreateSnapshot(ctx, handle.ID(), name)
}

// DeleteSnapshot forwards provider snapshot deletion. The skill install path
// uses it to abandon an orphan when the pointer switch fails; the reaper uses
// it to prune superseded snapshots that have aged past retention.
func (m *SessionBoundManager) DeleteSnapshot(ctx context.Context, snapshotID string) error {
	if err := m.requireRemoteBackend(); err != nil {
		return err
	}
	snapshots, ok := SnapshotManagerFrom(m.client)
	if !ok || !m.client.Capabilities().SupportsSnapshots {
		return errors.New("sandbox: remote provider does not support snapshots")
	}
	return snapshots.DeleteSnapshot(ctx, snapshotID)
}

// DeleteForkSnapshot removes a fork snapshot. sessionID is accepted so the
// method matches SessionForkSandboxPort; deletion is by snapshot ID.
func (m *SessionBoundManager) DeleteForkSnapshot(
	ctx context.Context, _ /* sessionID */, snapshotID string,
) error {
	return m.DeleteSnapshot(ctx, snapshotID)
}

// ListSnapshots forwards provider snapshot listing for audit and later cleanup
// tasks.
func (m *SessionBoundManager) ListSnapshots(
	ctx context.Context, sandboxID string,
) ([]RemoteSnapshotRef, error) {
	if err := m.requireRemoteBackend(); err != nil {
		return nil, err
	}
	snapshots, ok := SnapshotManagerFrom(m.client)
	if !ok || !m.client.Capabilities().SupportsSnapshots {
		return nil, errors.New("sandbox: remote provider does not support snapshots")
	}
	return snapshots.ListSnapshots(ctx, sandboxID)
}

// EnsureSessionDir creates dir inside the session's live sandbox when one is
// bound. It is a no-op when the session has no live binding; the skill
// framework will materialise the directory during the next Execute call.
func (m *SessionBoundManager) EnsureSessionDir(ctx context.Context, sessionID, dir string) error {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	if m.remoteDisabled() || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return err
	}
	ctx, release, err := m.admitUser(ctx, sc, "file")
	if err != nil {
		return err
	}
	defer release()
	handle, ok, err := m.lookupSessionHandle(ctx, sessionID)
	if err != nil || !ok {
		return err
	}
	if err := ignoreExistingDir(m.client.MakeDir(ctx, handle, dir)); err != nil {
		return fmt.Errorf("sandbox: ensure session dir %s: %w", dir, err)
	}
	return nil
}

// WriteSessionInputFile writes a durable attachment path into the session's
// remote sandbox, provisioning the sandbox on first call. It is refused when
// the manager has fallen back to Local (writing to the host would leak
// attachments outside the tenant's isolation boundary).
func (m *SessionBoundManager) WriteSessionInputFile(
	ctx context.Context, sessionID, filePath string, content []byte,
) error {
	if err := m.requireRemoteBackend(); err != nil {
		return err
	}
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("sandbox: session ID required for input staging")
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return err
	}
	clean, err := cleanSessionInputPath(filePath)
	if sc.user {
		clean, err = workspace.CleanInputPathIn(sc.layout, filePath)
	}
	if err != nil {
		return err
	}
	ctx, release, err := m.admitUser(ctx, sc, "file")
	if err != nil {
		return err
	}
	defer release()
	var handle RemoteSandboxHandle
	for attempt := 0; attempt < 2; attempt++ {
		handle, err = m.resolveScope(ctx, sc)
		if err != nil {
			return err
		}
		err = m.writeSessionInputOnHandle(ctx, handle, clean, content)
		if err == nil || attempt == 1 || !sessionBindingLost(err) {
			return err
		}
		// The idle sweep can delete the container after resolve and before
		// mkdir. The binding still points at that container, so drop it and
		// provision once more. A missing path inside a live container does
		// not take this branch.
		if dropErr := m.dropBoundSandbox(ctx, sc.machine(), handle.ID()); dropErr != nil {
			return err
		}
	}
	return err
}

func (m *SessionBoundManager) writeSessionInputOnHandle(
	ctx context.Context, handle RemoteSandboxHandle, clean string, content []byte,
) error {
	if err := ignoreExistingDir(m.client.MakeDir(ctx, handle, path.Dir(clean))); err != nil {
		return fmt.Errorf("sandbox: create input directory: %w", err)
	}
	if err := m.client.WriteFile(ctx, handle, clean, content); err != nil {
		return fmt.Errorf("sandbox: write session input %s: %w", clean, err)
	}
	return nil
}

// sessionBindingLost reports a sandbox that is gone. A missing path inside a
// live container is also NotFound ("does not exist"); rebinding on that
// would discard the session.
func sessionBindingLost(err error) bool {
	if !CanReplaceRemoteBinding(err) {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "does not exist") && !strings.Contains(msg, "vanished") {
		return false
	}
	return true
}

func (m *SessionBoundManager) dropBoundSandbox(ctx context.Context, key SessionSandboxKey, sandboxID string) error {
	binding, err := m.bindings.Get(ctx, key)
	if err != nil || binding == nil || binding.SandboxID != sandboxID || binding.Provider != m.client.Provider() {
		return err
	}
	_, err = m.bindings.DeleteIfMatch(ctx, key, binding.Provider, binding.SandboxID)
	return err
}

// WriteSessionWorkspaceFile writes a model-authored file into the session's
// remote sandbox, provisioning the sandbox on first call. Paths must sit
// inside the session sandbox and must not land in /workspace/input.
func (m *SessionBoundManager) WriteSessionWorkspaceFile(
	ctx context.Context, sessionID, filePath string, content []byte,
) error {
	return m.WriteSessionWorkspaceFiles(ctx, sessionID, []SessionWorkspaceFile{{
		Path:    filePath,
		Content: content,
	}})
}

// WriteSessionWorkspaceFiles prepares the session workspace once, then writes
// every file. Staging a host skill tree must not re-run directory bootstrap
// or walk the parent path for each entry.
func (m *SessionBoundManager) WriteSessionWorkspaceFiles(
	ctx context.Context, sessionID string, files []SessionWorkspaceFile,
) error {
	if err := m.requireRemoteBackend(); err != nil {
		return err
	}
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("sandbox: session ID required for workspace write")
	}
	if len(files) == 0 {
		return nil
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return err
	}
	type item struct {
		path    string
		content []byte
	}
	items := make([]item, 0, len(files))
	parents := make(map[string]struct{}, len(files))
	for _, file := range files {
		// Session mode keeps the historical cleaner, which is pinned to
		// /workspace and ignores a host WEKNORA_SKILL_OUTPUT_DIR overlay.
		clean, err := cleanSessionWorkspaceWritePath(file.Path)
		if sc.user {
			clean, err = workspace.CleanWriteFileIn(sc.layout, file.Path)
		}
		if err != nil {
			return err
		}
		items = append(items, item{path: clean, content: file.Content})
		parents[path.Dir(clean)] = struct{}{}
	}
	ctx, release, err := m.admitUser(ctx, sc, "file")
	if err != nil {
		return err
	}
	defer release()
	handle, err := m.resolveScope(ctx, sc)
	if err != nil {
		return err
	}
	if sc.user {
		if err := m.prepareSessionDirs(
			ctx, handle, DefaultSandboxExecUser, sc.layout.InputDir, sc.layout.OutputDir,
		); err != nil {
			return err
		}
	} else if err := m.ensureSessionWorkspaceDirs(ctx, handle, SessionOutputRoot); err != nil {
		return err
	}
	for parent := range parents {
		if err := ignoreExistingDir(m.client.MakeDir(ctx, handle, parent)); err != nil {
			return fmt.Errorf("sandbox: create workspace directory: %w", err)
		}
	}
	for _, file := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.client.WriteFile(ctx, handle, file.path, file.content); err != nil {
			return fmt.Errorf("sandbox: write session file %s: %w", file.path, err)
		}
	}
	return nil
}

// RemoveSessionInputPath deletes a staged attachment. It is a no-op when the
// session has no live sandbox and never provisions one.
func (m *SessionBoundManager) RemoveSessionInputPath(
	ctx context.Context, sessionID, targetPath string,
) error {
	if err := m.requireRemoteBackend(); err != nil {
		return err
	}
	if strings.TrimSpace(sessionID) == "" {
		if _, err := cleanSessionInputPath(targetPath); err != nil {
			return err
		}
		return nil
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return err
	}
	clean, err := cleanSessionInputPath(targetPath)
	if sc.user {
		clean, err = workspace.CleanInputPathIn(sc.layout, targetPath)
	}
	if err != nil {
		return err
	}
	ctx, release, err := m.admitUser(ctx, sc, "file")
	if err != nil {
		return err
	}
	defer release()
	handle, ok, err := m.lookupSessionHandle(ctx, sessionID)
	if err != nil || !ok {
		return err
	}
	if err := m.client.Remove(ctx, handle, clean); err != nil {
		return fmt.Errorf("sandbox: remove session input %s: %w", clean, err)
	}
	return nil
}

// ListSessionFiles walks dir under the session's live sandbox recursively.
// Returns nil (no error) when the session has no bound sandbox so callers can
// treat "no sandbox" and "empty output" uniformly.
func (m *SessionBoundManager) ListSessionFiles(
	ctx context.Context, sessionID, dir string,
) ([]RemoteDirEntry, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("sandbox: dir required for ListSessionFiles")
	}
	if m.remoteDisabled() || strings.TrimSpace(sessionID) == "" {
		return nil, nil
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	ctx, release, err := m.admitUser(ctx, sc, "file")
	if err != nil {
		return nil, err
	}
	defer release()
	handle, ok, err := m.lookupSessionHandle(ctx, sessionID)
	if err != nil || !ok {
		return nil, err
	}
	return m.walkSessionFiles(ctx, handle, dir)
}

// StatSessionFile returns metadata for a single file without downloading
// contents. Returns an error when no sandbox is bound: callers of this
// method already hold a path from a prior ListSessionFiles call and should
// not race with reaper/destroy.
func (m *SessionBoundManager) StatSessionFile(
	ctx context.Context, sessionID, filePath string,
) (*RemoteStatEntry, error) {
	if strings.TrimSpace(filePath) == "" {
		return nil, errors.New("sandbox: path required for StatSessionFile")
	}
	if m.remoteDisabled() || strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("sandbox: no live sandbox for session %s", sessionID)
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	ctx, release, err := m.admitUser(ctx, sc, "file")
	if err != nil {
		return nil, err
	}
	defer release()
	handle, ok, err := m.lookupSessionHandle(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("sandbox: no live sandbox for session %s", sessionID)
	}
	return m.client.Stat(ctx, handle, filePath)
}

// ReadSessionFile downloads a file from the session's live sandbox. Errors
// when no sandbox is bound for the same reason as StatSessionFile.
func (m *SessionBoundManager) ReadSessionFile(
	ctx context.Context, sessionID, filePath string,
) ([]byte, error) {
	if strings.TrimSpace(filePath) == "" {
		return nil, errors.New("sandbox: path required for ReadSessionFile")
	}
	if m.remoteDisabled() || strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("sandbox: no live sandbox for session %s", sessionID)
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	ctx, release, err := m.admitUser(ctx, sc, "file")
	if err != nil {
		return nil, err
	}
	defer release()
	handle, ok, err := m.lookupSessionHandle(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("sandbox: no live sandbox for session %s", sessionID)
	}
	return m.client.ReadFile(ctx, handle, filePath)
}

// WriteSessionFile writes an install/maintenance file into the session's live
// sandbox. It is deliberately narrower than a general remote write: only the
// tenant skills image root is accepted, because ordinary attachments must keep
// using WriteSessionInputFile and its /workspace/input guard.
func (m *SessionBoundManager) WriteSessionFile(
	ctx context.Context, sessionID, filePath string, content []byte,
) error {
	if err := m.requireRemoteBackend(); err != nil {
		return err
	}
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("sandbox: session ID required for file staging")
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return err
	}
	clean := path.Clean(strings.TrimSpace(filePath))
	if clean != SkillsImageRoot && !strings.HasPrefix(clean, SkillsImageRoot+"/") {
		return fmt.Errorf("sandbox: install file path %q is outside %s", filePath, SkillsImageRoot)
	}
	ctx, release, err := m.admitUser(ctx, sc, "file")
	if err != nil {
		return err
	}
	defer release()
	ctx = withMaintenanceFilesystem(ctx)
	handle, err := m.resolveScope(ctx, sc)
	if err != nil {
		return err
	}
	// resetSkillDir already created this folder with mkdir -p. Cube's MakeDir
	// then reports the existing directory as an error; ignoreExistingDir keeps
	// that from aborting the seed of SKILL.md.
	if err := ignoreExistingDir(m.client.MakeDir(ctx, handle, path.Dir(clean))); err != nil {
		return fmt.Errorf("sandbox: create install directory: %w", err)
	}
	if err := m.client.WriteFile(ctx, handle, clean, content); err != nil {
		return fmt.Errorf("sandbox: write install file %s: %w", clean, err)
	}
	return nil
}

// ShellExecOptions carries per-call shell execution knobs. The install-only
// flags select the installer working-directory allowlist and bootstrap. Both
// ordinary and install calls currently execute as root.
type ShellExecOptions struct {
	// ExpectedSandboxID makes maintenance lookup-only: never allocate/rebuild,
	// and fail if the binding changed since the caller selected this sandbox.
	ExpectedSandboxID string
	OnOutput          func(stream string, chunk []byte)

	WorkDir string
	Timeout time.Duration
	Env     map[string]string

	// AllowSkillsRoot selects the installer workspace/skills working-directory scope.
	// See cleanSessionWorkDir for why the work_dir allowlist is lexical only.
	// Never set this from a model-authored tool such as shell_exec.
	AllowSkillsRoot bool
	// AsRoot forces root and selects the maintenance bootstrap: only WorkDir
	// is prepared, without requiring /workspace/input or /workspace/output.
	// The default account is already root, but the bootstrap still differs.
	// AllowSkillsRoot separately selects the installer working-directory scope;
	// it is not a filesystem boundary for commands running as root.
	AsRoot bool
	// SkipWorkspacePrep omits prepareSessionDirs. Desktop maintenance and
	// workspace checkpoints operate on an existing layout. Never set this from a model-authored tool
	// such as shell_exec — agents still need the workspace contract.
	SkipWorkspacePrep bool
}

// ExecShellCommand runs a shell one-liner inside the session's persistent
// sandbox. work_dir may name any absolute directory inside that sandbox.
func (m *SessionBoundManager) ExecShellCommand(
	ctx context.Context,
	sessionID string,
	command string,
	workDir string,
	timeout time.Duration,
	env map[string]string,
) (*ExecuteResult, error) {
	return m.ExecShellCommandWithOptions(ctx, sessionID, command, ShellExecOptions{
		WorkDir: workDir,
		Timeout: timeout,
		Env:     env,
	})
}

// ExecShellCommandWithOptions runs a shell command with install-only options.
// Fallback is explicitly refused so even privileged installer calls never
// escape onto the WeKnora host machine.
func (m *SessionBoundManager) ExecShellCommandWithOptions(
	ctx context.Context,
	sessionID string,
	command string,
	opts ShellExecOptions,
) (*ExecuteResult, error) {
	result, _, err := m.execShellCommandWithOutputSnapshot(ctx, sessionID, command, opts, "")
	return result, err
}

// ShellOutputSnapshot contains two complete metadata listings of the same sandbox.
// A nil snapshot means inspection failed; an empty snapshot means no output files.
type ShellOutputSnapshot struct {
	Before []RemoteDirEntry
	After  []RemoteDirEntry
}

// ExecShellCommandWithOutputSnapshot resolves the session once for the command
// and both artifact probes. Handles never escape this call or survive a rebuild.
func (m *SessionBoundManager) ExecShellCommandWithOutputSnapshot(
	ctx context.Context, sessionID, command string, opts ShellExecOptions, outputDir string,
) (*ExecuteResult, *ShellOutputSnapshot, error) {
	return m.execShellCommandWithOutputSnapshot(ctx, sessionID, command, opts, outputDir)
}

func (m *SessionBoundManager) execShellCommandWithOutputSnapshot(
	ctx context.Context, sessionID, command string, opts ShellExecOptions, outputDir string,
) (*ExecuteResult, *ShellOutputSnapshot, error) {
	if err := m.requireRemoteBackend(); err != nil {
		return nil, nil, fmt.Errorf(
			"sandbox: shell_exec requires the remote sandbox provider (current mode: %s)",
			m.GetType(),
		)
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, nil, errors.New("sandbox: session_id required for ExecShellCommand")
	}
	if strings.TrimSpace(command) == "" {
		return nil, nil, errors.New("sandbox: command required for ExecShellCommand")
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = m.config.DefaultTimeout
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}
	if outputDir != "" {
		clean, ok := ValidatedOutputDirIn(sc.layout, outputDir)
		if !sc.user {
			clean, ok = ValidatedSessionOutputDir(outputDir)
		}
		if !ok {
			return nil, nil, errors.New("sandbox: invalid output directory")
		}
		outputDir = clean
	}
	workDir := strings.TrimSpace(opts.WorkDir)
	if workDir == "" {
		workDir = sc.layout.Root
	}
	workDir, err = workspace.CleanWorkDirIn(sc.layout, workDir, opts.AllowSkillsRoot)
	if !sc.user {
		fallback := SessionWorkspaceRoot
		if strings.TrimSpace(opts.WorkDir) != "" {
			fallback = strings.TrimSpace(opts.WorkDir)
		}
		workDir, err = cleanSessionWorkDir(fallback, opts.AllowSkillsRoot)
	}
	if err != nil {
		return nil, nil, err
	}
	ctx, release, err := m.admitUser(ctx, sc, "shell")
	if err != nil {
		return nil, nil, err
	}
	defer release()

	var handle RemoteSandboxHandle
	if opts.ExpectedSandboxID != "" {
		if !sc.user {
			exists, checkErr := m.checker.SessionExists(ctx, sc.sessionKeyOnly())
			if checkErr != nil {
				return nil, nil, checkErr
			}
			if !exists {
				return nil, nil, ErrSandboxSessionDeleted
			}
		}
		var found bool
		// Maintenance must recheck the binding even inside a file-operation scope.
		handle, found, err = m.lookupSessionHandleForKey(ctx, sc.machine(), opts.ExpectedSandboxID)
		if err != nil {
			return nil, nil, err
		}
		if !found || handle.ID() != opts.ExpectedSandboxID {
			return nil, nil, errors.New("sandbox: bound sandbox changed before maintenance command")
		}
	} else {
		handle, err = m.resolveScope(ctx, sc)
		if err != nil {
			return nil, nil, err
		}
	}
	var snapshot *ShellOutputSnapshot
	if outputDir != "" {
		if before, err := m.snapshotOutputFiles(ctx, handle, outputDir); err == nil {
			snapshot = &ShellOutputSnapshot{Before: before}
		}
	}
	user := DefaultSandboxExecUser
	if opts.AsRoot {
		user = "root"
	}
	if !opts.SkipWorkspacePrep {
		if opts.AsRoot {
			// Installation owns the skill directory and does not depend on a
			// writable session workspace (which is cleaned before snapshotting).
			if err := m.prepareSessionDirs(ctx, handle, user, workDir); err != nil {
				return nil, nil, err
			}
		} else if sc.user {
			if err := m.prepareSessionDirs(
				ctx, handle, user, sc.layout.InputDir, sc.layout.OutputDir, workDir,
			); err != nil {
				return nil, nil, err
			}
		} else if err := m.prepareSessionDirs(
			ctx, handle, user, SessionInputRoot, SessionOutputRoot, workDir,
		); err != nil {
			return nil, nil, err
		}
	}
	env := opts.Env
	if sc.user {
		env = withSessionEnv(opts.Env, sc.layout)
	}

	start := time.Now()
	execResult, execErr := m.client.Exec(ctx, handle, RemoteExecRequest{
		Command:  command,
		OnOutput: commandOutputCallback(ctx, opts.OnOutput),
		Shell:    true,
		Env:      env,
		WorkDir:  workDir,
		User:     user,
		Timeout:  timeout,
	})
	duration := time.Since(start)
	if snapshot != nil {
		if after, err := m.snapshotOutputFiles(ctx, handle, outputDir); err == nil {
			snapshot.After = after
		} else {
			snapshot = nil
		}
	}
	return remoteExecuteResult(execResult, execErr, duration), snapshot, nil
}

// SessionShellExecutor advertises the shell-execution capability while the
// manager is open.
func (m *SessionBoundManager) SessionShellExecutor() SessionShellExecutor {
	if m == nil || m.remoteDisabled() {
		return nil
	}
	return m
}

// SessionInstallShellExecutor advertises the privileged install-mode shell.
func (m *SessionBoundManager) SessionInstallShellExecutor() SessionInstallShellExecutor {
	if m == nil || m.remoteDisabled() {
		return nil
	}
	return m
}

// SessionWorkspaceLayout reports where sessionID's files live. A session-owned
// machine keeps the /workspace contract (a validated WEKNORA_SKILL_OUTPUT_DIR
// overlays OutputDir). A shared machine reports that session's directory.
// Lookups do not admit and do not require a tenant: tool descriptions call
// this before any provider operation.
func (m *SessionBoundManager) SessionWorkspaceLayout(ctx context.Context, sessionID string) (WorkspaceLayout, error) {
	a, ok, err := m.addressFor(ctx, strings.TrimSpace(sessionID))
	if err != nil {
		return WorkspaceLayout{}, err
	}
	if !ok || !a.IsUser() {
		return withValidatedSkillOutputDir(RemoteWorkspaceLayout()), nil
	}
	return a.Layout()
}

func withValidatedSkillOutputDir(layout WorkspaceLayout) WorkspaceLayout {
	raw := strings.TrimSpace(os.Getenv(skillOutputEnvVar))
	if raw == "" {
		return layout
	}
	clean, ok := ValidatedSessionOutputDir(raw)
	if !ok {
		return layout
	}
	previous := layout.OutputDir
	layout.OutputDir = clean
	if previous == clean || len(layout.ReadRoots) == 0 {
		return layout
	}
	roots := append([]string(nil), layout.ReadRoots...)
	for i, root := range roots {
		if root == previous {
			roots[i] = clean
		}
	}
	layout.ReadRoots = roots
	return layout
}

var _ SessionWorkspaceLayoutProvider = (*SessionBoundManager)(nil)

// SessionFileStore advertises the session-scoped filesystem capability while
// a real remote backend is active and the provider implements the enumeration
// operations (ListDir / Stat / MakeDir / Remove).
func (m *SessionBoundManager) SessionFileStore() SessionFileStore {
	if m == nil || m.remoteDisabled() {
		return nil
	}
	if !m.client.Capabilities().SupportsFilesystemEnumeration {
		return nil
	}
	return m
}

// SessionTerminalManager advertises the interactive-terminal capability while
// a real remote backend is active and the provider implements PTY streaming
// (E2B, Cube and Docker all do).
func (m *SessionBoundManager) SessionTerminalManager() SessionTerminalManager {
	if m == nil || m.remoteDisabled() {
		return nil
	}
	if _, ok := TerminalManagerFrom(m.client); !ok {
		return nil
	}
	return m
}

// OpenSessionTerminal opens a PTY on the sandbox currently bound to the
// session. It is strictly lookup-only: with no live binding it returns
// ErrNoLiveSessionSandbox instead of provisioning, because the terminal
// entry point lacks the config-pin context that agent-driven creation
// relies on. A bound sandbox that is not confirmed running returns
// ErrSandboxPaused unless opts.AllowResume is set — Connect would wake a
// paused instance. A backend that cannot stream PTYs returns
// ErrTerminalUnsupported, not "no sandbox".
func (m *SessionBoundManager) OpenSessionTerminal(
	ctx context.Context,
	sessionID string,
	opts RemoteTerminalOptions,
) (RemoteTerminalSession, error) {
	terminal, ok := TerminalManagerFrom(m.client)
	if !ok {
		return nil, ErrTerminalUnsupported
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if sc.user {
		if strings.TrimSpace(opts.Cwd) == "" {
			opts.Cwd = sc.layout.Root
		}
		opts.Envs = withSessionEnv(opts.Envs, sc.layout)
		// C10: PIDs on a shared machine belong to every session of the
		// owner; a client-supplied PID could attach another session's shell.
		opts.AttachPID = 0
	}
	// The PTY outlives this call: admit it on a context detached from the
	// HTTP request; heldTerminalSession releases it on Close.
	opCtx, release, err := m.admitUser(context.WithoutCancel(ctx), sc, "pty")
	if err != nil {
		return nil, err
	}
	// The dial and the stream both stop if the lease is lost. callCtx lives
	// as long as the terminal: cancelling it when this function returns
	// would close a PTY that has only just opened.
	callCtx, cancelCall := context.WithCancelCause(ctx)
	stopCall := context.AfterFunc(opCtx, func() { cancelCall(context.Cause(opCtx)) })
	abandon := func(err error) (RemoteTerminalSession, error) {
		stopCall()
		cancelCall(err)
		release()
		return nil, err
	}
	if !opts.AllowResume {
		if err := m.RequireRunningSessionSandbox(callCtx, sessionID); err != nil {
			return abandon(err)
		}
	}
	handle, found, err := m.lookupSessionHandle(callCtx, sessionID)
	if err != nil || !found {
		if err != nil {
			return abandon(err)
		}
		return abandon(ErrNoLiveSessionSandbox)
	}
	// A shared machine already exists for the owner's other sessions. This
	// session's directory is created by the first shell command, not by
	// attaching a PTY, so opening the terminal first would start a shell in
	// a directory that is not there yet.
	if sc.user {
		if err := m.prepareSessionDirs(
			callCtx, handle, DefaultSandboxExecUser,
			sc.layout.InputDir, sc.layout.OutputDir, opts.Cwd,
		); err != nil {
			return abandon(err)
		}
	}
	session, err := terminal.OpenTerminal(callCtx, handle, opts)
	if err != nil {
		return abandon(err)
	}
	if !sc.user {
		stopCall()
		release()
		return session, nil
	}
	held := &heldTerminalSession{
		RemoteTerminalSession: session,
		release:               release,
		cancelStream:          cancelCall,
		stopLeaseWatch:        stopCall,
	}
	if exempt := admissionExemptFrom(opCtx); len(exempt) > 0 {
		held.member = exempt[len(exempt)-1]
	}
	// C3: once the lease is lost another replica may rebuild or reclaim the
	// machine, so the PTY must not keep writing to it.
	held.stopWatch = context.AfterFunc(opCtx, func() { _ = held.Close() })
	return held, nil
}

// heldTerminalSession keeps the shared machine's admission for as long as
// the PTY is attached, so a rebuild or reclaim waits for the user to close
// the terminal.
type heldTerminalSession struct {
	RemoteTerminalSession
	once           sync.Once
	closeErr       error
	release        func()
	cancelStream   func(error)
	stopLeaseWatch func() bool
	stopWatch      func() bool
	member         string // the PTY's admission member; tests use it to inject loss
}

// Close closes the PTY and releases its admission. It runs at most once,
// whether the user closed the terminal or the lease was lost.
func (s *heldTerminalSession) Close() error {
	s.once.Do(func() {
		if s.stopWatch != nil {
			s.stopWatch()
		}
		if s.stopLeaseWatch != nil {
			s.stopLeaseWatch()
		}
		s.closeErr = s.RemoteTerminalSession.Close()
		if s.cancelStream != nil {
			s.cancelStream(context.Canceled)
		}
		s.release()
	})
	return s.closeErr
}

var _ SessionTerminalProvider = (*SessionBoundManager)(nil)

// SessionDesktopManager advertises the graphical-desktop capability while a
// real remote backend is active, the provider can relay a data-plane
// WebSocket (E2B and Cube do; Docker is not scheduled), and this config's
// base image is a desktop template. DesktopEnabled is the stored bit that
// survives skill snapshots replacing template_id with a UUID; without it the
// tab would Exec ensure.sh on a CLI image and only then report unsupported.
func (m *SessionBoundManager) SessionDesktopManager() SessionDesktopManager {
	if m == nil || m.remoteDisabled() {
		return nil
	}
	if m.config == nil || !m.config.DesktopEnabled {
		return nil
	}
	if _, ok := DesktopManagerFrom(m.client); !ok {
		return nil
	}
	return m
}

// RequireRunningSessionSandbox reports ErrNoLiveSessionSandbox or
// ErrSandboxPaused without Connect. Lookup-only terminal and desktop opens
// use it so opening a panel cannot resume (and re-bill) a paused instance.
// Only a List-confirmed running sandbox is safe: paused/transitioning
// Connect resumes, and a list miss with a stale binding is not "no sandbox".
func (m *SessionBoundManager) RequireRunningSessionSandbox(
	ctx context.Context,
	sessionID string,
) error {
	if m == nil {
		return ErrNoLiveSessionSandbox
	}
	state, bound, err := m.peekBoundSandboxState(ctx, sessionID)
	if err != nil {
		return err
	}
	if !bound {
		return ErrNoLiveSessionSandbox
	}
	if state != RemoteStateRunning {
		if m.client.Provider() == SandboxTypeDocker {
			return ErrSandboxStopped
		}
		return ErrSandboxPaused
	}
	return nil
}

// OpenSessionDesktop dials the desktop of the sandbox currently bound to the
// session.
//
// Unlike OpenSessionTerminal there is no AllowResume branch: by the time this
// runs, SandboxDesktopService has already driven the same
// resolveSandboxForExecution path a chat turn uses, so the sandbox is live
// and — critically — its inbound token is registered on THIS replica.
// Reaching here with no binding means the sandbox went away in between,
// which is an error, not a reason to provision.
func (m *SessionBoundManager) OpenSessionDesktop(
	ctx context.Context,
	sessionID string,
	opts RemoteDesktopOptions,
) (*SessionDesktopConn, error) {
	if m.SessionDesktopManager() == nil {
		return nil, ErrDesktopUnsupported
	}
	desktop, ok := DesktopManagerFrom(m.client)
	if !ok {
		return nil, ErrDesktopUnsupported
	}
	handle, found, err := m.lookupSessionHandle(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNoLiveSessionSandbox
	}
	conn, err := desktop.DialDesktop(ctx, handle, opts)
	if err != nil {
		return nil, err
	}
	out := &SessionDesktopConn{Conn: conn, SandboxID: handle.ID()}
	if refresher, ok := DesktopTTLRefresherFrom(m.client); ok {
		bound := handle
		out.StartTTLRefresh = func(ttlCtx context.Context) {
			refresher.StartDesktopTTLRefresh(ttlCtx, bound)
		}
	}
	return out, nil
}

var _ SessionDesktopProvider = (*SessionBoundManager)(nil)

// Cleanup marks the manager closed. Session sandboxes are not force-deleted
// here: their lifecycle is authoritative in the binding store and would
// leak to any other WeKnora replica if this replica reaped them on shutdown.
// Providers reclaim idle sandboxes via their own timeout/pause policies.
func (m *SessionBoundManager) Cleanup(_ context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.mu.Unlock()
	return nil
}

// --- internal helpers --------------------------------------------------------

// BeginSessionTurn opens one chat-turn hold for sessionID. The first resolve
// after this may rebuild a stale image; later resolves of the same turn keep
// the sandbox. end closes exactly this hold.
func (m *SessionBoundManager) BeginSessionTurn(ctx context.Context, sessionID string) (context.Context, func(), error) {
	noop := func() {}
	if m == nil {
		return ctx, noop, nil
	}
	leaser, ok := m.bindings.(sessionTurnLeaseStore)
	if !ok {
		return ctx, noop, nil
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}
	if err := leaser.BeginTurn(ctx, sc.key); err != nil {
		return nil, nil, err
	}
	endLease := func() {
		if err := leaser.EndTurn(context.WithoutCancel(ctx), sc.key); err != nil {
			logger.Warnf(ctx, "[sandbox] end turn for session %s failed: %v", sessionID, err)
		}
	}
	if !sc.user {
		var once sync.Once
		return ctx, func() { once.Do(endLease) }, nil
	}
	turnCtx, release, err := m.admitUser(ctx, sc, "turn")
	if err != nil {
		endLease()
		return nil, nil, err
	}
	var once sync.Once
	return turnCtx, func() {
		once.Do(func() {
			release()
			endLease()
		})
	}, nil
}

// HasActiveTurn reports whether an agent turn currently holds the session's
// sandbox lease. A store without turn-lease support is treated as not busy
// so fork can proceed.
func (m *SessionBoundManager) HasActiveTurn(ctx context.Context, sessionID string) (bool, error) {
	if m == nil {
		return false, nil
	}
	leaser, ok := m.bindings.(sessionTurnLeaseStore)
	if !ok {
		return false, nil
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return false, err
	}
	active, _, err := leaser.TurnState(ctx, sc.key)
	return active, err
}

const rewindExclusiveWait = 5 * time.Second

// LockRewindSession takes the session's rewind lock and, on a shared
// machine, the session's exclusive window, so no other operation of the
// session touches its work tree during the reset. Run the reset with the
// returned context. On a per-session sandbox the context is ctx itself.
func (m *SessionBoundManager) LockRewindSession(
	ctx context.Context, sessionID string,
) (context.Context, func(), error) {
	unlockRewind, err := m.TryLockRewind(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}
	exCtx, release, err := m.acquireSessionExclusive(ctx, sessionID, m.rewindWait)
	if err != nil {
		unlockRewind()
		return nil, nil, err
	}
	return exCtx, func() {
		release()
		unlockRewind()
	}, nil
}

// TryLockRewind takes an exclusive rewind lock for sessionID. Stores that do
// not implement rewind locking succeed as a no-op so local tests without a
// lease store still rewind.
func (m *SessionBoundManager) TryLockRewind(ctx context.Context, sessionID string) (func(), error) {
	noop := func() {}
	if m == nil {
		return noop, nil
	}
	locker, ok := m.bindings.(interface {
		TryLockRewind(context.Context, SessionSandboxKey) (func(), error)
	})
	if !ok {
		return noop, nil
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return locker.TryLockRewind(ctx, sc.key)
}

// HasRewindLock reports whether rewind currently holds sessionID.
func (m *SessionBoundManager) HasRewindLock(ctx context.Context, sessionID string) (bool, error) {
	if m == nil {
		return false, nil
	}
	reader, ok := m.bindings.(interface {
		HasRewindLock(context.Context, SessionSandboxKey) (bool, error)
	})
	if !ok {
		return false, nil
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return false, err
	}
	return reader.HasRewindLock(ctx, sc.key)
}

// CreateForkSnapshot snapshots the session's already-bound sandbox. It never
// provisions: an unbound session or a backend without snapshots returns an
// error so fork can degrade.
func (m *SessionBoundManager) CreateForkSnapshot(
	ctx context.Context, sessionID, name string,
) (string, error) {
	if err := m.requireRemoteBackend(); err != nil {
		return "", err
	}
	snapshots, ok := SnapshotManagerFrom(m.client)
	if !ok || !m.client.Capabilities().SupportsSnapshots {
		return "", errors.New("sandbox: remote provider does not support snapshots")
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if sc.user {
		return "", errors.New("sandbox: shared machines are forked by copying the session workspace")
	}
	sandboxID, bound := m.BoundSandboxID(ctx, sessionID)
	if !bound || sandboxID == "" {
		return "", errors.New("sandbox: session has no bound sandbox")
	}
	ref, err := createForkOrProviderSnapshot(ctx, m.client, snapshots, sandboxID, name)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(ref.ID)
	if id == "" {
		return "", errors.New("sandbox: snapshot returned empty id")
	}
	return id, nil
}

type forkSnapshotCreator interface {
	CreateForkSnapshot(ctx context.Context, sandboxID, name string) (RemoteSnapshotRef, error)
}

// createForkOrProviderSnapshot uses a fork-specific commit when the client
// has one (Docker's weknora-fork/ namespace). Cube and E2B have no extra
// namespace, so they keep using CreateSnapshot.
func createForkOrProviderSnapshot(
	ctx context.Context,
	client RemoteSandboxClient,
	snapshots RemoteSnapshotManager,
	sandboxID, name string,
) (RemoteSnapshotRef, error) {
	if creator, ok := client.(forkSnapshotCreator); ok {
		return creator.CreateForkSnapshot(ctx, sandboxID, name)
	}
	return snapshots.CreateSnapshot(ctx, sandboxID, name)
}

var _ SessionTurnHolder = (*SessionBoundManager)(nil)

// resolveSession resolves (or lazily creates) the remote sandbox bound to
// sessionID. Persistent path only.
func (m *SessionBoundManager) resolveSession(
	ctx context.Context,
	sessionID string,
) (RemoteSandboxHandle, error) {
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return m.resolveScope(ctx, sc)
}

// peekBoundSandboxState reads provider listing for the bound sandbox without
// Connect. The bool is "a binding exists for this provider", not "List
// returned a row": a list miss still reports bound so lookup cannot pretend
// the session has no sandbox. E2B/Cube Connect resumes a paused instance,
// so the lookup-only terminal path must List first.
func (m *SessionBoundManager) peekBoundSandboxState(
	ctx context.Context,
	sessionID string,
) (RemoteSandboxState, bool, error) {
	if m.remoteDisabled() || strings.TrimSpace(sessionID) == "" {
		return "", false, nil
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return "", false, err
	}
	key := sc.machine()
	binding, err := m.bindings.Get(ctx, key)
	if err != nil {
		return "", false, fmt.Errorf("sandbox: read session binding: %w", err)
	}
	if binding == nil || binding.Provider != m.client.Provider() {
		return "", false, nil
	}
	summaries, err := m.client.List(ctx, RemoteListFilter{
		Metadata: m.lifecycle.stateFilter(sc.key),
		States: []RemoteSandboxState{
			RemoteStateRunning,
			RemoteStatePaused,
			RemoteStateTransitioning,
		},
	})
	if err != nil {
		return "", false, fmt.Errorf("sandbox: list session sandbox: %w", err)
	}
	for _, summary := range summaries {
		if summary.ID == binding.SandboxID {
			return summary.State, true, nil
		}
	}
	// Binding exists but the provider list did not return it (lag, metadata
	// mismatch, or a state outside the filter). That is not "no sandbox":
	// the UI should ask before Connect, which would resume a paused VM.
	return RemoteStateUnknown, true, nil
}

// BoundSandboxID returns the ID of the sandbox currently bound to sessionID.
// It never provisions and never Connects: a session with no live binding
// reports ok=false, which callers treat as "nothing to check point".
func (m *SessionBoundManager) BoundSandboxID(
	ctx context.Context, sessionID string,
) (string, bool) {
	if m.remoteDisabled() || strings.TrimSpace(sessionID) == "" {
		return "", false
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return "", false
	}
	binding, err := m.bindings.Get(ctx, sc.machine())
	if err != nil {
		return "", false
	}
	if binding == nil || binding.Provider != m.client.Provider() {
		return "", false
	}
	sandboxID := strings.TrimSpace(binding.SandboxID)
	if sandboxID == "" {
		return "", false
	}
	return sandboxID, true
}

// lookupSessionHandle reads the authoritative binding and, when one exists
// with the current provider, connects to the remote sandbox without
// allocating. Used by artifact / staging paths that must never provision.
func (m *SessionBoundManager) lookupSessionHandle(
	ctx context.Context,
	sessionID string,
) (RemoteSandboxHandle, bool, error) {
	if m.remoteDisabled() || strings.TrimSpace(sessionID) == "" {
		return nil, false, nil
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return nil, false, err
	}
	key := sc.machine()
	// Serialise scope lookups so even concurrent reads connect only once.
	// The scope is owned by one operation, not by this long-lived manager.
	if scope, ok := ctx.Value(sessionFileOperationKey{}).(*sessionFileOperation); ok {
		scope.mu.Lock()
		defer scope.mu.Unlock()
		cacheKey := sessionFileHandleKey{manager: m, session: key}
		if handle := scope.handles[cacheKey]; handle != nil {
			return handle, true, nil
		}
		handle, found, err := m.lookupSessionHandleForKey(ctx, key, "")
		if err == nil && found {
			scope.handles[cacheKey] = handle
		}
		return handle, found, err
	}
	return m.lookupSessionHandleForKey(ctx, key, "")
}

func (m *SessionBoundManager) lookupSessionHandleForKey(
	ctx context.Context, key SessionSandboxKey, expectedID string,
) (RemoteSandboxHandle, bool, error) {
	binding, err := m.bindings.Get(ctx, key)
	if err != nil {
		return nil, false, fmt.Errorf("sandbox: read session binding: %w", err)
	}
	if binding == nil || binding.Provider != m.client.Provider() {
		return nil, false, nil
	}
	if expectedID != "" && binding.SandboxID != expectedID {
		return nil, false, errors.New("sandbox: bound sandbox changed before maintenance command")
	}
	handle, err := m.client.Connect(ctx, RemoteConnectRequest{
		SandboxID:          binding.SandboxID,
		TrafficAccessToken: binding.TrafficAccessToken,
	})
	if err != nil {
		if CanReplaceRemoteBinding(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("sandbox: connect session sandbox: %w", err)
	}
	if handle == nil || handle.ID() != binding.SandboxID ||
		handle.Provider() != m.client.Provider() {
		return nil, false, errors.New("sandbox: remote handle does not match binding")
	}
	persistInboundToken(ctx, m.bindings, key, *binding, handle)
	return handle, true, nil
}

// snapshotOutputFiles does not probe with Stat: a missing output directory is
// an empty baseline, while permission and transport errors remain failures.
func (m *SessionBoundManager) snapshotOutputFiles(
	ctx context.Context, handle RemoteSandboxHandle, dir string,
) ([]RemoteDirEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return m.walkSessionFiles(ctx, handle, dir)
}

func (m *SessionBoundManager) walkSessionFiles(
	ctx context.Context, handle RemoteSandboxHandle, dir string,
) ([]RemoteDirEntry, error) {
	stack := []string{dir}
	var files []RemoteDirEntry
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		entries, err := m.client.ListDir(ctx, handle, cur)
		if err != nil {
			// Only a missing root is an empty listing. A vanished subdirectory
			// makes this snapshot incomplete and must not publish partial links.
			if cur == dir && IsRemoteNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		for _, entry := range entries {
			if entry.Path == "" {
				entry.Path = path.Join(cur, entry.Name)
			}
			if entry.Type == RemoteEntryDir {
				stack = append(stack, entry.Path)
				continue
			}
			if entry.Type == RemoteEntryFile {
				files = append(files, entry)
			}
		}
	}
	return files, nil
}

// sessionScope is where one session's state lives.
type sessionScope struct {
	key        SessionSandboxKey
	layout     WorkspaceLayout
	assignment allocation.Assignment
	user       bool
}

func (s sessionScope) machine() SessionSandboxKey { return s.key.Machine() }

func (s sessionScope) sessionKeyOnly() SessionSandboxKey {
	return SessionSandboxKey{TenantID: s.key.TenantID, SessionID: s.key.SessionID}
}

func (s sessionScope) digest(namespace string) string { return s.key.User.Digest(namespace) }

// addressFor returns the session's recorded placement. User-mode rows are
// cached as addresses; session-mode rows and misses are not, because a turn
// may record the placement after this manager was built.
func (m *SessionBoundManager) addressFor(ctx context.Context, sessionID string) (allocation.Assignment, bool, error) {
	if m == nil || m.allocations == nil || strings.TrimSpace(sessionID) == "" {
		return allocation.Assignment{}, false, nil
	}
	if cached, ok := m.addresses.Load(sessionID); ok {
		return cached.(allocation.Assignment), true, nil
	}
	a, ok, err := m.allocations.SessionAllocation(ctx, sessionID)
	if err != nil {
		return allocation.Assignment{}, false, fmt.Errorf("sandbox: read session allocation: %w", err)
	}
	if ok && a.IsUser() {
		m.addresses.Store(sessionID, a)
	}
	return a, ok, nil
}

// scope resolves where sessionID's state lives. Every exported per-session
// method goes through here.
func (m *SessionBoundManager) scope(ctx context.Context, sessionID string) (sessionScope, error) {
	key, err := m.sessionKey(ctx, sessionID)
	if err != nil {
		return sessionScope{}, err
	}
	a, ok, err := m.addressFor(ctx, key.SessionID)
	if err != nil {
		return sessionScope{}, err
	}
	if !ok || !a.IsUser() {
		return sessionScope{key: key, layout: withValidatedSkillOutputDir(RemoteWorkspaceLayout()), assignment: a}, nil
	}
	// Eligibility is not in the assignment (C17); admitUser verifies it
	// from the session row before any provider call.
	if a.Machine.OwnerTenantID != key.TenantID {
		return sessionScope{}, fmt.Errorf("sandbox: session %s belongs to tenant %d, not %d",
			key.SessionID, a.Machine.OwnerTenantID, key.TenantID)
	}
	if NormalizeConfigID(a.ConfigID) != NormalizeConfigID(m.configID) {
		return sessionScope{}, fmt.Errorf("sandbox: session %s is assigned to config %s, not %s",
			key.SessionID, a.ConfigID, m.configID)
	}
	if m.admission == nil {
		return sessionScope{}, errors.New("sandbox: a user-mode session requires the admission registry")
	}
	layout, err := a.Layout()
	if err != nil {
		return sessionScope{}, err
	}
	machine, _ := a.UserMachine()
	return sessionScope{
		key:        SessionSandboxKey{TenantID: key.TenantID, SessionID: key.SessionID, User: machine},
		layout:     layout,
		assignment: a,
		user:       true,
	}, nil
}

type exclusiveSessionKey struct{}

// withExclusiveSession marks ctx as the holder of scope's exclusive window,
// so the holder's own operations skip the session lease it closed.
func withExclusiveSession(ctx context.Context, scope admission.Scope) context.Context {
	return context.WithValue(ctx, exclusiveSessionKey{}, scope)
}

func holdsExclusive(ctx context.Context, scope admission.Scope) bool {
	held, ok := ctx.Value(exclusiveSessionKey{}).(admission.Scope)
	return ok && held == scope
}

// holdWithWait registers member on scope, waiting out a closed scope for at
// most admissionWait.
func (m *SessionBoundManager) holdWithWait(
	ctx context.Context, scope admission.Scope, member string,
) (admission.Lease, error) {
	deadline := time.Now().Add(m.admissionWait)
	for {
		lease, err := m.admission.Hold(ctx, scope, member, admission.DefaultHoldTTL)
		if err == nil {
			return lease, nil
		}
		if !errors.Is(err, admission.ErrClosed) || time.Now().After(deadline) {
			return nil, fmt.Errorf("sandbox: shared machine is busy: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// verifyLive re-reads, from the database, everything that makes the cached
// address usable now (C4, C15): the same live assignment, and a session that
// exists, is not retired, and whose current owner is the assignment's owner.
func (m *SessionBoundManager) verifyLive(ctx context.Context, sc sessionScope) error {
	if err := m.allocations.VerifySessionAllocation(ctx, sc.key.TenantID, sc.key.SessionID, sc.assignment); err != nil {
		if errors.Is(err, ErrSandboxSessionDeleted) || errors.Is(err, ErrUserAllocationRejected) {
			return err
		}
		return fmt.Errorf("sandbox: verify session allocation: %w", err)
	}
	return nil
}

// admitUser registers one operation of a user-mode session (C4): machine
// lease, then session lease, then a fresh validity check. The returned
// context is cancelled when either lease is lost (C3); pass it to every
// provider call of the operation. It adds this operation's member to the
// exemptions already on ctx (a turn's own members), never a member guessed
// from the session id (C6). Session-mode scopes pass through untouched.
func (m *SessionBoundManager) admitUser(
	ctx context.Context, sc sessionScope, kind string,
) (context.Context, func(), error) {
	if !sc.user {
		return ctx, func() {}, nil
	}
	token, err := redislock.NewToken()
	if err != nil {
		return nil, nil, err
	}
	member := admission.SessionMember(sc.key.SessionID, kind, token)
	digest := sc.digest(m.namespace)
	machineLease, err := m.holdWithWait(ctx, admission.MachineScope(digest), member)
	if err != nil {
		return nil, nil, err
	}
	leases := []admission.Lease{machineLease}
	sessionLeaseScope := admission.SessionScope(digest, sc.key.SessionID)
	if !holdsExclusive(ctx, sessionLeaseScope) {
		sessionLease, err := m.holdWithWait(ctx, sessionLeaseScope, member)
		if err != nil {
			machineLease.Release()
			if errors.Is(err, admission.ErrClosed) {
				return nil, nil, fmt.Errorf("%w: %v", ErrSessionWorkspaceBusy, err)
			}
			return nil, nil, err
		}
		leases = append(leases, sessionLease)
	}
	releaseLeases := func() {
		for i := len(leases) - 1; i >= 0; i-- {
			leases[i].Release()
		}
	}
	if err := m.verifyLive(ctx, sc); err != nil {
		releaseLeases()
		return nil, nil, err
	}
	signals := make([]admission.Signal, 0, len(leases))
	for _, l := range leases {
		signals = append(signals, l)
	}
	opCtx, stop := admission.Bind(ctx, signals...)
	// Exempt this operation; a turn's own members arrive through ctx.
	opCtx = withAdmissionExempt(opCtx, member)
	var once sync.Once
	return opCtx, func() {
		once.Do(func() {
			stop()
			releaseLeases()
		})
	}, nil
}

// acquireExclusive closes sessionID's session scope and waits (bounded) for
// its registered operations to leave. The returned context lets this
// caller's own operations run inside the window.
func (m *SessionBoundManager) acquireExclusive(
	ctx context.Context, digest, sessionID string, wait time.Duration,
) (context.Context, admission.Barrier, error) {
	scope := admission.SessionScope(digest, sessionID)
	barrier, err := m.admission.Close(ctx, scope, admission.DefaultHoldTTL)
	if errors.Is(err, admission.ErrClosed) {
		return nil, nil, ErrSessionWorkspaceBusy
	}
	if err != nil {
		return nil, nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		n, err := m.admission.ActiveExcept(ctx, scope)
		if err != nil {
			barrier.Release()
			return nil, nil, err
		}
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			barrier.Release()
			return nil, nil, ErrSessionWorkspaceBusy
		}
		select {
		case <-ctx.Done():
			barrier.Release()
			return nil, nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return withExclusiveSession(ctx, scope), barrier, nil
}

// AcquireSessionExclusive is acquireExclusive for one user-mode session.
// Use the returned context for the exclusive work; it is cancelled with
// admission.ErrLeaseLost if the window is lost. Call the function to end the
// window. Session-mode sessions get ctx back unchanged.
func (m *SessionBoundManager) AcquireSessionExclusive(
	ctx context.Context, sessionID string,
) (context.Context, func(), error) {
	return m.acquireSessionExclusive(ctx, sessionID, m.admissionWait)
}

func (m *SessionBoundManager) acquireSessionExclusive(
	ctx context.Context, sessionID string, wait time.Duration,
) (context.Context, func(), error) {
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}
	if !sc.user {
		return ctx, func() {}, nil
	}
	// C4: the exclusive window is itself a machine member. Rebuild and reclaim
	// only count the machine scope, so a session barrier alone does not stop
	// them deleting the machine under a rewind or a directory delete.
	token, err := redislock.NewToken()
	if err != nil {
		return nil, nil, err
	}
	digest := sc.digest(m.namespace)
	lease, err := m.holdWithWait(ctx, admission.MachineScope(digest),
		admission.SessionMember(sc.key.SessionID, "exclusive", token))
	if err != nil {
		return nil, nil, err
	}
	exCtx, barrier, err := m.acquireExclusive(ctx, digest, sc.key.SessionID, wait)
	if err != nil {
		lease.Release()
		return nil, nil, err
	}
	// The machine lease stays out of the exemption list: this request's own
	// later exec must not rebuild the machine out from under the window.
	bound, stop := admission.Bind(exCtx, lease, barrier)
	var once sync.Once
	return bound, func() {
		once.Do(func() {
			stop()
			lease.Release()
			barrier.Release()
		})
	}, nil
}

// DesktopMachine names the physical machine behind sessionID's desktop and
// the sandbox it currently is. Sessions on one shared machine contend for one
// control slot; a rebuilt machine, or a sandbox of another deployment,
// provider or config, never shares a key. Call it while holding the session's
// operation so the binding cannot change underneath the caller.
func (m *SessionBoundManager) DesktopMachine(ctx context.Context, sessionID string) (string, string, error) {
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return "", "", err
	}
	if !sc.user {
		// A per-session machine rebuilds a stale image on the next resolve.
		// Pinning the current sandbox id would make the desktop's ensure
		// command lookup-only and skip that rebuild.
		return sessionID, "", nil
	}
	binding, err := m.bindings.Get(ctx, sc.machine())
	if err != nil {
		return "", "", err
	}
	if binding == nil || binding.SandboxID == "" || binding.Provider != m.client.Provider() {
		return "", "", ErrNoLiveSessionSandbox
	}
	parts := []string{
		m.namespace,
		string(m.client.Provider()),
		strconv.FormatUint(binding.ConfigTenantID, 10),
		NormalizeConfigID(m.configID),
		binding.SandboxID,
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:]), binding.SandboxID, nil
}

// HoldSessionOperation registers a long-lived operation (desktop relay) of
// sessionID; ctx is cancelled if the lease is lost. A no-op for per-session
// machines.
func (m *SessionBoundManager) HoldSessionOperation(
	ctx context.Context, sessionID, kind string,
) (context.Context, func(), error) {
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}
	return m.admitUser(ctx, sc, kind)
}

// resolveScope resolves (or lazily creates) the scope's machine. Callers
// admit user-mode scopes first; a deleted user-mode session never reaches
// the lifecycle, which never destroys a shared machine for one session.
func (m *SessionBoundManager) resolveScope(ctx context.Context, sc sessionScope) (RemoteSandboxHandle, error) {
	return m.lifecycle.Resolve(ctx, sc.key)
}

// Namespace is the deployment namespace user machine digests use.
func (m *SessionBoundManager) Namespace() string { return m.namespace }

// UserMachineKey is the machine-level key of a shared user machine.
func (m *SessionBoundManager) UserMachineKey(machine allocation.UserMachine) SessionSandboxKey {
	return SessionSandboxKey{TenantID: machine.OwnerTenantID, User: machine}
}

const sessionWorkspaceCleanupTimeout = 60 * time.Second

// ErrMachineAbsenceUnprovable is returned when a user machine has no binding
// and the provider cannot list by metadata, so it may still exist. Directory
// cleanups keep their task instead of treating the machine as gone.
var ErrMachineAbsenceUnprovable = errors.New("sandbox: cannot prove the user machine is gone")

// DeleteSessionWorkspaces removes the work trees and checkpoint repositories
// of retired sessions from their shared machine. It registers on the machine
// first, then takes each session's scope exclusively so work admitted before
// the retirement finishes before its directory goes; a session whose
// operations do not leave in time is skipped and reported as not deleted.
// Every hold is bound into the command's context, so a lost hold stops the
// deletion (C3). A machine whose binding was lost is re-adopted by metadata,
// never created; only a provider that lists nothing proves it is gone (C5).
// Connect resumes a paused machine: a deleted session's files must not
// linger on its disk.
func (m *SessionBoundManager) DeleteSessionWorkspaces(
	ctx context.Context, machine allocation.UserMachine, sessionIDs []string,
) ([]string, error) {
	if err := m.requireRemoteBackend(); err != nil {
		return nil, err
	}
	if m.admission == nil {
		return nil, errors.New("sandbox: user machines require the admission registry")
	}
	if len(sessionIDs) == 0 {
		return nil, nil
	}
	for _, id := range sessionIDs {
		if _, err := UserWorkspaceLayout(id); err != nil {
			return nil, err
		}
	}
	digest := machine.Digest(m.namespace)
	token, err := redislock.NewToken()
	if err != nil {
		return nil, err
	}
	lease, err := m.holdWithWait(ctx, admission.MachineScope(digest),
		admission.SessionMember(sessionIDs[0], "cleanup", token))
	if err != nil {
		return nil, err
	}
	defer lease.Release()

	signals := []admission.Signal{lease}
	var barriers []admission.Barrier
	defer func() {
		for _, b := range barriers {
			b.Release()
		}
	}()
	var ready []string
	for _, id := range sessionIDs {
		_, barrier, err := m.acquireExclusive(ctx, digest, id, m.admissionWait)
		if errors.Is(err, ErrSessionWorkspaceBusy) {
			continue
		}
		if err != nil {
			return nil, err
		}
		barriers = append(barriers, barrier)
		signals = append(signals, barrier)
		ready = append(ready, id)
	}
	if len(ready) == 0 {
		return nil, nil
	}
	guarded, stop := admission.Bind(ctx, signals...)
	defer stop()
	lost := func(err error) error {
		if cause := context.Cause(guarded); errors.Is(cause, admission.ErrLeaseLost) {
			return cause
		}
		return err
	}

	key := m.UserMachineKey(machine)
	handle, found, err := m.lookupSessionHandleForKey(guarded, key, "")
	if err != nil {
		return nil, lost(err)
	}
	if !found {
		handle, found, err = m.lifecycle.recoverWithoutCreating(guarded, key)
		if err != nil {
			return nil, lost(err)
		}
		if !found {
			return ready, nil
		}
	}
	var targets []string
	for _, id := range ready {
		l, err := UserWorkspaceLayout(id)
		if err != nil {
			return nil, err
		}
		targets = append(targets, ShellQuote(l.Root), ShellQuote(l.GitDir))
	}
	// Refuse a symlinked parent instead of following it: an agent on this
	// machine runs as root and may have replaced one.
	script := `set -eu
for p in /workspace /workspace/sessions /var/lib/weknora /var/lib/weknora/sessions; do
  if [ -L "$p" ]; then echo "refusing to clean through symlinked $p" >&2; exit 4; fi
done
for t in ` + strings.Join(targets, " ") + `; do
  if [ -L "$t" ]; then rm -f -- "$t"; else rm -rf -- "$t"; fi
done`
	result, err := m.client.Exec(guarded, handle, RemoteExecRequest{
		Shell: true, Command: script, User: "root", Timeout: sessionWorkspaceCleanupTimeout,
	})
	if err != nil {
		return nil, lost(fmt.Errorf("sandbox: delete session workspaces: %w", err))
	}
	if result == nil || result.ExitCode != 0 || result.Killed {
		detail := "no result"
		if result != nil {
			detail = fmt.Sprintf("exit=%d stderr=%s", result.ExitCode, strings.TrimSpace(result.Stderr))
		}
		return nil, fmt.Errorf("sandbox: delete session workspaces: %s", detail)
	}
	if err := lost(nil); err != nil {
		return nil, err
	}
	// A re-adopted machine whose token could not be recovered is bound only
	// once the directories are actually gone. ensureBinding is a no-op when
	// the binding was already there.
	if err := m.lifecycle.ensureBinding(guarded, key, handle); err != nil {
		return nil, lost(err)
	}
	if err := lost(nil); err != nil {
		return nil, err
	}
	return ready, nil
}

// ReclaimUserMachine deletes a shared user machine; see
// remoteSessionLifecycle.reclaimUserMachine for the safety rules.
func (m *SessionBoundManager) ReclaimUserMachine(
	ctx context.Context, machine allocation.UserMachine, check func(context.Context) (bool, error),
) (bool, error) {
	if err := m.requireRemoteBackend(); err != nil {
		return false, err
	}
	return m.lifecycle.reclaimUserMachine(ctx, m.UserMachineKey(machine), check)
}

// withSessionEnv overlays the session's input/output paths. On a shared
// machine they are reserved: no config or caller value may redirect them.
func withSessionEnv(env map[string]string, layout WorkspaceLayout) map[string]string {
	out := make(map[string]string, len(env)+2)
	for k, v := range env {
		out[k] = v
	}
	out[skillOutputEnvVar] = layout.OutputDir
	out[sessionInputEnvVar] = layout.InputDir
	return out
}

// userScriptDir is where an uploaded skill script lands on a shared machine.
func userScriptDir(layout WorkspaceLayout) string {
	return path.Join(layout.Root, ".weknora", "scripts")
}

// sessionKey resolves the tenant-scoped binding key. Tenant ID comes from the
// request context; empty tenant is treated as a caller error to keep session
// bindings globally addressable in Redis.
//
// It reads the session-owner tenant rather than the ambient request tenant so
// that a shared agent — which runs under the agent owner's workspace so its
// models, KBs and named sandbox configs resolve there — still binds its sandbox
// under the session's own tenant. Session deletion tears the sandbox down from a
// request that knows only that tenant, so any other choice would strand the
// MicroVM. SandboxTenantIDFromContext falls back to the request tenant, which
// is already the session owner on every non-borrowed path.
func (m *SessionBoundManager) sessionKey(
	ctx context.Context,
	sessionID string,
) (SessionSandboxKey, error) {
	tenantID, ok := types.SandboxTenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return SessionSandboxKey{}, errors.New("sandbox: tenant ID missing from context")
	}
	key := SessionSandboxKey{TenantID: tenantID, SessionID: strings.TrimSpace(sessionID)}
	if err := key.Validate(); err != nil {
		return SessionSandboxKey{}, err
	}
	return key, nil
}

func (m *SessionBoundManager) remoteDisabled() bool {
	if m == nil {
		return true
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.closed
}

func (m *SessionBoundManager) requireRemoteBackend() error {
	if m == nil {
		return ErrSandboxDisabled
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return ErrSandboxDisabled
	}
	return nil
}

func cleanSessionInputPath(filePath string) (string, error) {
	return workspace.CleanInputPathIn(RemoteWorkspaceLayout(), filePath)
}

// cleanSessionWorkspaceWritePath normalizes model-authored sandbox writes and
// protects staged attachments. The remote session binding isolates the files.
func cleanSessionWorkspaceWritePath(filePath string) (string, error) {
	return workspace.CleanWriteFileIn(RemoteWorkspaceLayout(), filePath)
}

// cleanSessionWorkDir requires an absolute sandbox-local working directory.
// Ordinary calls may work anywhere; the install/maintenance option retains its
// explicit workspace/skills scope. Neither check changes filesystem privileges.
func cleanSessionWorkDir(workDir string, allowSkillsRoot bool) (string, error) {
	return workspace.CleanWorkDirIn(RemoteWorkspaceLayout(), workDir, allowSkillsRoot)
}

// buildSessionCreateRequest projects Config into a provider-neutral remote
// create request. The metadata block is populated per-session by the
// lifecycle coordinator; env vars propagate as-is.
//
// The provider parameter (derived from RemoteSandboxClient.Provider()) is the
// authoritative source of identity — it selects the correct Config fields so
// Cube and E2B never read each other's templates or TTLs.
func buildSessionCreateRequest(provider RemoteProvider, cfg *Config) (RemoteCreateRequest, error) {
	envVars := withWorkspaceEnvDefaults(cloneMetadata(cfg.EnvVars))

	switch provider {
	case SandboxTypeCube:
		ttl := cfg.CubeSandboxTTL
		if ttl <= 0 {
			ttl = DefaultCubeSandboxTTL
		}
		return RemoteCreateRequest{
			TemplateID:   cfg.CubeTemplate,
			FromSnapshot: cfg.TemplateFromSnapshot,
			EnvVars:      envVars,
			Network:      cfg.Network,
			Timeout: RemoteTimeoutPolicy{
				Mode:       RemoteTimeoutExplicit,
				Value:      ttl,
				Action:     RemoteOnTimeoutPause,
				AutoResume: true,
			},
		}, nil

	case SandboxTypeE2B:
		ttl := cfg.E2BSandboxTTL
		if ttl <= 0 {
			ttl = DefaultE2BSandboxTTL
		}
		return RemoteCreateRequest{
			TemplateID:   cfg.E2BTemplate,
			FromSnapshot: cfg.TemplateFromSnapshot,
			EnvVars:      envVars,
			Network:      cfg.Network,
			Timeout: RemoteTimeoutPolicy{
				Mode:       RemoteTimeoutExplicit,
				Value:      ttl,
				Action:     RemoteOnTimeoutPause,
				AutoResume: true,
			},
		}, nil

	case SandboxTypeDocker:
		ttl := cfg.DockerIdleTTL
		if ttl <= 0 {
			ttl = DefaultDockerIdleTTL
		}
		// Docker can only honour the overall egress switch (see
		// DockerRemoteClient.networkMode); the allow / deny lists are
		// rejected at save time so they cannot arrive here.
		return RemoteCreateRequest{
			TemplateID: cfg.DockerImage,
			EnvVars:    envVars,
			Network:    cfg.Network,
			Timeout: RemoteTimeoutPolicy{
				Mode:  RemoteTimeoutExplicit,
				Value: ttl,
				// Docker's pause keeps the container's memory resident on the
				// host, so pausing an abandoned sandbox would reclaim nothing.
				// Idle containers are deleted; the lifecycle rebinds the
				// session exactly as it does for a provider-reaped sandbox.
				Action:     RemoteOnTimeoutKill,
				AutoResume: false,
			},
		}, nil

	default:
		return RemoteCreateRequest{}, fmt.Errorf(
			"sandbox: unsupported remote provider %q for session create request",
			provider,
		)
	}
}

// effectiveHTTPTimeout returns the HTTP timeout for health probes and API
// calls against the provider's control plane. The provider parameter is
// authoritative: Cube and E2B each read their own timeout field and fall back
// to their own package-level default.
func effectiveHTTPTimeout(provider RemoteProvider, cfg *Config) time.Duration {
	switch provider {
	case SandboxTypeCube:
		if cfg.CubeHTTPTimeout > 0 {
			return cfg.CubeHTTPTimeout
		}
		return DefaultCubeHTTPTimeout
	case SandboxTypeE2B:
		if cfg.E2BHTTPTimeout > 0 {
			return cfg.E2BHTTPTimeout
		}
		return DefaultE2BHTTPTimeout
	case SandboxTypeDocker:
		if cfg.DockerHTTPTimeout > 0 {
			return cfg.DockerHTTPTimeout
		}
		return DefaultDockerHTTPTimeout
	default:
		return DefaultCubeHTTPTimeout
	}
}

var forkCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

const (
	forkCopyTimeout   = 5 * time.Minute
	forkMaxCandidates = 2000
)

// WorkspaceFork is an open fork of a session on a shared machine. It holds
// the machine's and the source session's admission until Release, so the
// machine named by SandboxID is the one every step runs on. Its steps fail
// with admission.ErrLeaseLost once that hold is lost.
type WorkspaceFork interface {
	SandboxID() string
	// Context is cancelled with admission.ErrLeaseLost once the hold is
	// lost. Run every step of the fork with it, database steps included.
	Context() context.Context
	// Copy materializes sha of the source's checkpoint repository as
	// targetSessionID's workspace and reports which of candidates are sha
	// or its ancestors, i.e. which checkpoints exist in the new repository.
	Copy(targetSessionID, sha string, candidates []string) (map[string]bool, error)
	// Remove deletes targetSessionID's work tree and repository.
	Remove(targetSessionID string) error
	Release()
}

type workspaceFork struct {
	m       *SessionBoundManager
	ctx     context.Context
	source  WorkspaceLayout
	handle  RemoteSandboxHandle
	member  string
	release func()
}

// BeginWorkspaceFork opens a fork of sourceSessionID on its shared machine.
// It never creates the machine: a source without one has nothing to copy.
func (m *SessionBoundManager) BeginWorkspaceFork(ctx context.Context, sourceSessionID string) (WorkspaceFork, error) {
	if err := m.requireRemoteBackend(); err != nil {
		return nil, err
	}
	sc, err := m.scope(ctx, sourceSessionID)
	if err != nil {
		return nil, err
	}
	if !sc.user {
		return nil, ErrSharedSandbox
	}
	opCtx, release, err := m.admitUser(ctx, sc, "fork")
	if err != nil {
		// admitUser releases anything it acquired and returns a nil func.
		if release != nil {
			release()
		}
		return nil, err
	}
	handle, found, err := m.lookupSessionHandleForKey(opCtx, sc.machine(), "")
	if err != nil {
		release()
		return nil, err
	}
	if !found {
		release()
		return nil, ErrNoLiveSessionSandbox
	}
	exempt := admissionExemptFrom(opCtx)
	return &workspaceFork{
		m: m, ctx: opCtx, source: sc.layout, handle: handle,
		member: exempt[len(exempt)-1], release: release,
	}, nil
}

func (f *workspaceFork) SandboxID() string        { return f.handle.ID() }
func (f *workspaceFork) Context() context.Context { return f.ctx }
func (f *workspaceFork) Member() string           { return f.member }
func (f *workspaceFork) Release()                 { f.release() }

func (f *workspaceFork) alive() error {
	if err := context.Cause(f.ctx); err != nil {
		return err
	}
	return nil
}

func (f *workspaceFork) run(script string) (*RemoteExecResult, error) {
	if err := f.alive(); err != nil {
		return nil, err
	}
	result, err := f.m.client.Exec(f.ctx, f.handle, RemoteExecRequest{
		Shell: true, Command: script, User: "root", Timeout: forkCopyTimeout,
	})
	if cause := f.alive(); cause != nil {
		return nil, cause
	}
	if err != nil {
		return nil, err
	}
	if result == nil || result.ExitCode != 0 || result.Killed {
		detail := "no result"
		if result != nil {
			detail = fmt.Sprintf("exit=%d stderr=%s", result.ExitCode, strings.TrimSpace(result.Stderr))
		}
		return nil, errors.New(detail)
	}
	return result, nil
}

func (f *workspaceFork) Copy(targetSessionID, sha string, candidates []string) (map[string]bool, error) {
	if !forkCommitPattern.MatchString(sha) {
		return nil, fmt.Errorf("sandbox: invalid fork commit %q", sha)
	}
	target, err := UserWorkspaceLayout(targetSessionID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var quoted []string
	for _, c := range candidates {
		if forkCommitPattern.MatchString(c) && !seen[c] && len(quoted) < forkMaxCandidates {
			seen[c] = true
			quoted = append(quoted, ShellQuote(c))
		}
	}
	script := fmt.Sprintf(`set -eu
SRC_GIT=%s
DST_TREE=%s
DST_GIT=%s
SHA=%s
for p in /workspace /workspace/sessions /var/lib/weknora /var/lib/weknora/sessions; do
  if [ -L "$p" ]; then echo "refusing symlinked $p" >&2; exit 4; fi
done
if [ -e "$DST_TREE" ] || [ -e "$DST_GIT" ]; then echo "fork target exists" >&2; exit 5; fi
mkdir -p /var/lib/weknora/sessions /workspace/sessions
tmp=$(mktemp -d /var/lib/weknora/sessions/.fork.XXXXXX)
trap 'rm -rf -- "$tmp"' EXIT
g() { git -c safe.directory='*' -c core.hooksPath=/dev/null "$@"; }
g --git-dir="$SRC_GIT" cat-file -e "$SHA^{commit}"
if g --git-dir="$SRC_GIT" ls-tree -r "$SHA" | awk '$1=="160000"{f=1} END{exit !f}'; then
  echo "checkpoint contains a gitlink" >&2; exit 6
fi
g init -q --bare "$tmp/repo.git"
g -c uploadpack.allowAnySHA1InWant=true --git-dir="$tmp/repo.git" fetch -q --no-tags "$SRC_GIT" "$SHA:refs/heads/main"
g --git-dir="$tmp/repo.git" symbolic-ref HEAD refs/heads/main
g --git-dir="$tmp/repo.git" config core.bare false
g --git-dir="$tmp/repo.git" config user.email agent@weknora.local
g --git-dir="$tmp/repo.git" config user.name 'WeKnora Agent'
mkdir -p "$tmp/tree"
g --git-dir="$tmp/repo.git" --work-tree="$tmp/tree" checkout -q -f main
mkdir -p "$tmp/tree/input" "$tmp/tree/output"
mv -- "$tmp/repo.git" "$DST_GIT"
mv -- "$tmp/tree" "$DST_TREE"
for c in %s; do
  if g --git-dir="$DST_GIT" merge-base --is-ancestor "$c" "$SHA" 2>/dev/null; then echo "R $c"; fi
done
`, ShellQuote(f.source.GitDir), ShellQuote(target.Root), ShellQuote(target.GitDir), ShellQuote(sha),
		strings.Join(quoted, " "))
	result, err := f.run(script)
	if err != nil {
		return nil, fmt.Errorf("sandbox: copy session workspace: %w", err)
	}
	reachable := map[string]bool{}
	for _, line := range strings.Split(result.Stdout, "\n") {
		if c, ok := strings.CutPrefix(strings.TrimSpace(line), "R "); ok && seen[c] {
			reachable[c] = true
		}
	}
	return reachable, nil
}

func (f *workspaceFork) Remove(targetSessionID string) error {
	target, err := UserWorkspaceLayout(targetSessionID)
	if err != nil {
		return err
	}
	_, err = f.run(fmt.Sprintf(`set -eu
for p in /workspace /workspace/sessions /var/lib/weknora /var/lib/weknora/sessions; do
  if [ -L "$p" ]; then echo "refusing symlinked $p" >&2; exit 4; fi
done
rm -rf -- %s %s`, ShellQuote(target.Root), ShellQuote(target.GitDir)))
	if err != nil {
		return fmt.Errorf("sandbox: remove forked workspace: %w", err)
	}
	return nil
}

var (
	_ SessionCapabilityProvider        = (*SessionBoundManager)(nil)
	_ SessionShellExecutor             = (*SessionBoundManager)(nil)
	_ SessionFileStore                 = (*SessionBoundManager)(nil)
	_ SessionInstallCapabilityProvider = (*SessionBoundManager)(nil)
	_ SessionInstallShellExecutor      = (*SessionBoundManager)(nil)
)

// PermissiveSessionExistenceChecker accepts every session. It is safe in
// deployments where WeKnora's own DestroySession is the only session-delete
// path (single-process memory binding store); the Redis-authoritative
// deployment must inject a real checker consulting the session repository.
type PermissiveSessionExistenceChecker struct{}

// SessionExists always returns true.
func (PermissiveSessionExistenceChecker) SessionExists(
	context.Context, SessionSandboxKey,
) (bool, error) {
	return true, nil
}
