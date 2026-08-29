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
	"errors"
	"fmt"
	"log"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// SessionInputRoot is reserved for durable user attachments restored from
// file storage. Generated artifacts must remain under SessionOutputRoot.
const SessionInputRoot = "/workspace/input"

// SessionOutputRoot is where skill scripts write artifacts for collection.
// The skills manager injects this path via skillOutputEnvVar; Execute
// materialises the directory via envd before the script runs so scripts
// do not depend on the template user being able to mkdir under /workspace.
const SessionOutputRoot = "/workspace/output"

// skillOutputEnvVar matches the skills manager's WEKNORA_SKILL_OUTPUT_DIR.
const skillOutputEnvVar = "WEKNORA_SKILL_OUTPUT_DIR"

// sessionInputEnvVar matches the skills manager's WEKNORA_SESSION_INPUT_DIR.
// Both names are injected into the sandbox environment itself, not only into
// the skill-script Execute call, so an agent exploring with shell_exec reads
// the same paths the skills framework uses.
const sessionInputEnvVar = "WEKNORA_SESSION_INPUT_DIR"

// SessionWorkspaceRoot is the writable workspace root inside remote sandboxes.
// shell_exec work_dir must stay underneath this path.
const SessionWorkspaceRoot = "/workspace"

// sessionArtifactDirBootstrapTimeout bounds the root-owned setup step that
// grants DefaultSandboxExecUser write access to the artifact directory.
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

	// mu guards Cleanup's idempotency flag.
	mu     sync.RWMutex
	closed bool
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

	// SkipHealthProbe skips the construction-time Health() round-trip.
	// Set by the per-tenant resolver, which builds a manager per request.
	// See NewSessionBoundManager.
	SkipHealthProbe bool
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

	lifecycle, err := newRemoteSessionLifecycle(
		deps.Client,
		deps.Store,
		deps.Checker,
		createRequest,
		sessionLifecycleCleanupTimeout,
		deps.ConfigID,
	)
	if err != nil {
		return nil, fmt.Errorf("session bound manager: %w", err)
	}

	m := &SessionBoundManager{
		config:     cfg,
		validator:  NewScriptValidator(),
		client:     deps.Client,
		bindings:   deps.Store,
		checker:    deps.Checker,
		lifecycle:  lifecycle,
		ephemeral:  NewRemoteSandbox(deps.Client, createRequest),
		activeType: provider,
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

	handle, err := m.resolveSession(ctx, cfg.SessionID)
	if err != nil {
		return nil, err
	}
	m.ensureSessionWorkspaceDirs(ctx, handle, executionOutputDir(cfg))
	return m.ephemeral.ExecuteOnHandle(ctx, handle, cfg)
}

// ensureSessionWorkspaceDirs materialises the input and artifact directories
// and makes sure DefaultSandboxExecUser can write to them. It runs before
// every operation rather than only before script execution: a snapshot-derived
// image has no /workspace tree at all (skill install wipes it before the
// snapshot), an agent that explores with shell_exec first would otherwise find
// neither directory, and a later `rm -rf` of those paths must be repaired
// rather than left missing until the process restarts.
//
// This runs AS the sandbox account, never as root. The directories sit inside
// the session's own writable workspace, and chown/chmod follow symlinks, so a
// root-run bootstrap can be aimed at any directory in the container: a session
// that swaps its artifact directory for a link to /etc gets handed ownership of
// /etc, and from there uid 0 by rewriting passwd. Running as the sandbox
// account makes that a no-op — the kernel refuses everything the account does
// not already own.
//
// The command is a no-op when the directories already exist and are writable,
// so repeating it is cheap relative to recreating a missing tree. Best-effort:
// failures are logged and do not abort the upcoming operation.
func (m *SessionBoundManager) ensureSessionWorkspaceDirs(
	ctx context.Context,
	handle RemoteSandboxHandle,
	outputDir string,
) {
	if m == nil || m.client == nil || handle == nil || outputDir == "" {
		return
	}
	execUser := DefaultSandboxExecUser
	result, err := m.client.Exec(ctx, handle, RemoteExecRequest{
		Shell:   true,
		Command: workspaceBootstrapCommand(SessionInputRoot, outputDir),
		User:    execUser,
		Timeout: sessionArtifactDirBootstrapTimeout,
	})
	switch {
	case err != nil:
		log.Printf(
			"[sandbox] prepare workspace dirs (%s, %s) for %s failed: %v",
			SessionInputRoot, outputDir, execUser, err,
		)
		return
	case result != nil && result.ExitCode != 0:
		// The account cannot create or take over these directories, which
		// means /workspace itself does not belong to it. No runtime step can
		// repair that; the image or template has to be rebuilt from a base
		// where /workspace is owned by the sandbox account.
		log.Printf(
			"[sandbox] prepare workspace dirs (%s, %s) for %s: exit=%d stderr=%s "+
				"— rebuild the sandbox image/template so /workspace is owned by %s",
			SessionInputRoot, outputDir, execUser,
			result.ExitCode, strings.TrimSpace(result.Stderr), execUser,
		)
	}
}

