// Package sandbox: session-bound Manager.
//
// SessionBoundManager keeps one persistent remote sandbox per tenant session.
// It delegates all provider-specific work to a RemoteSandboxClient adapter and
// treats the authoritative session→sandbox binding as external state that
// lives in SessionSandboxBindingStore (Redis in production, memory in tests
// and single-process deployments). This makes the manager provider-neutral
// (Cube today, E2B tomorrow) and multi-instance safe: two WeKnora processes
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
//   - When the remote provider's Health probe fails at construction time and
//     config.FallbackEnabled is true, the manager falls back to LocalSandbox.
//     Every session-scoped capability (shell exec, file staging, session
//     filesystem inspection) then refuses to run on the host: those calls
//     require a real remote provider.
//   - Sandboxes are never reaped from inside WeKnora. Idle-timeout / pause /
//     kill is the provider's responsibility (Cube's on_timeout + Cube's
//     sweeper; E2B's built-in TTL). Multi-instance deployments must not race
//     on this decision.
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
// file storage. Generated artifacts must remain under /workspace/output.
const SessionInputRoot = "/workspace/input"

// sessionLifecycleCleanupTimeout bounds the lifecycle coordinator's own
// bookkeeping deletions (loser cleanup, orphan cleanup after session
// disappearance).
const sessionLifecycleCleanupTimeout = 30 * time.Second

// SessionBoundManager is a sandbox.Manager that binds one remote sandbox per
// tenant session. Concrete provider work is delegated to RemoteSandboxClient;
// this type owns validation, fallback, and the mapping between application
// concepts (ExecuteConfig, session-scoped shell/file APIs) and the provider-
// neutral RemoteSandboxClient contract.
type SessionBoundManager struct {
	config    *Config
	validator *ScriptValidator

	client    RemoteSandboxClient
	bindings  SessionSandboxBindingStore
	checker   SessionExistenceChecker
	lifecycle *remoteSessionLifecycle
	ephemeral *RemoteSandbox

	// fallback is used when the remote provider's health probe fails at
	// construction time. Nil when the remote provider is healthy.
	fallback Sandbox

	// activeType is the effective sandbox type callers observe. It equals
	// client.Provider() in the normal path and the fallback sandbox's Type()
	// after Local fallback engages.
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
}

// NewSessionBoundManager wires the manager with an explicit RemoteSandboxClient
// backend, binding store, and session existence checker. Every persistent
// operation flows through these three dependencies; the manager never keeps
// authoritative session→sandbox state locally.
//
// When the client's Health probe fails and config.FallbackEnabled is true,
// the manager transparently falls back to LocalSandbox for ephemeral Execute
// calls. Session-scoped capabilities remain refused in that mode.
func NewSessionBoundManager(deps SessionBoundManagerConfig) (*SessionBoundManager, error) {
	cfg := deps.Config
	if cfg == nil {
		cfg = DefaultConfig()
	}
	if err := ValidateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid sandbox config: %w", err)
	}
	applyCubeDefaults(cfg)
	if deps.Client == nil {
		return nil, errors.New("session bound manager requires a RemoteSandboxClient")
	}
	if deps.Store == nil {
		return nil, errors.New("session bound manager requires a SessionSandboxBindingStore")
	}
	if deps.Checker == nil {
		return nil, errors.New("session bound manager requires a SessionExistenceChecker")
	}

	createRequest := buildSessionCreateRequest(cfg)
	lifecycle, err := newRemoteSessionLifecycle(
		deps.Client,
		deps.Store,
		deps.Checker,
		createRequest,
		sessionLifecycleCleanupTimeout,
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
		activeType: deps.Client.Provider(),
	}

	probeCtx, cancel := context.WithTimeout(context.Background(), effectiveHTTPTimeout(cfg))
	defer cancel()
	if err := deps.Client.Health(probeCtx); err != nil {
		if !cfg.FallbackEnabled {
			return nil, fmt.Errorf("remote sandbox provider unavailable: %w", err)
		}
		log.Printf("[sandbox] remote provider %s unhealthy (%v); falling back to local sandbox",
			deps.Client.Provider(), err)
		m.fallback = NewLocalSandbox(cfg)
		m.activeType = m.fallback.Type()
	}
	return m, nil
}

