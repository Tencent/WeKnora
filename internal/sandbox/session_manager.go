package sandbox

import (
	"context"
	"fmt"
	"log"
	"path"
	"strings"
	"sync"
	"time"
)

// SessionBoundManager is a sandbox.Manager implementation that keeps one
// persistent CubeSandbox instance per SessionID. It gives the caller
// state-preserving script execution semantics: pip installs, files, and
// long-running services set up during one Execute call are still visible
// in the next call from the same session.
//
// Design notes:
//
//   - Sandboxes are created lazily on the first Execute for a given
//     SessionID, so purely-conversational sessions never allocate a MicroVM.
//   - Executions from a request without a SessionID (e.g. an ephemeral tool
//     invocation outside the chat pipeline) fall back to an ephemeral
//     CubeSandbox, giving stateless-per-call semantics for backwards
//     compatibility with DockerSandbox / LocalSandbox callers.
//   - The reaper goroutine periodically walks the map and kills sandboxes
//     that have been idle for longer than CubeIdleTTL * idleReapMultiplier.
//   - When the Cube API is unreachable at construction time, we transparently
//     fall back to LocalSandbox if config.FallbackEnabled is true; otherwise
//     the constructor returns an error so callers can decide how to react.
//
// SessionBoundManager satisfies the Manager interface, so it can be dropped
// into any place that previously held a *DefaultManager. Callers that want to
// tear down a specific session's sandbox (e.g. sessionService.DeleteSession)
// can type-assert to the exported DestroySession method.
type SessionBoundManager struct {
	config    *Config
	validator *ScriptValidator
	client    *cubeClient

	// sandboxes maps SessionID -> *CubeSandbox.
	sandboxes sync.Map

	// fallback is used when the Cube API is unreachable. Nil when disabled.
	fallback Sandbox

	// mu guards state that has to change atomically (fallback + closed).
	mu     sync.RWMutex
	closed bool

	// reaper lifecycle
	reaperCancel context.CancelFunc
	reaperWG     sync.WaitGroup
}

// idleReapMultiplier controls when idle sandboxes transition from "idle" to
// "kill". A sandbox idle for CubeIdleTTL * idleReapMultiplier is torn down.
const idleReapMultiplier = 3

// SessionInputRoot is reserved for durable user attachments restored from file
// storage. Generated artifacts must remain under /workspace/output.
const SessionInputRoot = "/workspace/input"

// NewSessionBoundManager wires up a SessionBoundManager from Config.
// It performs a health probe against CubeAPI so callers can detect fatal
// mis-configuration up-front instead of at first Execute.
func NewSessionBoundManager(config *Config) (*SessionBoundManager, error) {
	if config == nil {
		config = DefaultConfig()
	}
	config.Type = SandboxTypeCube
	if err := ValidateConfig(config); err != nil {
		return nil, fmt.Errorf("invalid sandbox config: %w", err)
	}
	applyCubeDefaults(config)

	m := &SessionBoundManager{
		config:    config,
		validator: NewScriptValidator(),
		client:    newCubeClient(config),
	}

	// Probe once so the caller can log a friendly diagnostic. We swallow the
	// error and let the fallback path handle it — the Cube deployment might
	// come online after WeKnora starts.
	probeCtx, cancel := context.WithTimeout(context.Background(), config.CubeHTTPTimeout)
	defer cancel()
	if err := m.client.Health(probeCtx); err != nil {
		if !config.FallbackEnabled {
			return nil, fmt.Errorf("cube sandbox unavailable: %w", err)
		}
		log.Printf("[sandbox] cube api unreachable at %s (%v); falling back to local sandbox", config.CubeAPIURL, err)
		m.fallback = NewLocalSandbox(config)
	}

	m.startReaper()
	return m, nil
}

// Type reports the current effective sandbox type. When the Cube API is
// unreachable and the fallback engaged, this returns SandboxTypeLocal.
func (m *SessionBoundManager) GetType() SandboxType {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.fallback != nil {
		return m.fallback.Type()
	}
	return SandboxTypeCube
}