// workspaceBootstrapCommand builds the repair script the sandbox account runs.
//
// Each directory is handled in three steps because all three states occur in
// the field: missing (snapshot images carry no /workspace tree), present but
// not a directory (a symlink an earlier turn left behind), and present but
// owned by another account (a provider whose filesystem API creates
// directories as root). The last one is repaired by moving the directory aside
// instead of chowning it: deletion rights come from the parent, so the account
// that owns /workspace can always replace a child it does not own, and nothing
// here needs privileges.
func workspaceBootstrapCommand(dirs ...string) string {
	quoted := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		quoted = append(quoted, ShellQuote(dir))
	}
	return fmt.Sprintf(
		`set -e; for d in %s; do `+
			`if [ -d "$d" ] && [ -w "$d" ] && [ ! -L "$d" ]; then continue; fi; `+
			`if [ -L "$d" ] || { [ -e "$d" ] && [ ! -d "$d" ]; }; then rm -f "$d"; fi; `+
			`[ -d "$d" ] || mkdir -p "$d"; `+
			`if [ ! -w "$d" ]; then mv -f "$d" "$d.unwritable.$(date +%%s)"; mkdir "$d"; fi; `+
			`chmod 775 "$d"; done`,
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
			if clean, err := cleanSessionWorkDir(dir, false); err == nil {
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
	key, err := m.sessionKey(ctx, sessionID)
	if err != nil {
		return err
	}
	return m.lifecycle.Destroy(ctx, key)
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
	return m.bindings.InvalidateByConfig(ctx, tenantID, configID)
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
	handle, err := m.resolveSession(ctx, sessionID)
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
	clean, err := cleanSessionInputPath(filePath)
	if err != nil {
		return err
	}
	handle, err := m.resolveSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if err := ignoreExistingDir(m.client.MakeDir(ctx, handle, path.Dir(clean))); err != nil {
		return fmt.Errorf("sandbox: create input directory: %w", err)
	}
	if err := m.client.WriteFile(ctx, handle, clean, content); err != nil {
		return fmt.Errorf("sandbox: write session input %s: %w", clean, err)
	}
	return nil
}

// WriteSessionWorkspaceFile writes a model-authored file into the session's
// remote sandbox, provisioning the sandbox on first call. Paths must sit
// under /workspace and must not land in /workspace/input.
func (m *SessionBoundManager) WriteSessionWorkspaceFile(
	ctx context.Context, sessionID, filePath string, content []byte,
) error {
	if err := m.requireRemoteBackend(); err != nil {
		return err
	}
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("sandbox: session ID required for workspace write")
	}
	clean, err := cleanSessionWorkspaceWritePath(filePath)
	if err != nil {
		return err
	}
	handle, err := m.resolveSession(ctx, sessionID)
	if err != nil {
		return err
	}
	m.ensureSessionWorkspaceDirs(ctx, handle, SessionOutputRoot)
	if err := ignoreExistingDir(m.client.MakeDir(ctx, handle, path.Dir(clean))); err != nil {
		return fmt.Errorf("sandbox: create workspace directory: %w", err)
	}
	if err := m.client.WriteFile(ctx, handle, clean, content); err != nil {
		return fmt.Errorf("sandbox: write session file %s: %w", clean, err)
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
	clean, err := cleanSessionInputPath(targetPath)
	if err != nil {
		return err
	}
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
	handle, ok, err := m.lookupSessionHandle(ctx, sessionID)
	if err != nil || !ok {
		return nil, err
	}
	return m.listFilesRecursive(ctx, handle, dir)
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
	clean := path.Clean(strings.TrimSpace(filePath))
	if clean != SkillsImageRoot && !strings.HasPrefix(clean, SkillsImageRoot+"/") {
		return fmt.Errorf("sandbox: install file path %q is outside %s", filePath, SkillsImageRoot)
	}
	handle, err := m.resolveSession(ctx, sessionID)
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
// flags are explicit so skill image maintenance can write under /opt without
// loosening work_dir or user privileges for ordinary chat sessions.
type ShellExecOptions struct {
	WorkDir string
	Timeout time.Duration
	Env     map[string]string

	// AllowSkillsRoot lets installer calls work inside the skills image root.
	// See cleanSessionWorkDir for why the work_dir allowlist is lexical only.
	// Never set this from a model-authored tool such as shell_exec.
	AllowSkillsRoot bool
	// AsRoot is reserved for install/maintenance commands that need to write
	// outside /workspace; ordinary sessions must keep the provider default user.
	// Never set this from a model-authored tool such as shell_exec: root inside
	// the sandbox bypasses file-mode isolation on the image.
	AsRoot bool
}

// ExecShellCommand runs a shell one-liner inside the session's persistent
// sandbox. It preserves the shell_exec tool contract: /workspace-only work_dir
// validation and provider default user.
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
	if err := m.requireRemoteBackend(); err != nil {
		return nil, fmt.Errorf(
			"sandbox: shell_exec requires the remote sandbox provider (current mode: %s)",
			m.GetType(),
		)
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("sandbox: session_id required for ExecShellCommand")
	}
	if strings.TrimSpace(command) == "" {
		return nil, errors.New("sandbox: command required for ExecShellCommand")
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = m.config.DefaultTimeout
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	workDir := strings.TrimSpace(opts.WorkDir)
	if workDir != "" {
		cleanWorkDir, err := cleanSessionWorkDir(workDir, opts.AllowSkillsRoot)
		if err != nil {
			return nil, err
		}
		workDir = cleanWorkDir
	}

	handle, err := m.resolveSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	// The model reaches the sandbox through this path too, and typically
	// before any skill script runs. Without the same bootstrap it would find
	// no artifact directory and no place to read attachments from, and would
	// improvise somewhere the collector never looks.
	m.ensureSessionWorkspaceDirs(ctx, handle, SessionOutputRoot)
	if workDir != "" {
		if mkErr := m.client.MakeDir(ctx, handle, workDir); mkErr != nil {
			log.Printf("[sandbox] shell_exec: MakeDir %s failed (continuing): %v", workDir, mkErr)
		}
	}

	// Named explicitly rather than left to each adapter's default. This command
	// line comes from the model, so it is the one exec path an injected prompt
	// reaches directly, and the account it runs as must not depend on which
	// backend the workspace happens to have selected. Only a caller inside the
	// server may ask for root, and only image maintenance does.
	user := DefaultSandboxExecUser
	if opts.AsRoot {
		user = "root"
	}

	start := time.Now()
	execResult, execErr := m.client.Exec(ctx, handle, RemoteExecRequest{
		Command: command,
		Shell:   true,
		Env:     opts.Env,
		WorkDir: workDir,
		User:    user,
		Timeout: timeout,
	})
	duration := time.Since(start)
	return remoteExecuteResult(execResult, execErr, duration), nil
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

// BeginSessionTurn opens the chat-turn lease for sessionID. The first
// resolve after this may rebuild a stale image; later resolves of the same
// turn keep the sandbox.
func (m *SessionBoundManager) BeginSessionTurn(ctx context.Context, sessionID string) error {
	if m == nil {
		return nil
	}
	leaser, ok := m.bindings.(sessionTurnLeaseStore)
	if !ok {
		return nil
	}
	key, err := m.sessionKey(ctx, sessionID)
	if err != nil {
		return err
	}
	return leaser.BeginTurn(ctx, key)
}

// EndSessionTurn closes the chat-turn lease. It ignores request cancellation
// so a disconnected client still releases the lease.
func (m *SessionBoundManager) EndSessionTurn(ctx context.Context, sessionID string) error {
	if m == nil {
		return nil
	}
	leaser, ok := m.bindings.(sessionTurnLeaseStore)
	if !ok {
		return nil
	}
	key, err := m.sessionKey(ctx, sessionID)
	if err != nil {
		return err
	}
	return leaser.EndTurn(context.WithoutCancel(ctx), key)
}

var _ SessionTurnHolder = (*SessionBoundManager)(nil)

// resolveSession resolves (or lazily creates) the remote sandbox bound to
// sessionID. Persistent path only.
func (m *SessionBoundManager) resolveSession(
	ctx context.Context,
	sessionID string,
) (RemoteSandboxHandle, error) {
	key, err := m.sessionKey(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return m.lifecycle.Resolve(ctx, key)
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
	key, err := m.sessionKey(ctx, sessionID)
	if err != nil {
		return nil, false, err
	}
	binding, err := m.bindings.Get(ctx, key)
	if err != nil {
		return nil, false, fmt.Errorf("sandbox: read session binding: %w", err)
	}
	if binding == nil || binding.Provider != m.client.Provider() {
		return nil, false, nil
	}
	handle, err := m.client.Connect(ctx, binding.SandboxID)
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
	return handle, true, nil
}

func (m *SessionBoundManager) listFilesRecursive(
	ctx context.Context,
	handle RemoteSandboxHandle,
	dir string,
) ([]RemoteDirEntry, error) {
	stat, err := m.client.Stat(ctx, handle, dir)
	if err != nil {
		if IsRemoteNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("sandbox: stat %s: %w", dir, err)
	}
	if stat == nil {
		return nil, nil
	}

	stack := []string{dir}
	var files []RemoteDirEntry
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		entries, err := m.client.ListDir(ctx, handle, cur)
		if err != nil {
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
	clean := path.Clean(strings.TrimSpace(filePath))
	if clean == SessionInputRoot || strings.HasPrefix(clean, SessionInputRoot+"/") {
		return clean, nil
	}
	return "", fmt.Errorf(
		"sandbox: session input path %q is outside %s",
		filePath, SessionInputRoot,
	)
}

// cleanSessionWorkspaceWritePath keeps model-authored writes inside the
// session workspace and out of the attachment tree. Validation is lexical
// (path.Clean plus prefix checks), matching cleanSessionWorkDir.
func cleanSessionWorkspaceWritePath(filePath string) (string, error) {
	clean := path.Clean(strings.TrimSpace(filePath))
	if !path.IsAbs(clean) || clean == "." || clean == "/" {
		return "", fmt.Errorf("sandbox: workspace write path %q must be an absolute file path", filePath)
	}
	if clean == SessionWorkspaceRoot || clean == SessionOutputRoot || clean == SessionInputRoot {
		return "", fmt.Errorf("sandbox: workspace write path %q is a directory, not a file", filePath)
	}
	if !strings.HasPrefix(clean, SessionWorkspaceRoot+"/") {
		return "", fmt.Errorf("sandbox: workspace write path %q is outside %s", filePath, SessionWorkspaceRoot)
	}
	if strings.HasPrefix(clean, SessionInputRoot+"/") {
		return "", fmt.Errorf("sandbox: session input %s is read-only", SessionInputRoot)
	}
	return clean, nil
}

// cleanSessionWorkDir keeps shell_exec inside directories we are willing to let
// an agent work in. Ordinary sessions get /workspace only.
//
// Validation is lexical (path.Clean plus prefix checks): a symlink under an
// allowed root that resolves elsewhere at execution time is not detected and
// that is intentional. The only caller that passes allowSkillsRoot also passes
// AsRoot and runs arbitrary install shell commands, so a symlink would grant
// nothing those commands cannot already reach via cd or absolute paths. For
// ordinary sessions the allowlist is unchanged and its lexical nature is
// pre-existing. The allowlist stops casual wandering and makes intent
// auditable; the real isolation boundary is the remote sandbox itself.
//
// allowSkillsRoot widens it to the skills image root for install/maintenance
// sessions, so the installer agent can set work_dir to the skill directory and
// run ordinary relative commands instead of composing long absolute paths. It
// is a widening, not a removal: everything outside these two roots is still
// refused.
func cleanSessionWorkDir(workDir string, allowSkillsRoot bool) (string, error) {
	clean := path.Clean(strings.TrimSpace(workDir))
	if clean == SessionWorkspaceRoot || strings.HasPrefix(clean, SessionWorkspaceRoot+"/") {
		return clean, nil
	}
	if allowSkillsRoot &&
		(clean == SkillsImageRoot || strings.HasPrefix(clean, SkillsImageRoot+"/")) {
		return clean, nil
	}
	allowed := SessionWorkspaceRoot
	if allowSkillsRoot {
		allowed = SessionWorkspaceRoot + ", " + SkillsImageRoot
	}
	return "", fmt.Errorf(
		"sandbox: work dir %q is outside allowed roots (%s)",
		workDir, allowed,
	)
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
			TemplateID: cfg.CubeTemplate,
			EnvVars:    envVars,
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
			TemplateID: cfg.E2BTemplate,
			EnvVars:    envVars,
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
		return RemoteCreateRequest{
			TemplateID: cfg.DockerImage,
			EnvVars:    envVars,
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