// GetType reports the current effective sandbox type. Returns the fallback
// type after Local fallback engages.
func (m *SessionBoundManager) GetType() SandboxType {
	if m == nil {
		return SandboxTypeDisabled
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeType
}

// GetSandbox exposes a diagnostic Sandbox for callers that need to inspect
// availability. Returns the fallback when engaged, otherwise a stateless
// RemoteSandbox surface for the current provider.
func (m *SessionBoundManager) GetSandbox() Sandbox {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.fallback != nil {
		return m.fallback
	}
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
	fallback := m.fallback
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

	if fallback != nil {
		return fallback.Execute(ctx, cfg)
	}
	if strings.TrimSpace(cfg.SessionID) == "" {
		return m.ephemeral.Execute(ctx, cfg)
	}

	handle, err := m.resolveSession(ctx, cfg.SessionID)
	if err != nil {
		return nil, err
	}
	return m.ephemeral.ExecuteOnHandle(ctx, handle, cfg)
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
	if err := m.client.MakeDir(ctx, handle, dir); err != nil {
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
	if err := m.client.MakeDir(ctx, handle, path.Dir(clean)); err != nil {
		return fmt.Errorf("sandbox: create input directory: %w", err)
	}
	if err := m.client.WriteFile(ctx, handle, clean, content); err != nil {
		return fmt.Errorf("sandbox: write session input %s: %w", clean, err)
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

// ExecShellCommand runs a shell one-liner inside the session's persistent
// sandbox. Fallback is explicitly refused so shell_exec never escapes onto
// the host machine.
func (m *SessionBoundManager) ExecShellCommand(
	ctx context.Context,
	sessionID string,
	command string,
	workDir string,
	timeout time.Duration,
	env map[string]string,
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
	if timeout <= 0 {
		timeout = m.config.DefaultTimeout
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	handle, err := m.resolveSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if workDir != "" {
		if mkErr := m.client.MakeDir(ctx, handle, workDir); mkErr != nil {
			log.Printf("[sandbox] shell_exec: MakeDir %s failed (continuing): %v", workDir, mkErr)
		}
	}

	start := time.Now()
	execResult, execErr := m.client.Exec(ctx, handle, RemoteExecRequest{
		Command: command,
		Shell:   true,
		Env:     env,
		WorkDir: workDir,
		Timeout: timeout,
	})
	duration := time.Since(start)
	return remoteExecuteResult(execResult, execErr, duration), nil
}

// SessionShellExecutor advertises the shell-execution capability while a
// real remote backend is active. Returns nil after Local fallback engages so
// the tool layer refuses to run shell commands on the host machine.
func (m *SessionBoundManager) SessionShellExecutor() SessionShellExecutor {
	if m == nil || m.remoteDisabled() {
		return nil
	}
	return m
}

// SessionFileStore advertises the session-scoped filesystem capability while
// a real remote backend is active. Returns nil after Local fallback engages.
func (m *SessionBoundManager) SessionFileStore() SessionFileStore {
	if m == nil || m.remoteDisabled() {
		return nil
	}
	return m
}

// Cleanup marks the manager closed. Session sandboxes are not force-deleted
// here: their lifecycle is authoritative in the binding store and would
// leak to any other WeKnora replica if this replica reaped them on shutdown.
// Providers reclaim idle sandboxes via their own timeout/pause policies.
func (m *SessionBoundManager) Cleanup(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	fallback := m.fallback
	m.mu.Unlock()

	if fallback != nil {
		return fallback.Cleanup(ctx)
	}
	return nil
}

// --- internal helpers --------------------------------------------------------

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
func (m *SessionBoundManager) sessionKey(
	ctx context.Context,
	sessionID string,
) (SessionSandboxKey, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
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
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.fallback != nil || m.closed
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
	if m.fallback != nil {
		return fmt.Errorf(
			"sandbox: remote-only capability requires the remote provider (current mode: %s)",
			m.fallback.Type(),
		)
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

// buildSessionCreateRequest projects Config into a provider-neutral remote
// create request. The metadata block is populated per-session by the
// lifecycle coordinator; env vars propagate as-is.
func buildSessionCreateRequest(cfg *Config) RemoteCreateRequest {
	ttl := cfg.CubeSandboxTTL
	if ttl <= 0 {
		ttl = DefaultCubeSandboxTTL
	}
	return RemoteCreateRequest{
		TemplateID: cfg.CubeTemplate,
		Timeout: RemoteTimeoutPolicy{
			Mode:   RemoteTimeoutExplicit,
			Value:  ttl,
			Action: RemoteOnTimeoutKill,
		},
	}
}

func effectiveHTTPTimeout(cfg *Config) time.Duration {
	if cfg.CubeHTTPTimeout > 0 {
		return cfg.CubeHTTPTimeout
	}
	return DefaultCubeHTTPTimeout
}

var (
	_ SessionCapabilityProvider = (*SessionBoundManager)(nil)
	_ SessionShellExecutor      = (*SessionBoundManager)(nil)
	_ SessionFileStore          = (*SessionBoundManager)(nil)
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