// GetSandbox returns a representative sandbox for callers that need to
// inspect availability. It intentionally does not create one on demand —
// SessionBoundManager binds sandboxes to session IDs, not to the manager
// itself.
func (m *SessionBoundManager) GetSandbox() Sandbox {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.fallback != nil {
		return m.fallback
	}
	// Return the first live sandbox we can find. Callers only use this for
	// diagnostics (Type / IsAvailable).
	var picked Sandbox
	m.sandboxes.Range(func(_, v any) bool {
		picked = v.(*CubeSandbox)
		return false
	})
	return picked
}

// Execute runs config against the per-SessionID sandbox (or an ephemeral one
// if no SessionID is provided). All the shared security validation runs
// first, matching DefaultManager's behaviour.
func (m *SessionBoundManager) Execute(ctx context.Context, config *ExecuteConfig) (*ExecuteResult, error) {
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

	// Security validation is applied regardless of backend.
	if !config.SkipValidation {
		if err := runScriptValidation(m.validator, config); err != nil {
			log.Printf("[sandbox] Security validation failed: %v", err)
			return &ExecuteResult{
				ExitCode: -1,
				Error:    err.Error(),
				Stderr:   fmt.Sprintf("Security validation failed: %v", err),
			}, ErrSecurityViolation
		}
	}

	// If the Cube API was unavailable at construction time, honour the
	// configured fallback so scripting keeps working under partial outages.
	if fallback != nil {
		return fallback.Execute(ctx, config)
	}

	sandbox, err := m.resolveSandbox(ctx, config.SessionID)
	if err != nil {
		return nil, err
	}
	return sandbox.Execute(ctx, config)
}

// resolveSandbox returns the CubeSandbox bound to sessionID, creating one
// lazily. When sessionID is empty, a fresh ephemeral sandbox is returned so
// each call is independent.
func (m *SessionBoundManager) resolveSandbox(_ context.Context, sessionID string) (*CubeSandbox, error) {
	if sessionID == "" {
		return newPersistentCubeSandbox(m.config, m.client).withEphemeral(true), nil
	}
	if existing, ok := m.sandboxes.Load(sessionID); ok {
		return existing.(*CubeSandbox), nil
	}
	sb := newPersistentCubeSandbox(m.config, m.client)
	actual, loaded := m.sandboxes.LoadOrStore(sessionID, sb)
	if loaded {
		return actual.(*CubeSandbox), nil
	}
	return sb, nil
}

// DestroySession tears down the sandbox bound to sessionID, if any.
// The sessionService.DeleteSession hook calls this so orphaned MicroVMs get
// cleaned up promptly on user-initiated session deletion.
func (m *SessionBoundManager) DestroySession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	v, ok := m.sandboxes.LoadAndDelete(sessionID)
	if !ok {
		return nil
	}
	return v.(*CubeSandbox).Cleanup(ctx)
}

// liveSandboxInfoForSession returns the SandboxInfo of the sandbox already
// bound to sessionID, without creating one lazily. It reports ok=false when
// no sandbox has been created for this session yet, or the bound sandbox has
// not yet materialised on the Cube side (i.e. no Execute call has landed
// against it). This is the safety knob that keeps ArtifactCollector from
// spinning up MicroVMs for chat-only sessions.
func (m *SessionBoundManager) liveSandboxInfoForSession(sessionID string) (*SandboxInfo, bool) {
	if m == nil || sessionID == "" {
		return nil, false
	}
	v, ok := m.sandboxes.Load(sessionID)
	if !ok {
		return nil, false
	}
	sb := v.(*CubeSandbox)
	sb.mu.Lock()
	info := sb.info
	sb.mu.Unlock()
	if info == nil || info.ID == "" {
		return nil, false
	}
	return info, true
}

// EnsureSessionDir creates (or no-ops on existing) a directory inside the
// session's live sandbox. Callers use this to guarantee $WEKNORA_SKILL_OUTPUT_DIR
// exists before the skill script tries to write to it. Returns nil silently
// when no sandbox is bound yet — the directory will be provisioned by the
// upcoming CubeSandbox.Execute path via the sandbox base image.
func (m *SessionBoundManager) EnsureSessionDir(ctx context.Context, sessionID, dir string) error {
	if dir == "" {
		return nil
	}
	info, ok := m.liveSandboxInfoForSession(sessionID)
	if !ok {
		return nil
	}
	return m.client.MakeDir(ctx, info, dir)
}

// WriteSessionInputFile writes a server-selected attachment path into the
// session's persistent Cube sandbox, provisioning it on first use.
func (m *SessionBoundManager) WriteSessionInputFile(
	ctx context.Context, sessionID, filePath string, content []byte,
) error {
	if m == nil || m.GetType() != SandboxTypeCube {
		return ErrSandboxDisabled
	}
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("sandbox: session ID required for input staging")
	}
	clean, err := cleanSessionInputPath(filePath)
	if err != nil {
		return err
	}
	sb, err := m.resolveSandbox(ctx, sessionID)
	if err != nil {
		return err
	}
	info, err := sb.ensureSandbox(ctx)
	if err != nil {
		return err
	}
	if err := m.client.MakeDir(ctx, info, path.Dir(clean)); err != nil {
		return fmt.Errorf("sandbox: create input directory: %w", err)
	}
	if err := m.client.WriteFile(ctx, info, clean, content); err != nil {
		return fmt.Errorf("sandbox: write session input %s: %w", clean, err)
	}
	sb.mu.Lock()
	sb.lastUsed = time.Now()
	sb.mu.Unlock()
	return nil
}

// RemoveSessionInputPath removes a file or directory under SessionInputRoot.
// It is a no-op when the session has no live sandbox and never provisions one.
func (m *SessionBoundManager) RemoveSessionInputPath(
	ctx context.Context, sessionID, targetPath string,
) error {
	if m == nil || m.GetType() != SandboxTypeCube {
		return ErrSandboxDisabled
	}
	clean, err := cleanSessionInputPath(targetPath)
	if err != nil {
		return err
	}
	info, ok := m.liveSandboxInfoForSession(sessionID)
	if !ok {
		return nil
	}
	if err := m.client.Remove(ctx, info, clean); err != nil {
		return fmt.Errorf("sandbox: remove session input %s: %w", clean, err)
	}
	if v, exists := m.sandboxes.Load(sessionID); exists {
		sb := v.(*CubeSandbox)
		sb.mu.Lock()
		sb.lastUsed = time.Now()
		sb.mu.Unlock()
	}
	return nil
}

func cleanSessionInputPath(filePath string) (string, error) {
	clean := path.Clean(strings.TrimSpace(filePath))
	if clean == SessionInputRoot || strings.HasPrefix(clean, SessionInputRoot+"/") {
		return clean, nil
	}
	return "", fmt.Errorf("sandbox: session input path %q is outside %s", filePath, SessionInputRoot)
}

// ListSessionFiles enumerates the contents of dir inside the session's live
// sandbox recursively. Returns an empty slice (and no error) when the session
// has no bound sandbox yet, so callers can treat "no sandbox" and "no files"
// uniformly.
//
// Each returned DirEntry has an absolute .Path. Directories are followed
// depth-first, but the entries returned to the caller only contain files;
// intermediate directories are traversed transparently.
func (m *SessionBoundManager) ListSessionFiles(ctx context.Context, sessionID, dir string) ([]DirEntry, error) {
	info, ok := m.liveSandboxInfoForSession(sessionID)
	if !ok {
		return nil, nil
	}
	if dir == "" {
		return nil, fmt.Errorf("sandbox: dir required for ListSessionFiles")
	}
	return m.listFilesRecursive(ctx, info, dir)
}

// listFilesRecursive walks dir under the given sandbox and returns a flat
// slice of file entries. Non-existent directories are treated as empty so
// callers don't have to distinguish "never created" from "created but
// empty". Errors reading individual sub-directories propagate.
func (m *SessionBoundManager) listFilesRecursive(ctx context.Context, info *SandboxInfo, dir string) ([]DirEntry, error) {
	// Guard against a missing root: envd returns an error for a non-existent
	// path, but from WeKnora's perspective "the skill has not produced
	// anything yet" is the common case, not a failure.
	if stat, err := m.client.Stat(ctx, info, dir); err != nil {
		return nil, fmt.Errorf("sandbox: stat %s: %w", dir, err)
	} else if stat == nil {
		return nil, nil
	}

	stack := []string{dir}
	var files []DirEntry
	for len(stack) > 0 {
		// Pop.
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		entries, err := m.client.ListDir(ctx, info, cur)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.Path == "" {
				// envd sometimes returns Path relative to the parent; make
				// sure the caller always gets an absolute path.
				e.Path = cur + "/" + e.Name
			}
			if e.Type == "dir" || e.Type == "directory" {
				stack = append(stack, e.Path)
				continue
			}
			files = append(files, e)
		}
	}
	return files, nil
}

// StatSessionFile returns metadata for a file in an already-live session
// sandbox without downloading its contents or lazily creating a MicroVM.
func (m *SessionBoundManager) StatSessionFile(ctx context.Context, sessionID, path string) (*StatEntry, error) {
	info, ok := m.liveSandboxInfoForSession(sessionID)
	if !ok {
		return nil, fmt.Errorf("sandbox: no live sandbox for session %s", sessionID)
	}
	if path == "" {
		return nil, fmt.Errorf("sandbox: path required for StatSessionFile")
	}
	return m.client.Stat(ctx, info, path)
}

// ReadSessionFile downloads a file from the session's live sandbox. Returns
// an error when no sandbox is bound — callers are expected to have chosen
// the path via a prior ListSessionFiles call, so "no sandbox" here means the
// caller lost a race with reaper or DestroySession.
func (m *SessionBoundManager) ReadSessionFile(ctx context.Context, sessionID, path string) ([]byte, error) {
	info, ok := m.liveSandboxInfoForSession(sessionID)
	if !ok {
		return nil, fmt.Errorf("sandbox: no live sandbox for session %s", sessionID)
	}
	if path == "" {
		return nil, fmt.Errorf("sandbox: path required for ReadSessionFile")
	}
	return m.client.ReadFile(ctx, info, path)
}

// ExecShellCommand runs an ad-hoc shell command inside the session's
// persistent Cube sandbox. Unlike Execute (which uploads a script file and
// invokes an interpreter), this path is the LLM-facing "run a shell one-liner"
// primitive intended for skill dependency preparation, environment probing,
// and other lightweight operations the agent needs to perform between
// skill invocations.
//
// Design notes:
//   - Lazily provisions the MicroVM on first call, mirroring how Execute
//     behaves for the same session. This keeps chat-only sessions free of
//     MicroVM allocation until a tool actually needs it.
//   - Bypasses ScriptValidator on purpose: shell commands are not python
//     script contents and the validator's rules do not model them. Session
//     isolation on the Cube side is the real security boundary; the tool
//     layer applies a lightweight command-shape blacklist.
//   - Fallback (LocalSandbox) is explicitly refused: shell_exec must not
//     escape onto the host machine. Callers should feature-gate registration
//     on GetType() == SandboxTypeCube.
//   - Errors from Cube are wrapped so callers can distinguish "sandbox not
//     bound / unreachable" from "command ran and returned non-zero".
func (m *SessionBoundManager) ExecShellCommand(
	ctx context.Context,
	sessionID string,
	command string,
	workDir string,
	timeout time.Duration,
	env map[string]string,
) (*ExecuteResult, error) {
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

	// shell_exec is a Cube-only capability. If we fell back to LocalSandbox
	// at construction time, refuse the call rather than run the command on
	// the WeKnora host.
	if fallback != nil {
		return nil, fmt.Errorf("sandbox: shell_exec requires the Cube backend but the fallback (%s) is in effect", fallback.Type())
	}

	if sessionID == "" {
		return nil, fmt.Errorf("sandbox: session_id required for ExecShellCommand")
	}
	if command == "" {
		return nil, fmt.Errorf("sandbox: command required for ExecShellCommand")
	}

	if timeout <= 0 {
		timeout = m.config.DefaultTimeout
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	// Ensure a sandbox is bound to this session. resolveSandbox creates one
	// lazily (persistent mode) so subsequent shell_exec + execute_skill_script
	// calls share state (installed pip packages etc.).
	sb, err := m.resolveSandbox(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	info, err := sb.ensureSandbox(ctx)
	if err != nil {
		return nil, err
	}

	// Materialise the working directory so `cd` in RunCommand doesn't fail
	// on a first-run session that has never written to it. MakeDir is a no-op
	// when the directory already exists.
	if workDir != "" {
		if mkErr := m.client.MakeDir(ctx, info, workDir); mkErr != nil {
			log.Printf("[sandbox] shell_exec: MakeDir %s failed (continuing): %v", workDir, mkErr)
		}
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	// Use RunShell (not RunCommand) so `command` is forwarded to
	// `/bin/bash -l -c` verbatim. RunCommand would shell-quote the whole
	// string and try to exec it as a single binary — turning something
	// like `pip install foo` into `command not found: pip install foo`.
	cmdRes, err := m.client.RunShell(
		execCtx,
		info,
		command,
		env,
		workDir,
	)
	duration := time.Since(start)

	// Refresh last-used so the reaper counts this call as activity.
	sb.mu.Lock()
	sb.lastUsed = time.Now()
	sb.mu.Unlock()

	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			return &ExecuteResult{
				Duration: duration,
				Killed:   true,
				ExitCode: -1,
				Error:    ErrTimeout.Error(),
			}, nil
		}
		return &ExecuteResult{
			Duration: duration,
			ExitCode: -1,
			Error:    err.Error(),
		}, nil
	}

	return &ExecuteResult{
		Stdout:   cmdRes.Stdout,
		Stderr:   cmdRes.Stderr,
		ExitCode: cmdRes.ExitCode,
		Duration: duration,
		Killed:   cmdRes.Killed,
	}, nil
}

// Cleanup destroys every remaining sandbox and stops the reaper. It is safe
// to call multiple times.
func (m *SessionBoundManager) Cleanup(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	cancel := m.reaperCancel
	m.reaperCancel = nil
	fallback := m.fallback
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	m.reaperWG.Wait()

	var firstErr error
	m.sandboxes.Range(func(k, v any) bool {
		sb := v.(*CubeSandbox)
		m.sandboxes.Delete(k)
		if err := sb.Cleanup(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
		return true
	})
	if fallback != nil {
		if err := fallback.Cleanup(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// startReaper launches the background goroutine that kills idle sandboxes.
// The tick interval is CubeIdleTTL / 4 so idle sandboxes are reaped
// promptly even for very short TTLs; a lower bound of 15s prevents
// pathological configurations from hammering the API.
func (m *SessionBoundManager) startReaper() {
	if m.config.CubeIdleTTL <= 0 {
		return
	}
	interval := m.config.CubeIdleTTL / 4
	if interval < 15*time.Second {
		interval = 15 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.reaperCancel = cancel
	m.reaperWG.Add(1)
	go func() {
		defer m.reaperWG.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.reapOnce(ctx)
			}
		}
	}()
}

// reapOnce walks the sandbox map once and kills anything that has been idle
// for longer than CubeIdleTTL * idleReapMultiplier.
func (m *SessionBoundManager) reapOnce(ctx context.Context) {
	idleThreshold := m.config.CubeIdleTTL * idleReapMultiplier
	cutoff := time.Now().Add(-idleThreshold)

	m.sandboxes.Range(func(k, v any) bool {
		sb := v.(*CubeSandbox)
		last := sb.LastUsed()
		if last.IsZero() {
			// Never used since being registered — skip on this pass to give
			// the caller a chance to actually run something.
			return true
		}
		if last.Before(cutoff) {
			m.sandboxes.Delete(k)
			if err := sb.Cleanup(ctx); err != nil {
				log.Printf("[sandbox] reaper: cleanup session %v: %v", k, err)
			} else {
				log.Printf("[sandbox] reaper: reclaimed idle sandbox for session %v", k)
			}
		}
		return true
	})
}

// withEphemeral toggles the ephemeral flag on a CubeSandbox and returns the
// same pointer for fluent style. Kept private because it's only useful to
// the manager's dispatch logic.
func (s *CubeSandbox) withEphemeral(v bool) *CubeSandbox {
	s.ephemeral = v
	return s
}
